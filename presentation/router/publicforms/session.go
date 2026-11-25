package publicforms

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/Zubrium/cdn/pkg/contracts"
)

type SessionOperation struct{ api VisitorAPI }

func NewSessionOperation(api VisitorAPI) (*SessionOperation, error) {
	if api == nil {
		return nil, contracts.ErrPublicUnavailable
	}
	return &SessionOperation{api: api}, nil
}

func (operation *SessionOperation) Descriptor() contracts.PublicOperationDescriptor {
	return contracts.PublicOperationDescriptor{ID: SessionOperationID, ContractVersion: 1,
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

func (operation *SessionOperation) Execute(ctx context.Context, raw json.RawMessage, request contracts.PublicOperationRequest) (contracts.DeliveryResponse, error) {
	if operation == nil || operation.api == nil || ctx == nil {
		return contracts.DeliveryResponse{}, contracts.ErrPublicUnavailable
	}
	config, err := decodeBinding(raw)
	if err != nil {
		return contracts.DeliveryResponse{}, contracts.ErrInvalidTarget
	}
	if request.Query != "" || len(request.EscapedPath) > 8192 || requireEmptyBody(request) != nil {
		return contracts.DeliveryResponse{}, contracts.ErrPublicInvalid
	}
	binding := config.Binding()
	token := tokenFor(request, binding.FormID)
	switch request.Method {
	case http.MethodGet:
		if token == "" {
			return formErrorResponse(contracts.ErrPublicNotFound, binding.FormID, false)
		}
		session, err := operation.api.GetVisitorSession(ctx, contracts.PublicGetVisitorSessionRequest{PublicFormBinding: binding, Token: token})
		if err != nil {
			return formErrorResponse(err, binding.FormID, true)
		}
		cookie := &contracts.FormSessionCookieChange{FormID: binding.FormID, Token: token, ExpiresAt: session.ExpiresAt}
		return successResponse(http.StatusOK, session, cookie)
	case http.MethodPost:
		if token != "" {
			session, err := operation.api.GetVisitorSession(ctx, contracts.PublicGetVisitorSessionRequest{PublicFormBinding: binding, Token: token})
			if err == nil && !session.Completed {
				cookie := &contracts.FormSessionCookieChange{FormID: binding.FormID, Token: token, ExpiresAt: session.ExpiresAt}
				return successResponse(http.StatusOK, session, cookie)
			}
			if errors.Is(err, contracts.ErrPublicConflict) {
				// A fixed route whose schema version has changed cannot resume the
				// old capability. Clear the stale cookie and require a fresh route
				// binding before accepting new work.
				return formErrorResponse(err, binding.FormID, true)
			}
			if err != nil && !errors.Is(err, contracts.ErrPublicExpired) && !errors.Is(err, contracts.ErrPublicNotFound) && !errors.Is(err, contracts.ErrPublicUnauthorized) {
				return formErrorResponse(err, binding.FormID, false)
			}
		}
		created, err := operation.api.CreateVisitorSession(ctx, binding)
		if err != nil {
			return formErrorResponse(err, binding.FormID, token != "")
		}
		cookie := &contracts.FormSessionCookieChange{FormID: binding.FormID, Token: created.Token, ExpiresAt: created.Session.ExpiresAt}
		return successResponse(http.StatusCreated, created.Session, cookie)
	case http.MethodDelete:
		cookie := &contracts.FormSessionCookieChange{FormID: binding.FormID, Clear: true}
		if token != "" {
			err := operation.api.CancelVisitorSession(ctx, contracts.PublicCancelVisitorSessionRequest{PublicFormBinding: binding, Token: token})
			if err != nil && !errors.Is(err, contracts.ErrPublicConflict) && !errors.Is(err, contracts.ErrPublicExpired) && !errors.Is(err, contracts.ErrPublicNotFound) {
				return formErrorResponse(err, binding.FormID, false)
			}
		}
		return emptyResponse(http.StatusNoContent, cookie)
	default:
		return contracts.DeliveryResponse{}, contracts.ErrPublicInvalid
	}
}
