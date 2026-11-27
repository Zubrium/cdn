package contracts

import (
	"errors"
)

// Durable page-retirement workflow operations. Every route target and the page
// identity are server-owned; the root payload cannot select a worker class, an
// engine or a Router operation.
const (
	RetirementStartOperationID        = "retirement.retire.start"
	RetirementRouteRemoveOperationID  = "retirement.route.remove"
	RetirementPageFinalizeOperationID = "retirement.page.finalize"

	RetirementStartContractVersion = 1
	RetirementChildContractVersion = 1

	RetirementWorkflowFormat          = "retirement.retire/v1"
	RetirementRouteRemoveResultFormat = "retirement.route.remove/v1"
	RetirementFinalizeResultFormat    = "retirement.page.finalize/v1"

	RetirementPageStateActive   = "active"
	RetirementPageStateRetiring = "retiring"
	RetirementPageStateRetired  = "retired"

	RetirementRouteStateRemoved   = "removed"
	RetirementRouteStateRetryable = "retryable_failed"

	RetirementJobStateRunning   = "running"
	RetirementJobStateSucceeded = "succeeded"
	RetirementJobStateRetryable = "retryable_failed"
)

type RetirementWorkflowPayload struct {
	JobID            string   `json:"job_id"`
	PageID           string   `json:"page_id"`
	ExpectedRevision int64    `json:"expected_revision"`
	IfMatch          string   `json:"if_match"`
	IdempotencyKey   string   `json:"idempotency_key"`
	Routes           []string `json:"routes"`
}

type RetirementRouteRemovePayload struct {
	JobID          string `json:"job_id"`
	PageID         string `json:"page_id"`
	RouteID        string `json:"route_id"`
	IdempotencyKey string `json:"idempotency_key"`
}

type RetirementRouteRemoveResult struct {
	Format    string `json:"format"`
	JobID     string `json:"job_id"`
	RouteID   string `json:"route_id"`
	State     string `json:"state"`
	Revision  int64  `json:"revision,omitempty"`
	ErrorCode string `json:"error_code,omitempty"`
}

type RetirementPageFinalizePayload struct {
	JobID            string `json:"job_id"`
	PageID           string `json:"page_id"`
	ExpectedRevision int64  `json:"expected_revision"`
	IdempotencyKey   string `json:"idempotency_key"`
}

type RetirementPageFinalizeResult struct {
	Format string `json:"format"`
	JobID  string `json:"job_id"`
	PageID string `json:"page_id"`
	State  string `json:"state"`
}

type RetirementStartResult struct {
	Format              string   `json:"format"`
	JobID               string   `json:"job_id"`
	PageID              string   `json:"page_id"`
	RouteEmissionKeys   []string `json:"route_emission_keys"`
	FinalizeEmissionKey string   `json:"finalize_emission_key"`
}

func ValidateRetirementWorkflowPayload(payload RetirementWorkflowPayload) error {
	if !retirementPageIDPattern.MatchString(payload.JobID) || !retirementPageIDPattern.MatchString(payload.PageID) ||
		payload.ExpectedRevision < 1 || !retirementIfMatchPattern.MatchString(payload.IfMatch) ||
		!retirementKeyPattern.MatchString(payload.IdempotencyKey) || len(payload.Routes) == 0 || len(payload.Routes) > 64 {
		return ErrInvalidRetirementRequest
	}
	seen := make(map[string]struct{}, len(payload.Routes))
	for _, routeID := range payload.Routes {
		if !retirementPageIDPattern.MatchString(routeID) {
			return ErrInvalidRetirementRequest
		}
		if _, duplicate := seen[routeID]; duplicate {
			return ErrInvalidRetirementRequest
		}
		seen[routeID] = struct{}{}
	}
	return nil
}

func ValidateRetirementRouteRemovePayload(payload RetirementRouteRemovePayload) error {
	if !retirementPageIDPattern.MatchString(payload.JobID) || !retirementPageIDPattern.MatchString(payload.PageID) ||
		!retirementPageIDPattern.MatchString(payload.RouteID) || !retirementKeyPattern.MatchString(payload.IdempotencyKey) {
		return ErrInvalidRetirementRequest
	}
	return nil
}

func ValidateRetirementPageFinalizePayload(payload RetirementPageFinalizePayload) error {
	if !retirementPageIDPattern.MatchString(payload.JobID) || !retirementPageIDPattern.MatchString(payload.PageID) ||
		payload.ExpectedRevision < 1 || !retirementKeyPattern.MatchString(payload.IdempotencyKey) {
		return ErrInvalidRetirementRequest
	}
	return nil
}

func RetirementRouteEmissionKey(jobID, routeID string) string {
	return "retirement.route:" + jobID + ":" + routeID
}

func RetirementFinalizeEmissionKey(jobID string) string {
	return "retirement.finalize:" + jobID
}

func RetirementRouteIdempotencyKey(payload RetirementRouteRemovePayload) string {
	return "retire-route:" + payload.JobID + ":" + payload.RouteID
}

func RetirementFinalizeIdempotencyKey(payload RetirementPageFinalizePayload) string {
	return "retire-page:" + payload.JobID
}

// RetirementRouteErrorCode is the stable bounded error code recorded on a
// retryable route outcome. No engine error text is exposed to a caller.
func RetirementRouteErrorCode(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, ErrRetirementNotFound) {
		return "not_found"
	}
	if errors.Is(err, ErrRetirementConflict) {
		return "revision_conflict"
	}
	if errors.Is(err, ErrRetirementUnavailable) {
		return "unavailable"
	}
	return "route_failed"
}
