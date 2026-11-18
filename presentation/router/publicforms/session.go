package publicforms

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	forms "github.com/Zubrium/cdn/engines/forms/presentation"
	router "github.com/Zubrium/cdn/engines/router/presentation"
)

type SessionOperation struct{ api VisitorAPI }

func NewSessionOperation(api VisitorAPI) (*SessionOperation, error) {
	if api == nil {
		return nil, router.ErrUnavailable
	}
	return &SessionOperation{api: api}, nil
}

func (operation *SessionOperation) Descriptor() router.PublicOperationDescriptor {
	return router.PublicOperationDescriptor{ID: SessionOperationID, ContractVersion: 1,
		Methods: []string{http.MethodGet, http.MethodPost, http.MethodDelete}, MaxRequestBytes: 1,
		AllowedContentTypes: []string{"application/json"}}
}

func (operation *SessionOperation) ValidateRouteConfig(raw json.RawMessage) error {
	_, err := decodeBinding(raw)
	return err
}

func (operation *SessionOperation) ValidateRouteConfigWithContext(ctx context.Context, raw json.RawMessage) error {
	config, err := decodeBinding(raw)
	if err != nil {
		return err
	}
	return validateBindingWithForms(ctx, operation.api, config)
}

func (operation *SessionOperation) Execute(ctx context.Context, raw json.RawMessage, request router.PublicOperationRequest) (router.DeliveryResponse, error) {
	if operation == nil || operation.api == nil || ctx == nil {
		return router.DeliveryResponse{}, router.ErrUnavailable
	}
	config, err := decodeBinding(raw)
	if err != nil {
		return router.DeliveryResponse{}, router.ErrInvalidTarget
	}
	if request.Query != "" || len(request.EscapedPath) > 8192 || requireEmptyBody(request) != nil {
		return router.DeliveryResponse{}, router.ErrInvalid
	}
	binding := config.Binding()
	token := tokenFor(request, binding.FormID)
	switch request.Method {
	case http.MethodGet:
		if token == "" {
			return formErrorResponse(forms.ErrNotFound, binding.FormID, false)
		}
		session, err := operation.api.GetVisitorSession(ctx, forms.GetVisitorSessionRequest{PublicFormBinding: binding, Token: token})
		if err != nil {
			return formErrorResponse(err, binding.FormID, true)
		}
		cookie := &router.FormSessionCookieChange{FormID: binding.FormID, Token: token, ExpiresAt: session.ExpiresAt}
		return successResponse(http.StatusOK, session, cookie)
	case http.MethodPost:
		if token != "" {
			session, err := operation.api.GetVisitorSession(ctx, forms.GetVisitorSessionRequest{PublicFormBinding: binding, Token: token})
			if err == nil && !session.Completed {
				cookie := &router.FormSessionCookieChange{FormID: binding.FormID, Token: token, ExpiresAt: session.ExpiresAt}
				return successResponse(http.StatusOK, session, cookie)
			}
			if errors.Is(err, forms.ErrConflict) {
				// A fixed route whose schema version has changed cannot resume the
				// old capability. Clear the stale cookie and require a fresh route
				// binding before accepting new work.
				return formErrorResponse(err, binding.FormID, true)
			}
			if err != nil && !errors.Is(err, forms.ErrExpired) && !errors.Is(err, forms.ErrNotFound) && !errors.Is(err, forms.ErrUnauthorized) {
				return formErrorResponse(err, binding.FormID, false)
			}
		}
		created, err := operation.api.CreateVisitorSession(ctx, forms.CreateVisitorSessionRequest{PublicFormBinding: binding})
		if err != nil {
			return formErrorResponse(err, binding.FormID, token != "")
		}
		cookie := &router.FormSessionCookieChange{FormID: binding.FormID, Token: created.Token, ExpiresAt: created.Session.ExpiresAt}
		return successResponse(http.StatusCreated, created.Session, cookie)
	case http.MethodDelete:
		cookie := &router.FormSessionCookieChange{FormID: binding.FormID, Clear: true}
		if token != "" {
			err := operation.api.CancelVisitorSession(ctx, forms.CancelVisitorSessionRequest{PublicFormBinding: binding, Token: token})
			if err != nil && !errors.Is(err, forms.ErrConflict) && !errors.Is(err, forms.ErrExpired) && !errors.Is(err, forms.ErrNotFound) {
				return formErrorResponse(err, binding.FormID, false)
			}
		}
		return emptyResponse(http.StatusNoContent, cookie)
	default:
		return router.DeliveryResponse{}, router.ErrInvalid
	}
}
