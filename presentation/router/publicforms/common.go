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

	forms "github.com/Zubrium/cdn/engines/forms/presentation"
	router "github.com/Zubrium/cdn/engines/router/presentation"
	httpjson "github.com/Zubrium/cdn/pkg/json"
	"github.com/google/uuid"
)

const (
	SessionOperationID = "public.form-session"
	SubmitOperationID  = "public.form-submit"
)

type VisitorAPI interface {
	ValidatePublicForm(context.Context, forms.PublicFormBinding) error
	ValidatePublicUploadRoute(context.Context, forms.PublicUploadRouteBinding) error
	CreateVisitorSession(context.Context, forms.CreateVisitorSessionRequest) (forms.VisitorSessionCredential, error)
	GetVisitorSession(context.Context, forms.GetVisitorSessionRequest) (forms.VisitorSession, error)
	BeginVisitorUpload(context.Context, forms.BeginVisitorUploadRequest) (forms.VisitorUploadReservation, error)
	CompleteVisitorUpload(context.Context, forms.CompleteVisitorUploadRequest) (forms.VisitorAttachment, error)
	AbortVisitorUpload(context.Context, forms.AbortVisitorUploadRequest) error
	CancelVisitorSession(context.Context, forms.CancelVisitorSessionRequest) error
	SubmitVisitorResponse(context.Context, forms.SubmitVisitorResponseRequest) (forms.VisitorSubmissionReceipt, error)
}

type BindingConfig struct {
	Enabled       bool   `json:"enabled"`
	SnapshotID    string `json:"snapshot_id"`
	FormID        string `json:"form_id"`
	SchemaVersion int64  `json:"schema_version"`
}

func (config BindingConfig) Binding() forms.PublicFormBinding {
	return forms.PublicFormBinding{SnapshotID: config.SnapshotID, FormID: config.FormID, SchemaVersion: config.SchemaVersion}
}

func (config BindingConfig) Validate() error {
	if !config.Enabled || config.Binding().Validate() != nil {
		return router.ErrInvalidTarget
	}
	for _, rawID := range []string{config.SnapshotID, config.FormID} {
		id, err := uuid.Parse(rawID)
		if err != nil || id == uuid.Nil || id.String() != rawID {
			return router.ErrInvalidTarget
		}
	}
	return nil
}

func decodeBinding(raw json.RawMessage) (BindingConfig, error) {
	if len(raw) == 0 || len(raw) > 64<<10 {
		return BindingConfig{}, router.ErrInvalidTarget
	}
	var config BindingConfig
	if err := httpjson.DecodeBytes(raw, &config); err != nil || config.Validate() != nil {
		return BindingConfig{}, router.ErrInvalidTarget
	}
	return config, nil
}

func validateBindingWithForms(ctx context.Context, api VisitorAPI, config BindingConfig) error {
	if api == nil || ctx == nil {
		return router.ErrUnavailable
	}
	if err := config.Validate(); err != nil {
		return err
	}
	if err := api.ValidatePublicForm(ctx, config.Binding()); err != nil {
		return mapFormsError(err)
	}
	return nil
}

func tokenFor(request router.PublicOperationRequest, formID string) string {
	return request.FormSessionTokens[formID]
}

func successResponse(status int, value any, cookie *router.FormSessionCookieChange) (router.DeliveryResponse, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return router.DeliveryResponse{}, router.ErrUnavailable
	}
	return router.DeliveryResponse{StatusCode: status, ContentType: "application/json; charset=utf-8", ContentLength: int64(len(body)),
		CacheControl: "no-store", Body: io.NopCloser(bytes.NewReader(body)), FormSessionCookie: cookie}, nil
}

func emptyResponse(status int, cookie *router.FormSessionCookieChange) (router.DeliveryResponse, error) {
	return router.DeliveryResponse{StatusCode: status, ContentLength: 0, CacheControl: "no-store", FormSessionCookie: cookie}, nil
}

func formErrorResponse(err error, formID string, clearCookie bool) (router.DeliveryResponse, error) {
	status := http.StatusServiceUnavailable
	code := "unavailable"
	switch {
	case errors.Is(err, forms.ErrInvalid):
		status, code = http.StatusBadRequest, "invalid_request"
	case errors.Is(err, forms.ErrTooLarge):
		status, code = http.StatusRequestEntityTooLarge, "request_too_large"
	case errors.Is(err, forms.ErrNotFound), errors.Is(err, forms.ErrExpired), errors.Is(err, forms.ErrUnauthorized):
		status, code, clearCookie = http.StatusNotFound, "not_found", true
	case errors.Is(err, forms.ErrConflict):
		status, code = http.StatusConflict, "conflict"
	case errors.Is(err, forms.ErrForbidden):
		status, code = http.StatusForbidden, "forbidden"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return router.DeliveryResponse{}, err
	}
	var cookie *router.FormSessionCookieChange
	if clearCookie && formID != "" {
		cookie = &router.FormSessionCookieChange{FormID: formID, Clear: true}
	}
	return successResponse(status, struct {
		Error string `json:"error"`
	}{Error: code}, cookie)
}

// ErrorResponse lets sibling Router operations return the same stable public
// Forms errors and cookie-clearing behavior without exposing HTTP cookies to
// the Forms engine.
func ErrorResponse(err error, formID string, clearCookie bool) (router.DeliveryResponse, error) {
	return formErrorResponse(err, formID, clearCookie)
}

func mapFormsError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, forms.ErrUnauthorized):
		return router.ErrUnauthorized
	case errors.Is(err, forms.ErrForbidden):
		return router.ErrForbidden
	case errors.Is(err, forms.ErrNotFound), errors.Is(err, forms.ErrExpired):
		return router.ErrNotFound
	case errors.Is(err, forms.ErrConflict):
		return router.ErrConflict
	case errors.Is(err, forms.ErrInvalid):
		return router.ErrInvalid
	case errors.Is(err, forms.ErrTooLarge):
		return router.ErrTooLarge
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	default:
		return router.ErrUnavailable
	}
}

// MapFormsError maps Forms application errors to the Router operation
// contract. It is exported for upload operations implemented by sibling
// Router packages.
func MapFormsError(err error) error { return mapFormsError(err) }

func readJSON(request router.PublicOperationRequest, maxBytes int64, target any) error {
	if request.Body == nil || maxBytes <= 0 {
		return router.ErrInvalid
	}
	raw, err := io.ReadAll(io.LimitReader(request.Body, maxBytes+1))
	if err != nil {
		return router.ErrInvalid
	}
	if int64(len(raw)) > maxBytes {
		return router.ErrTooLarge
	}
	if err := httpjson.DecodeBytes(raw, target); err != nil {
		return router.ErrInvalid
	}
	return nil
}

func requireEmptyBody(request router.PublicOperationRequest) error {
	if request.Body == nil || request.ContentLength > 0 || request.ContentLength < -1 {
		return router.ErrInvalid
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, 2))
	if err != nil || len(body) != 0 {
		return router.ErrInvalid
	}
	return nil
}

var _ router.PublicOperation = (*SessionOperation)(nil)
var _ router.PublicOperation = (*SubmitOperation)(nil)
