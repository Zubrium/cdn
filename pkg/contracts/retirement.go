package contracts

import (
	"errors"
	"regexp"
)

var (
	ErrInvalidRetirementRequest = errors.New("invalid page retirement request")
	ErrRetirementUnavailable    = errors.New("page retirement capability is unavailable")
	ErrRetirementConflict       = errors.New("page retirement revision conflict")
	ErrRetirementNotFound       = errors.New("page retirement job not found")
)

var (
	retirementPageIDPattern  = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	retirementIfMatchPattern = regexp.MustCompile(`^"[1-9][0-9]*"$`)
	retirementKeyPattern     = regexp.MustCompile(`^[A-Za-z0-9._:-]{16,128}$`)
)

// RetirePageRequest is the process-local trigger consumed by the trusted
// composition root. The page identity and every Router route target come from
// server-owned state; the HTTP client only supplies an If-Match token and an
// idempotency key.
type RetirePageRequest struct {
	PageID         string `json:"page_id"`
	IfMatch        string `json:"if_match"`
	IdempotencyKey string `json:"idempotency_key"`
}

type PageRetirementRoute struct {
	RouteID   string `json:"route_id"`
	State     string `json:"state"`
	ErrorCode string `json:"error_code,omitempty"`
}

type PageRetirement struct {
	ID     string                `json:"id"`
	PageID string                `json:"page_id"`
	State  string                `json:"state"`
	Routes []PageRetirementRoute `json:"routes"`
}

type PageRetirementQuery struct {
	PageID string `json:"page_id"`
	JobID  string `json:"job_id"`
}

func RetirePageOperation() Operation[RetirePageRequest, PageRetirement] {
	return Operation[RetirePageRequest, PageRetirement]{
		Key: Key{ID: "main.retirement.retire", Version: 1},
		Validate: func(request RetirePageRequest) error {
			if !retirementPageIDPattern.MatchString(request.PageID) || !retirementIfMatchPattern.MatchString(request.IfMatch) ||
				!retirementKeyPattern.MatchString(request.IdempotencyKey) {
				return ErrInvalidRetirementRequest
			}
			return nil
		},
	}
}

func GetPageRetirementOperation() Operation[PageRetirementQuery, PageRetirement] {
	return Operation[PageRetirementQuery, PageRetirement]{
		Key: Key{ID: "main.retirement.status", Version: 1},
		Validate: func(request PageRetirementQuery) error {
			if !retirementPageIDPattern.MatchString(request.PageID) || !retirementPageIDPattern.MatchString(request.JobID) {
				return ErrInvalidRetirementRequest
			}
			return nil
		},
	}
}
