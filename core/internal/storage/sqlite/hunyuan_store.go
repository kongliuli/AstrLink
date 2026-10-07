package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	storagecontract "github.com/QuantumNous/astrlink/core/internal/storage"
)

var (
	ErrHunyuanBudgetExhausted    = errors.New(contract.HunyuanErrBudgetExhausted)
	ErrHunyuanBudgetUnguaranteed = errors.New(contract.HunyuanErrBudgetUnguaranteed)
	ErrHunyuanConcurrencyLimit   = errors.New(contract.HunyuanErrConcurrencyLimit)
	ErrHunyuanTransition         = errors.New("invocation already finished or canceled")
)

const hunyuanDefaultProject = contract.HunyuanDefaultProject

type HunyuanInvocationRecord struct {
	ID                    string
	ProjectID             string
	AccessTokenID         string
	Status                string
	RequestedModel        string
	ActualModel           string
	Provider              string
	TaskRef               string
	BlueprintRef          string
	InputHash             string
	InputBytes            int
	OutputJSON            string
	UsageJSON             string
	CostStatus            string
	CostAmount            string
	ErrorCode             string
	ErrorMessage          string
	TraceID               string
	IdempotencyKey        string
	RequestHash           string
	BudgetPolicyRef       string
	CancelAccepted        bool
	LocalConnectionClosed bool
	RemoteStop            string
	CreatedAt             time.Time
	UpdatedAt             time.Time
	FinishedAt            *time.Time
}

type HunyuanCreateResult struct {
	Record   HunyuanInvocationRecord
	Created  bool
	Conflict bool
}

func (store *Store) EnsureHunyuanProjectBinding(ctx context.Context, tokenID contract.AccessTokenID, projectID string) error {
	if tokenID == "" || strings.TrimSpace(projectID) == "" {
		return fmt.Errorf("hunyuan project binding is invalid")
	}
	_, err := store.db.ExecContext(ctx, `
INSERT INTO hunyuan_project_bindings(access_token_id, project_id)
SELECT ?, ? WHERE EXISTS (SELECT 1 FROM local_access_tokens WHERE id = ?)
ON CONFLICT(access_token_id) DO NOTHING`, string(tokenID), projectID, string(tokenID))
	if err != nil {
		return err
	}
	bound, err := store.HunyuanProjectForToken(ctx, tokenID)
	if err != nil {
		return err
	}
	if bound != projectID {
		return fmt.Errorf("token is already bound to another hunyuan project")
	}
	return nil
}

func (store *Store) HunyuanProjectForToken(ctx context.Context, tokenID contract.AccessTokenID) (string, error) {
	var projectID string
	err := store.db.QueryRowContext(ctx,
		`SELECT project_id FROM hunyuan_project_bindings WHERE access_token_id = ?`,
		string(tokenID),
	).Scan(&projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", storagecontract.ErrNotFound
	}
	return projectID, err
}

func (store *Store) BindAllAccessTokensToHunyuanDev(ctx context.Context) error {
	tokens, err := store.ListAccessTokens(ctx)
	if err != nil {
		return err
	}
	for _, token := range tokens {
		if err := store.EnsureHunyuanProjectBinding(ctx, token.ID, hunyuanDefaultProject); err != nil {
			return err
		}
	}
	return nil
}

func (store *Store) SetHunyuanBudget(ctx context.Context, projectID, policyRef string, remaining, maxInflight int) error {
	_, err := store.db.ExecContext(ctx, `
INSERT INTO hunyuan_budget(project_id, policy_ref, remaining, inflight, max_inflight)
VALUES (?, ?, ?, 0, ?)
ON CONFLICT(project_id, policy_ref) DO UPDATE SET
 remaining = excluded.remaining,
 max_inflight = excluded.max_inflight`, projectID, policyRef, remaining, maxInflight)
	return err
}

func (store *Store) CreateHunyuanInvocation(ctx context.Context, record HunyuanInvocationRecord) (HunyuanCreateResult, error) {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return HunyuanCreateResult{}, err
	}
	defer func() { _ = tx.Rollback() }()

	if record.IdempotencyKey != "" {
		var existingID, existingHash string
		err := tx.QueryRowContext(ctx, `
SELECT invocation_id, request_hash FROM hunyuan_idempotency
WHERE project_id = ? AND idempotency_key = ?`, record.ProjectID, record.IdempotencyKey).Scan(&existingID, &existingHash)
		if err == nil {
			if existingHash != record.RequestHash {
				return HunyuanCreateResult{Conflict: true}, nil
			}
			got, getErr := getHunyuanInvocationTx(ctx, tx, existingID)
			if getErr != nil {
				return HunyuanCreateResult{}, getErr
			}
			if err := tx.Commit(); err != nil {
				return HunyuanCreateResult{}, err
			}
			return HunyuanCreateResult{Record: got, Created: false}, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return HunyuanCreateResult{}, err
		}
	}

	if record.BudgetPolicyRef == "unguaranteed" {
		return HunyuanCreateResult{}, ErrHunyuanBudgetUnguaranteed
	}

	res, err := tx.ExecContext(ctx, `
UPDATE hunyuan_budget
SET remaining = remaining - 1, inflight = inflight + 1
WHERE project_id = ? AND policy_ref = ? AND remaining >= 1 AND inflight < max_inflight`,
		record.ProjectID, record.BudgetPolicyRef)
	if err != nil {
		return HunyuanCreateResult{}, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return HunyuanCreateResult{}, err
	}
	if affected == 0 {
		var remaining, inflight, maxInflight int
		qerr := tx.QueryRowContext(ctx, `
SELECT remaining, inflight, max_inflight FROM hunyuan_budget
WHERE project_id = ? AND policy_ref = ?`, record.ProjectID, record.BudgetPolicyRef).Scan(&remaining, &inflight, &maxInflight)
		if errors.Is(qerr, sql.ErrNoRows) {
			return HunyuanCreateResult{}, ErrHunyuanBudgetUnguaranteed
		}
		if qerr != nil {
			return HunyuanCreateResult{}, qerr
		}
		if remaining < 1 {
			return HunyuanCreateResult{}, ErrHunyuanBudgetExhausted
		}
		return HunyuanCreateResult{}, ErrHunyuanConcurrencyLimit
	}

	now := store.now().UTC().Format(time.RFC3339Nano)
	record.CreatedAt = store.now().UTC()
	record.UpdatedAt = record.CreatedAt
	record.Status = contract.HunyuanStatusReserved
	record.CostStatus = contract.HunyuanCostUnknown
	record.RemoteStop = contract.HunyuanRemoteStopNA
	if _, err := tx.ExecContext(ctx, `
INSERT INTO hunyuan_invocations(
 id, project_id, access_token_id, status, requested_model, actual_model, provider,
 task_ref, blueprint_ref, input_hash, input_bytes, output_json, usage_json,
 cost_status, cost_amount, error_code, error_message, trace_id, idempotency_key,
 request_hash, budget_policy_ref, cancel_accepted, local_connection_closed, remote_stop,
 created_at, updated_at, finished_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, 0, ?, ?, ?, NULL)`,
		record.ID, record.ProjectID, record.AccessTokenID, record.Status, record.RequestedModel,
		nullIfEmpty(record.ActualModel), nullIfEmpty(record.Provider), nullIfEmpty(record.TaskRef),
		nullIfEmpty(record.BlueprintRef), record.InputHash, record.InputBytes, nullIfEmpty(record.OutputJSON),
		nullIfEmpty(record.UsageJSON), record.CostStatus, nullIfEmpty(record.CostAmount),
		nullIfEmpty(record.ErrorCode), nullIfEmpty(record.ErrorMessage), record.TraceID,
		nullIfEmpty(record.IdempotencyKey), record.RequestHash, record.BudgetPolicyRef,
		record.RemoteStop, now, now,
	); err != nil {
		return HunyuanCreateResult{}, err
	}
	if record.IdempotencyKey != "" {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO hunyuan_idempotency(project_id, idempotency_key, request_hash, invocation_id)
VALUES (?, ?, ?, ?)`, record.ProjectID, record.IdempotencyKey, record.RequestHash, record.ID); err != nil {
			return HunyuanCreateResult{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return HunyuanCreateResult{}, err
	}
	return HunyuanCreateResult{Record: record, Created: true}, nil
}

func (store *Store) GetHunyuanInvocation(ctx context.Context, id, projectID string) (HunyuanInvocationRecord, error) {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return HunyuanInvocationRecord{}, err
	}
	defer func() { _ = tx.Rollback() }()
	record, err := getHunyuanInvocationTx(ctx, tx, id)
	if err != nil {
		return HunyuanInvocationRecord{}, err
	}
	if record.ProjectID != projectID {
		return HunyuanInvocationRecord{}, storagecontract.ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return HunyuanInvocationRecord{}, err
	}
	return record, nil
}

func (store *Store) UpdateHunyuanInvocation(ctx context.Context, record HunyuanInvocationRecord) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	current, err := getHunyuanInvocationTx(ctx, tx, record.ID)
	if err != nil {
		return err
	}
	if current.ProjectID != record.ProjectID || current.FinishedAt != nil {
		return ErrHunyuanTransition
	}
	if current.CancelAccepted {
		if record.FinishedAt == nil {
			return ErrHunyuanTransition
		}
		record.Status = contract.HunyuanStatusCanceled
		record.ErrorCode = "canceled"
		record.ErrorMessage = "invocation canceled"
		record.OutputJSON = ""
		record.CancelAccepted = true
		record.RemoteStop = contract.HunyuanRemoteStopUnknown
	}
	if record.FinishedAt == nil && current.Status != contract.HunyuanStatusReserved {
		return ErrHunyuanTransition
	}
	now := store.now().UTC().Format(time.RFC3339Nano)
	var finished any
	if record.FinishedAt != nil {
		finished = record.FinishedAt.UTC().Format(time.RFC3339Nano)
	}
	_, err = tx.ExecContext(ctx, `
UPDATE hunyuan_invocations SET
 status=?, actual_model=?, provider=?, output_json=?, usage_json=?, cost_status=?,
 cost_amount=?, error_code=?, error_message=?, cancel_accepted=?, local_connection_closed=?,
 remote_stop=?, updated_at=?, finished_at=?
WHERE id=?`,
		record.Status, nullIfEmpty(record.ActualModel), nullIfEmpty(record.Provider),
		nullIfEmpty(record.OutputJSON), nullIfEmpty(record.UsageJSON), record.CostStatus,
		nullIfEmpty(record.CostAmount), nullIfEmpty(record.ErrorCode), nullIfEmpty(record.ErrorMessage),
		boolInt(record.CancelAccepted), boolInt(record.LocalConnectionClosed), record.RemoteStop,
		now, finished, record.ID)
	if err != nil {
		return err
	}
	if record.FinishedAt != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE hunyuan_budget SET inflight = inflight - 1 WHERE project_id = ? AND policy_ref = ? AND inflight > 0`, record.ProjectID, record.BudgetPolicyRef); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (store *Store) CancelHunyuanInvocation(ctx context.Context, id, projectID string) (HunyuanInvocationRecord, bool, error) {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return HunyuanInvocationRecord{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	record, err := getHunyuanInvocationTx(ctx, tx, id)
	if err != nil {
		return record, false, err
	}
	if record.ProjectID != projectID {
		return HunyuanInvocationRecord{}, false, storagecontract.ErrNotFound
	}
	if record.FinishedAt != nil {
		return record, false, tx.Commit()
	}
	record.CancelAccepted = true
	record.RemoteStop = contract.HunyuanRemoteStopUnknown
	now := store.now().UTC()
	record.UpdatedAt = now
	if record.Status == contract.HunyuanStatusReserved {
		record.Status = contract.HunyuanStatusCanceled
		record.FinishedAt = &now
		record.ErrorCode = "canceled"
		record.ErrorMessage = "canceled before dispatch"
		if _, err := tx.ExecContext(ctx, `UPDATE hunyuan_budget SET inflight = inflight - 1 WHERE project_id = ? AND policy_ref = ? AND inflight > 0`, projectID, record.BudgetPolicyRef); err != nil {
			return record, false, err
		}
	}
	var finished any
	if record.FinishedAt != nil {
		finished = now.Format(time.RFC3339Nano)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE hunyuan_invocations SET status=?, cancel_accepted=1, remote_stop=?, error_code=?, error_message=?, updated_at=?, finished_at=? WHERE id=?`, record.Status, record.RemoteStop, nullIfEmpty(record.ErrorCode), nullIfEmpty(record.ErrorMessage), now.Format(time.RFC3339Nano), finished, id); err != nil {
		return record, false, err
	}
	return record, true, tx.Commit()
}

func (store *Store) RecoverPendingHunyuanInvocations(ctx context.Context) (int64, error) {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	now := store.now().UTC().Format(time.RFC3339Nano)
	result, err := tx.ExecContext(ctx, `UPDATE hunyuan_invocations SET
	 status=CASE WHEN status IN ('accepted','reserved') THEN 'canceled' ELSE 'unknown' END,
	 error_code=CASE WHEN status IN ('accepted','reserved') THEN 'canceled' ELSE 'unknown' END,
	 error_message='core stopped before a terminal result; not retried',
	 cost_status='unknown', remote_stop='unknown', updated_at=?, finished_at=? WHERE finished_at IS NULL`, now, now)
	if err != nil {
		return 0, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE hunyuan_budget SET inflight=(SELECT COUNT(*) FROM hunyuan_invocations i WHERE i.project_id=hunyuan_budget.project_id AND i.budget_policy_ref=hunyuan_budget.policy_ref AND i.finished_at IS NULL)`); err != nil {
		return 0, err
	}
	return count, tx.Commit()
}

func getHunyuanInvocationTx(ctx context.Context, tx *sql.Tx, id string) (HunyuanInvocationRecord, error) {
	var record HunyuanInvocationRecord
	var actual, provider, task, blueprint, output, usage, costAmount, errCode, errMsg, idem, finished sql.NullString
	var cancel, closed int
	var created, updated string
	err := tx.QueryRowContext(ctx, `
SELECT id, project_id, access_token_id, status, requested_model, actual_model, provider,
 task_ref, blueprint_ref, input_hash, input_bytes, output_json, usage_json, cost_status,
 cost_amount, error_code, error_message, trace_id, idempotency_key, request_hash,
 budget_policy_ref, cancel_accepted, local_connection_closed, remote_stop,
 created_at, updated_at, finished_at
FROM hunyuan_invocations WHERE id = ?`, id).Scan(
		&record.ID, &record.ProjectID, &record.AccessTokenID, &record.Status, &record.RequestedModel,
		&actual, &provider, &task, &blueprint, &record.InputHash, &record.InputBytes, &output, &usage,
		&record.CostStatus, &costAmount, &errCode, &errMsg, &record.TraceID, &idem, &record.RequestHash,
		&record.BudgetPolicyRef, &cancel, &closed, &record.RemoteStop, &created, &updated, &finished,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return HunyuanInvocationRecord{}, storagecontract.ErrNotFound
	}
	if err != nil {
		return HunyuanInvocationRecord{}, err
	}
	record.ActualModel = actual.String
	record.Provider = provider.String
	record.TaskRef = task.String
	record.BlueprintRef = blueprint.String
	record.OutputJSON = output.String
	record.UsageJSON = usage.String
	record.CostAmount = costAmount.String
	record.ErrorCode = errCode.String
	record.ErrorMessage = errMsg.String
	record.IdempotencyKey = idem.String
	record.CancelAccepted = cancel == 1
	record.LocalConnectionClosed = closed == 1
	record.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	record.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	if finished.Valid {
		t, _ := time.Parse(time.RFC3339Nano, finished.String)
		record.FinishedAt = &t
	}
	return record, nil
}

func nullIfEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
