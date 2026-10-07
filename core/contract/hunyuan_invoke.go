package contract

const (
	HunyuanAIPrefix = "/hunyuan/ai/v1"

	HunyuanStatusAccepted    = "accepted"
	HunyuanStatusReserved    = "reserved"
	HunyuanStatusDispatching = "dispatching"
	HunyuanStatusRunning     = "running"
	HunyuanStatusStreaming   = "streaming"
	HunyuanStatusUnknown     = "unknown"
	HunyuanStatusSucceeded   = "succeeded"
	HunyuanStatusFailed      = "failed"
	HunyuanStatusCanceled    = "canceled"
	HunyuanStatusTimedOut    = "timed_out"

	HunyuanCostUnknown        = "unknown"
	HunyuanCostKnown          = "known"
	HunyuanCostNotApplicable  = "not_applicable"
	HunyuanRemoteStopUnknown  = "unknown"
	HunyuanRemoteStopStopped  = "stopped"
	HunyuanRemoteStopNA       = "not_applicable"
	HunyuanSupportSupported   = "supported"
	HunyuanSupportUnsupported = "unsupported"
	HunyuanSupportUnknown     = "unknown"

	HunyuanErrUnauthorized          = "unauthorized"
	HunyuanErrProjectMismatch       = "project_mismatch"
	HunyuanErrIdempotencyConflict   = "idempotency_conflict"
	HunyuanErrBudgetExhausted       = "budget_exhausted"
	HunyuanErrBudgetUnguaranteed    = "budget_unguaranteed"
	HunyuanErrConcurrencyLimit      = "concurrency_limit"
	HunyuanErrCapabilityUnsupported = "capability_unsupported"
	HunyuanErrSchemaInvalid         = "schema_invalid"
	HunyuanErrSchemaUnsupported     = "schema_unsupported"
	HunyuanErrTimeout               = "timeout"
	HunyuanErrCancelRace            = "cancel_race"
	HunyuanErrNotConnected          = "not_connected"
	HunyuanErrInvocationNotFound    = "invocation_not_found"
	HunyuanErrInvalidRequest        = "invalid_request"

	HunyuanDefaultProject = "hunyuan-dev"
	HunyuanDefaultBudget  = "default"
	HunyuanMockProvider   = "mock"
)

func HunyuanTerminal(status string) bool {
	switch status {
	case HunyuanStatusSucceeded, HunyuanStatusFailed, HunyuanStatusCanceled, HunyuanStatusTimedOut:
		return true
	default:
		return false
	}
}
