// Package publicforms implements Router operations for anonymous form flows.
// It contains no HTTP cookie parsing; Router's HTTP adapter owns that boundary.
package publicforms

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/Zubrium/cdn/pkg/contracts"
	httpjson "github.com/Zubrium/cdn/pkg/json"
	"github.com/google/uuid"
)

const (
	SessionOperationID = "public.form-session"
	SubmitOperationID  = "public.form-submit"
)

// VisitorAPI is the process-local DataBus client. Router no longer receives
// an engine facade; the dispatcher fixes module identity outside the payload.
type VisitorAPI = contracts.PublicFormsVisitor

type BindingConfig struct {
	Enabled       bool   `json:"enabled"`
	FormID        string `json:"form_id"`
	SchemaVersion int64  `json:"schema_version"`
}

func (config BindingConfig) Binding() contracts.PublicFormBinding {
	return contracts.PublicFormBinding{FormID: config.FormID, SchemaVersion: config.SchemaVersion}
}

func (config BindingConfig) Validate() error {
	if !config.Enabled || config.Binding().Validate() != nil {
		return contracts.ErrInvalidTarget
	}
	for _, rawID := range []string{config.FormID} {
		id, err := uuid.Parse(rawID)
		if err != nil || id == uuid.Nil || id.String() != rawID {
			return contracts.ErrInvalidTarget
		}
	}
	return nil
}

func decodeBinding(raw json.RawMessage) (BindingConfig, error) {
	if len(raw) == 0 || len(raw) > 64<<10 {
		return BindingConfig{}, contracts.ErrInvalidTarget
	}
	var config BindingConfig
	if err := httpjson.DecodeBytes(raw, &config); err != nil || config.Validate() != nil {
		return BindingConfig{}, contracts.ErrInvalidTarget
	}
	return config, nil
}

func validateBindingWithForms(ctx context.Context, api VisitorAPI, config BindingConfig) error {
	if api == nil || ctx == nil {
		return contracts.ErrPublicUnavailable
	}
	if err := config.Validate(); err != nil {
		return err
	}
	if err := api.ValidatePublicForm(ctx, config.Binding()); err != nil {
		return mapFormsError(err)
	}
	return nil
}

func tokenFor(request contracts.PublicOperationRequest, formID string) string {
	return request.FormSessionTokens[formID]
}

func successResponse(status int, value any, cookie *contracts.FormSessionCookieChange) (contracts.DeliveryResponse, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return contracts.DeliveryResponse{}, contracts.ErrPublicUnavailable
	}
	return contracts.DeliveryResponse{StatusCode: status, ContentType: "application/json; charset=utf-8", ContentLength: int64(len(body)),
		CacheControl: "no-store", Body: io.NopCloser(bytes.NewReader(body)), FormSessionCookie: cookie}, nil
}

func emptyResponse(status int, cookie *contracts.FormSessionCookieChange) (contracts.DeliveryResponse, error) {
	return contracts.DeliveryResponse{StatusCode: status, ContentLength: 0, CacheControl: "no-store", FormSessionCookie: cookie}, nil
}

func formErrorResponse(err error, formID string, clearCookie bool) (contracts.DeliveryResponse, error) {
	status := http.StatusServiceUnavailable
	code := "unavailable"
	switch {
	case errors.Is(err, contracts.ErrPublicInvalid):
		status, code = http.StatusBadRequest, "invalid_request"
	case errors.Is(err, contracts.ErrPublicTooLarge):
		status, code = http.StatusRequestEntityTooLarge, "request_too_large"
	case errors.Is(err, contracts.ErrPublicNotFound), errors.Is(err, contracts.ErrPublicExpired), errors.Is(err, contracts.ErrPublicUnauthorized):
		status, code, clearCookie = http.StatusNotFound, "not_found", true
	case errors.Is(err, contracts.ErrPublicConflict):
		status, code = http.StatusConflict, "conflict"
	case errors.Is(err, contracts.ErrPublicForbidden):
		status, code = http.StatusForbidden, "forbidden"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return contracts.DeliveryResponse{}, err
	}
	var cookie *contracts.FormSessionCookieChange
	if clearCookie && formID != "" {
		cookie = &contracts.FormSessionCookieChange{FormID: formID, Clear: true}
	}
	return successResponse(status, struct {
		Error string `json:"error"`
	}{Error: code}, cookie)
}

// ErrorResponse lets sibling Router operations return the same stable public
// Forms errors and cookie-clearing behavior without exposing HTTP cookies to
// the Forms engine.
func ErrorResponse(err error, formID string, clearCookie bool) (contracts.DeliveryResponse, error) {
	return formErrorResponse(err, formID, clearCookie)
}

func mapFormsError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, contracts.ErrPublicUnauthorized):
		return contracts.ErrPublicUnauthorized
	case errors.Is(err, contracts.ErrPublicForbidden):
		return contracts.ErrPublicForbidden
	case errors.Is(err, contracts.ErrPublicNotFound), errors.Is(err, contracts.ErrPublicExpired):
		return contracts.ErrPublicNotFound
	case errors.Is(err, contracts.ErrPublicConflict):
		return contracts.ErrPublicConflict
	case errors.Is(err, contracts.ErrPublicInvalid):
		return contracts.ErrPublicInvalid
	case errors.Is(err, contracts.ErrPublicTooLarge):
		return contracts.ErrPublicTooLarge
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	default:
		return contracts.ErrPublicUnavailable
	}
}

// MapFormsError maps Forms application errors to the Router operation
// contract. It is exported for upload operations implemented by sibling
// Router packages.
func MapFormsError(err error) error { return mapFormsError(err) }

func readJSON(request contracts.PublicOperationRequest, maxBytes int64, target any) error {
	if request.Body == nil || maxBytes <= 0 {
		return contracts.ErrPublicInvalid
	}
	raw, err := io.ReadAll(io.LimitReader(request.Body, maxBytes+1))
	if err != nil {
		return contracts.ErrPublicInvalid
	}
	if int64(len(raw)) > maxBytes {
		return contracts.ErrPublicTooLarge
	}
	if err := httpjson.DecodeBytes(raw, target); err != nil {
		return contracts.ErrPublicInvalid
	}
	return nil
}

func requireEmptyBody(request contracts.PublicOperationRequest) error {
	if request.Body == nil || request.ContentLength > 0 || request.ContentLength < -1 {
		return contracts.ErrPublicInvalid
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, 2))
	if err != nil || len(body) != 0 {
		return contracts.ErrPublicInvalid
	}
	return nil
}

var _ contracts.PublicOperation = (*SessionOperation)(nil)
var _ contracts.PublicOperation = (*SubmitOperation)(nil)
