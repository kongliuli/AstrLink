package hunyuanapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/ingress"
	"github.com/QuantumNous/astrlink/core/internal/storage/sqlite"
)

type Store interface {
	HunyuanProjectForToken(context.Context, contract.AccessTokenID) (string, error)
	CreateHunyuanInvocation(context.Context, sqlite.HunyuanInvocationRecord) (sqlite.HunyuanCreateResult, error)
	GetHunyuanInvocation(context.Context, string, string) (sqlite.HunyuanInvocationRecord, error)
	UpdateHunyuanInvocation(context.Context, sqlite.HunyuanInvocationRecord) error
	CancelHunyuanInvocation(context.Context, string, string) (sqlite.HunyuanInvocationRecord, bool, error)
}

type Handler struct {
	store         Store
	authenticator ingress.AccessTokenAuthenticator
	provider      Provider
	mu            sync.Mutex
	cancels       map[string]context.CancelFunc
}

type Provider interface {
	Name() string
	Capabilities() []ModelCapability
	Support(model, feature string) string
	Generate(ctx context.Context, req InvokeRequest, emit func(string) error) (Output, error)
}

type ModelCapability struct {
	ID               string `json:"id"`
	Stream           string `json:"stream"`
	Tools            string `json:"tools"`
	StructuredOutput string `json:"structured_output"`
	Notes            string `json:"notes,omitempty"`
}

type InvokeRequest struct {
	Project         string          `json:"project,omitempty"`
	TaskRef         string          `json:"task_ref,omitempty"`
	BlueprintRef    string          `json:"blueprint_ref,omitempty"`
	Model           string          `json:"model"`
	Input           string          `json:"input"`
	MaxOutputTokens int             `json:"max_output_tokens,omitempty"`
	TimeoutMS       int             `json:"timeout_ms,omitempty"`
	BudgetPolicyRef string          `json:"budget_policy_ref,omitempty"`
	ResponseSchema  json.RawMessage `json:"response_schema,omitempty"`
	Tools           []ToolDecl      `json:"tools,omitempty"`
	IdempotencyKey  string          `json:"-"`
}

type ToolDecl struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type Output struct {
	Type        string          `json:"type"`
	Text        string          `json:"text,omitempty"`
	JSON        json.RawMessage `json:"json,omitempty"`
	ToolCalls   []ToolCall      `json:"tool_calls,omitempty"`
	Usage       *Usage          `json:"-"`
	ActualModel string          `json:"-"`
}

type ToolCall struct {
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type ErrorBody struct {
	Error ErrorInfo `json:"error"`
}

type ErrorInfo struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type InvocationView struct {
	InvocationID   string     `json:"invocation_id"`
	Status         string     `json:"status"`
	RequestedModel string     `json:"requested_model"`
	ActualModel    *string    `json:"actual_model"`
	Provider       *string    `json:"provider"`
	Output         *Output    `json:"output,omitempty"`
	Usage          *Usage     `json:"usage,omitempty"`
	CostStatus     string     `json:"cost_status"`
	Cost           *float64   `json:"cost"`
	Error          *ErrorInfo `json:"error"`
	TraceID        string     `json:"trace_id"`
}

type Usage struct {
	InputTokens  int   `json:"input_tokens,omitempty"`
	OutputTokens int   `json:"output_tokens,omitempty"`
	DurationMS   int64 `json:"duration_ms,omitempty"`
}

type CancelResponse struct {
	Invocation            InvocationView `json:"invocation"`
	CancelAccepted        bool           `json:"cancel_accepted"`
	LocalConnectionClosed bool           `json:"local_connection_closed"`
	RemoteStop            string         `json:"remote_stop"`
	CostStatus            string         `json:"cost_status"`
	Cost                  *float64       `json:"cost"`
}

func New(store Store, authenticator ingress.AccessTokenAuthenticator, provider Provider) *Handler {
	if provider == nil {
		provider = MockProvider{}
	}
	return &Handler{
		store:         store,
		authenticator: authenticator,
		provider:      provider,
		cancels:       map[string]context.CancelFunc{},
	}
}

func Mount(next http.Handler, hunyuan *Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasPrefix(request.URL.Path, contract.HunyuanAIPrefix) {
			if base, ok := next.(*ingress.Handler); ok && !base.CheckInferenceBoundary(writer, request) {
				return
			}
			hunyuan.ServeHTTP(writer, request)
			return
		}
		next.ServeHTTP(writer, request)
	})
}

func (handler *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	path := strings.TrimPrefix(request.URL.Path, contract.HunyuanAIPrefix)
	if path == "" {
		path = "/"
	}
	if strings.HasPrefix(path, "/agent/") || path == "/agent" {
		writeHunyuanError(writer, http.StatusNotImplemented, contract.HunyuanErrNotConnected, "agent execution is not connected")
		return
	}
	tokenID, ok := handler.authenticate(writer, request)
	if !ok {
		return
	}
	projectID, err := handler.store.HunyuanProjectForToken(request.Context(), tokenID)
	if err != nil {
		writeHunyuanError(writer, http.StatusForbidden, contract.HunyuanErrUnauthorized, "token is not bound to a hunyuan project")
		return
	}
	switch {
	case request.Method == http.MethodGet && path == "/capabilities":
		handler.getCapabilities(writer)
	case request.Method == http.MethodPost && path == "/invoke":
		handler.postInvoke(writer, request, tokenID, projectID)
	case request.Method == http.MethodGet && strings.HasPrefix(path, "/invocations/"):
		handler.getInvocation(writer, request, projectID, strings.TrimPrefix(path, "/invocations/"))
	case request.Method == http.MethodPost && strings.HasSuffix(path, "/cancel") && strings.HasPrefix(path, "/invocations/"):
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/invocations/"), "/cancel")
		handler.postCancel(writer, request, projectID, id)
	default:
		writeHunyuanError(writer, http.StatusNotFound, "not_found", "hunyuan path not found")
	}
}

func (handler *Handler) authenticate(writer http.ResponseWriter, request *http.Request) (contract.AccessTokenID, bool) {
	if request.URL.Query().Has("key") || request.URL.Query().Has("api_key") || request.URL.Query().Has("access_token") {
		writeHunyuanError(writer, http.StatusUnauthorized, contract.HunyuanErrUnauthorized, "tokens are not accepted in query parameters")
		return "", false
	}
	raw, ok := localToken(request.Header)
	if !ok || handler.authenticator == nil {
		writer.Header().Set("WWW-Authenticate", `Bearer realm="astrlink-inference"`)
		writeHunyuanError(writer, http.StatusUnauthorized, contract.HunyuanErrUnauthorized, "a valid local access token is required")
		return "", false
	}
	id, err := handler.authenticator.AuthenticateAccessToken(request.Context(), raw)
	if err != nil || id == "" {
		writer.Header().Set("WWW-Authenticate", `Bearer realm="astrlink-inference"`)
		writeHunyuanError(writer, http.StatusUnauthorized, contract.HunyuanErrUnauthorized, "a valid local access token is required")
		return "", false
	}
	return id, true
}

func localToken(header http.Header) (string, bool) {
	if bearer := header.Get("Authorization"); strings.HasPrefix(strings.ToLower(bearer), "bearer ") {
		return strings.TrimSpace(bearer[7:]), true
	}
	if key := strings.TrimSpace(header.Get("X-Api-Key")); key != "" {
		return key, true
	}
	return "", false
}

func (handler *Handler) getCapabilities(writer http.ResponseWriter) {
	writeJSON(writer, http.StatusOK, map[string]any{"models": handler.provider.Capabilities()})
}

func (handler *Handler) postInvoke(writer http.ResponseWriter, request *http.Request, tokenID contract.AccessTokenID, projectID string) {
	body, err := io.ReadAll(io.LimitReader(request.Body, (1<<20)+1))
	if err != nil {
		writeHunyuanError(writer, http.StatusBadRequest, contract.HunyuanErrInvalidRequest, "could not read body")
		return
	}
	if len(body) > 1<<20 {
		writeHunyuanError(writer, http.StatusRequestEntityTooLarge, contract.HunyuanErrInvalidRequest, "invoke body exceeds size limit")
		return
	}
	var req InvokeRequest
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeHunyuanError(writer, http.StatusUnprocessableEntity, contract.HunyuanErrInvalidRequest, "invalid invoke body")
		return
	}
	if dec.Decode(new(any)) != io.EOF || req.MaxOutputTokens < 0 || req.TimeoutMS < 0 || req.TimeoutMS > 86400000 {
		writeHunyuanError(writer, http.StatusUnprocessableEntity, contract.HunyuanErrInvalidRequest, "invalid invoke limits or trailing JSON")
		return
	}
	if len(req.ResponseSchema) > 0 {
		var schema map[string]any
		if err := json.Unmarshal(req.ResponseSchema, &schema); err != nil {
			writeHunyuanError(writer, http.StatusUnprocessableEntity, contract.HunyuanErrSchemaUnsupported, "response schema must be an object")
			return
		}
		if err := checkSchema(schema); err != nil {
			writeHunyuanError(writer, http.StatusUnprocessableEntity, contract.HunyuanErrSchemaUnsupported, err.Error())
			return
		}
	}
	for _, tool := range req.Tools {
		if strings.TrimSpace(tool.Name) == "" {
			writeHunyuanError(writer, http.StatusUnprocessableEntity, contract.HunyuanErrInvalidRequest, "tool name is required")
			return
		}
		if len(tool.Parameters) > 0 {
			var schema map[string]any
			if json.Unmarshal(tool.Parameters, &schema) != nil || schema == nil {
				writeHunyuanError(writer, http.StatusUnprocessableEntity, contract.HunyuanErrInvalidRequest, "tool parameters must be an object")
				return
			}
		}
	}
	req.IdempotencyKey = strings.TrimSpace(request.Header.Get("Idempotency-Key"))
	if req.Model == "" || req.Input == "" {
		writeHunyuanError(writer, http.StatusUnprocessableEntity, contract.HunyuanErrInvalidRequest, "model and input are required")
		return
	}
	if req.Project != "" && req.Project != projectID {
		writeHunyuanError(writer, http.StatusForbidden, contract.HunyuanErrProjectMismatch, "project does not match the authenticated binding")
		return
	}
	if req.BudgetPolicyRef == "" {
		req.BudgetPolicyRef = contract.HunyuanDefaultBudget
	}
	if support := handler.provider.Support(req.Model, "model"); support == contract.HunyuanSupportUnsupported {
		writeHunyuanError(writer, http.StatusUnprocessableEntity, contract.HunyuanErrCapabilityUnsupported, "model is not supported")
		return
	}
	wantStream := strings.Contains(request.Header.Get("Accept"), "text/event-stream")
	if wantStream && handler.provider.Support(req.Model, "stream") == contract.HunyuanSupportUnsupported {
		writeHunyuanError(writer, http.StatusUnprocessableEntity, contract.HunyuanErrCapabilityUnsupported, "streaming is not supported")
		return
	}
	if len(req.Tools) > 0 && handler.provider.Support(req.Model, "tools") == contract.HunyuanSupportUnsupported {
		writeHunyuanError(writer, http.StatusUnprocessableEntity, contract.HunyuanErrCapabilityUnsupported, "tools are not supported")
		return
	}
	if len(req.ResponseSchema) > 0 && handler.provider.Support(req.Model, "structured_output") == contract.HunyuanSupportUnsupported {
		writeHunyuanError(writer, http.StatusUnprocessableEntity, contract.HunyuanErrCapabilityUnsupported, "structured output is not supported")
		return
	}
	record, created, err := handler.create(request.Context(), tokenID, projectID, req)
	if err != nil {
		handler.writeStoreError(writer, err)
		return
	}
	if !created {
		writeJSON(writer, http.StatusOK, viewOf(record))
		return
	}
	timeout := 15 * time.Second
	if req.TimeoutMS > 0 {
		timeout = time.Duration(req.TimeoutMS) * time.Millisecond
	}
	ctx, cancel := context.WithTimeout(request.Context(), timeout)
	ctx = context.WithValue(ctx, gatewayRequestKey{}, request)
	handler.mu.Lock()
	handler.cancels[record.ID] = cancel
	handler.mu.Unlock()
	defer func() {
		cancel()
		handler.mu.Lock()
		delete(handler.cancels, record.ID)
		handler.mu.Unlock()
	}()

	if wantStream {
		handler.streamInvoke(ctx, writer, request, record, req)
		return
	}
	handler.completeInvoke(ctx, writer, record, req, false)
}

func (handler *Handler) create(ctx context.Context, tokenID contract.AccessTokenID, projectID string, req InvokeRequest) (sqlite.HunyuanInvocationRecord, bool, error) {
	canonical, _ := json.Marshal(struct {
		Model  string          `json:"model"`
		Input  string          `json:"input"`
		Max    int             `json:"max"`
		Budget string          `json:"budget"`
		Schema json.RawMessage `json:"schema"`
		Tools  []ToolDecl      `json:"tools"`
		Task   string          `json:"task"`
		Blue   string          `json:"blue"`
	}{req.Model, req.Input, req.MaxOutputTokens, req.BudgetPolicyRef, req.ResponseSchema, req.Tools, req.TaskRef, req.BlueprintRef})
	sum := sha256.Sum256(canonical)
	hash := hex.EncodeToString(sum[:])
	record := sqlite.HunyuanInvocationRecord{
		ID:              "hyinv_" + randomHex(8),
		ProjectID:       projectID,
		AccessTokenID:   string(tokenID),
		RequestedModel:  req.Model,
		TaskRef:         req.TaskRef,
		BlueprintRef:    req.BlueprintRef,
		InputHash:       hash,
		InputBytes:      len(req.Input),
		TraceID:         "hytr_" + randomHex(8),
		IdempotencyKey:  req.IdempotencyKey,
		RequestHash:     hash,
		BudgetPolicyRef: req.BudgetPolicyRef,
	}
	result, err := handler.store.CreateHunyuanInvocation(ctx, record)
	if err != nil {
		return sqlite.HunyuanInvocationRecord{}, false, err
	}
	if result.Conflict {
		return sqlite.HunyuanInvocationRecord{}, false, errIdempotencyConflict
	}
	return result.Record, result.Created, nil
}

var errIdempotencyConflict = errors.New(contract.HunyuanErrIdempotencyConflict)

func (handler *Handler) completeInvoke(ctx context.Context, writer http.ResponseWriter, record sqlite.HunyuanInvocationRecord, req InvokeRequest, streamed bool) {
	started := time.Now()
	record.Status = contract.HunyuanStatusDispatching
	record.Provider = handler.provider.Name()
	record.ActualModel = req.Model
	if err := handler.dispatch(ctx, &record); err != nil {
		handler.finish(writer, record, req, Output{}, err, streamed, false, started)
		return
	}
	output, genErr := handler.provider.Generate(ctx, req, nil)
	handler.finish(writer, record, req, output, genErr, streamed, false, started)
}

func (handler *Handler) streamInvoke(ctx context.Context, writer http.ResponseWriter, request *http.Request, record sqlite.HunyuanInvocationRecord, req InvokeRequest) {
	started := time.Now()
	record.Status = contract.HunyuanStatusStreaming
	record.Provider = handler.provider.Name()
	record.ActualModel = req.Model
	if err := handler.dispatch(ctx, &record); err != nil {
		handler.finish(writer, record, req, Output{}, err, false, false, started)
		return
	}
	flusher, _ := writer.(http.Flusher)
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.WriteHeader(http.StatusOK)
	if flusher != nil {
		flusher.Flush()
	}
	var writeErr error
	output, genErr := handler.provider.Generate(ctx, req, func(delta string) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		_, err := fmt.Fprintf(writer, "event: delta\ndata: %s\n\n", jsonString(delta))
		if flusher != nil {
			flusher.Flush()
		}
		if err != nil {
			writeErr = err
		}
		return err
	})
	disconnected := request.Context().Err() != nil || writeErr != nil
	if genErr == nil && writeErr != nil {
		genErr = writeErr
	}
	handler.finish(writer, record, req, output, genErr, true, disconnected, started)
}

func (handler *Handler) finish(writer http.ResponseWriter, record sqlite.HunyuanInvocationRecord, req InvokeRequest, output Output, genErr error, streamed, disconnected bool, started time.Time) {
	now := time.Now().UTC()
	record.FinishedAt = &now
	record.CostStatus = contract.HunyuanCostUnknown
	record.CostAmount = ""
	record.ActualModel = req.Model
	if output.ActualModel != "" {
		record.ActualModel = output.ActualModel
	}
	record.Provider = handler.provider.Name()
	durationMS := time.Since(started).Milliseconds()
	switch {
	case disconnected && genErr != nil:
		record.Status = contract.HunyuanStatusUnknown
		record.LocalConnectionClosed = true
		record.ErrorCode = contract.HunyuanStatusUnknown
		record.ErrorMessage = "stream disconnected before a terminal result"
	case errors.Is(genErr, errUnknownResult):
		record.Status = contract.HunyuanStatusUnknown
		record.ErrorCode = contract.HunyuanStatusUnknown
		record.ErrorMessage = "provider result is unknown; not retried"
	case errors.Is(genErr, context.DeadlineExceeded):
		record.Status = contract.HunyuanStatusTimedOut
		record.ErrorCode = contract.HunyuanErrTimeout
		record.ErrorMessage = "invocation timed out"
	case errors.Is(genErr, context.Canceled):
		record.Status = contract.HunyuanStatusCanceled
		record.ErrorCode = "canceled"
		record.ErrorMessage = "invocation canceled"
		record.CancelAccepted = true
		record.RemoteStop = contract.HunyuanRemoteStopUnknown
	case genErr != nil:
		record.Status = contract.HunyuanStatusFailed
		record.ErrorCode = apiCode(genErr)
		record.ErrorMessage = genErr.Error()
	default:
		if len(req.ResponseSchema) > 0 {
			payload := output.JSON
			if len(payload) == 0 {
				payload = json.RawMessage(output.Text)
			}
			if err := validateInstance(req.ResponseSchema, payload); err != nil {
				record.Status = contract.HunyuanStatusFailed
				record.ErrorCode = contract.HunyuanErrSchemaInvalid
				record.ErrorMessage = err.Error()
				break
			}
			output.Type = "json"
			output.JSON = payload
		}
		encoded, err := json.Marshal(output)
		if err != nil {
			record.Status = contract.HunyuanStatusFailed
			record.ErrorCode = contract.HunyuanErrInvalidRequest
			record.ErrorMessage = "invalid provider output"
			break
		}
		record.OutputJSON = string(encoded)
		record.Status = contract.HunyuanStatusSucceeded
		measured := Usage{DurationMS: durationMS}
		if output.Usage != nil {
			measured.InputTokens = output.Usage.InputTokens
			measured.OutputTokens = output.Usage.OutputTokens
		}
		usage, _ := json.Marshal(measured)
		record.UsageJSON = string(usage)
	}
	if record.UsageJSON == "" {
		usage, _ := json.Marshal(Usage{DurationMS: durationMS})
		record.UsageJSON = string(usage)
	}
	persistCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := handler.store.UpdateHunyuanInvocation(persistCtx, record); err != nil && !errors.Is(err, sqlite.ErrHunyuanTransition) {
		if streamed {
			fmt.Fprintf(writer, "event: error\ndata: %s\n\n", mustJSON(ErrorBody{Error: ErrorInfo{Code: "internal_error", Message: "could not persist invocation result"}}))
		} else {
			handler.writeStoreError(writer, err)
		}
		return
	}
	stored, err := handler.store.GetHunyuanInvocation(persistCtx, record.ID, record.ProjectID)
	if err != nil {
		handler.writeStoreError(writer, err)
		return
	}
	record = stored
	view := viewOf(record)
	if streamed {
		if record.Status == contract.HunyuanStatusSucceeded {
			fmt.Fprintf(writer, "event: result\ndata: %s\n\n", mustJSON(view))
		} else {
			fmt.Fprintf(writer, "event: error\ndata: %s\n\n", mustJSON(ErrorBody{Error: ErrorInfo{Code: record.ErrorCode, Message: record.ErrorMessage}}))
		}
		return
	}
	if record.Status == contract.HunyuanStatusSucceeded {
		writeJSON(writer, http.StatusOK, view)
		return
	}
	status := http.StatusUnprocessableEntity
	if record.Status == contract.HunyuanStatusTimedOut {
		status = http.StatusGatewayTimeout
	}
	writeJSON(writer, status, view)
}

func (handler *Handler) getInvocation(writer http.ResponseWriter, request *http.Request, projectID, id string) {
	record, err := handler.store.GetHunyuanInvocation(request.Context(), id, projectID)
	if err != nil {
		writeHunyuanError(writer, http.StatusNotFound, contract.HunyuanErrInvocationNotFound, "invocation not found")
		return
	}
	writeJSON(writer, http.StatusOK, viewOf(record))
}

func (handler *Handler) postCancel(writer http.ResponseWriter, request *http.Request, projectID, id string) {
	record, accepted, err := handler.store.CancelHunyuanInvocation(request.Context(), id, projectID)
	if err != nil {
		writeHunyuanError(writer, http.StatusNotFound, contract.HunyuanErrInvocationNotFound, "invocation not found")
		return
	}
	handler.mu.Lock()
	cancel, running := handler.cancels[id]
	handler.mu.Unlock()
	if running && accepted {
		cancel()
	}
	writeJSON(writer, http.StatusOK, CancelResponse{
		Invocation:            viewOf(record),
		CancelAccepted:        accepted || record.CancelAccepted,
		LocalConnectionClosed: record.LocalConnectionClosed,
		RemoteStop:            record.RemoteStop,
		CostStatus:            record.CostStatus,
		Cost:                  nil,
	})
}

func (handler *Handler) dispatch(ctx context.Context, record *sqlite.HunyuanInvocationRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	project, err := handler.store.HunyuanProjectForToken(ctx, contract.AccessTokenID(record.AccessTokenID))
	if err != nil || project != record.ProjectID {
		return fmt.Errorf("%s: project binding changed before dispatch", contract.HunyuanErrProjectMismatch)
	}
	if err := handler.store.UpdateHunyuanInvocation(ctx, *record); err != nil {
		if errors.Is(err, sqlite.ErrHunyuanTransition) {
			return context.Canceled
		}
		return err
	}
	return nil
}

func (handler *Handler) writeStoreError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errIdempotencyConflict):
		writeHunyuanError(writer, http.StatusConflict, contract.HunyuanErrIdempotencyConflict, "idempotency key reused with different content")
	case errors.Is(err, sqlite.ErrHunyuanBudgetExhausted):
		writeHunyuanError(writer, http.StatusTooManyRequests, contract.HunyuanErrBudgetExhausted, "project budget is exhausted")
	case errors.Is(err, sqlite.ErrHunyuanBudgetUnguaranteed):
		writeHunyuanError(writer, http.StatusUnprocessableEntity, contract.HunyuanErrBudgetUnguaranteed, "hard budget cannot be guaranteed")
	case errors.Is(err, sqlite.ErrHunyuanConcurrencyLimit):
		writeHunyuanError(writer, http.StatusTooManyRequests, contract.HunyuanErrConcurrencyLimit, "project concurrency limit reached")
	default:
		writeHunyuanError(writer, http.StatusInternalServerError, "internal_error", err.Error())
	}
}

func viewOf(record sqlite.HunyuanInvocationRecord) InvocationView {
	view := InvocationView{
		InvocationID:   record.ID,
		Status:         record.Status,
		RequestedModel: record.RequestedModel,
		CostStatus:     record.CostStatus,
		Cost:           nil,
		TraceID:        record.TraceID,
	}
	if record.ActualModel != "" {
		view.ActualModel = &record.ActualModel
	}
	if record.Provider != "" {
		view.Provider = &record.Provider
	}
	if record.OutputJSON != "" {
		var output Output
		_ = json.Unmarshal([]byte(record.OutputJSON), &output)
		view.Output = &output
	}
	if record.UsageJSON != "" {
		var usage Usage
		_ = json.Unmarshal([]byte(record.UsageJSON), &usage)
		view.Usage = &usage
	}
	if record.ErrorCode != "" {
		view.Error = &ErrorInfo{Code: record.ErrorCode, Message: record.ErrorMessage}
	}
	return view
}

func writeHunyuanError(writer http.ResponseWriter, status int, code, message string) {
	writeJSON(writer, status, ErrorBody{Error: ErrorInfo{Code: code, Message: message}})
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func randomHex(n int) string {
	buf := make([]byte, n)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

func jsonString(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func mustJSON(value any) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func apiCode(err error) string {
	if err == nil {
		return ""
	}
	if code := err.Error(); strings.Contains(code, "unsupported") {
		return contract.HunyuanErrCapabilityUnsupported
	}
	return contract.HunyuanErrInvalidRequest
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
