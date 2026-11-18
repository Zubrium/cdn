package publicforms

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	forms "github.com/Zubrium/cdn/engines/forms/presentation"
	router "github.com/Zubrium/cdn/engines/router/presentation"
)

type SubmitOperation struct {
	api             VisitorAPI
	maxRequestBytes int64
}

type submitBody struct {
	Values map[string]any `json:"values"`
}

func NewSubmitOperation(api VisitorAPI, maxRequestBytes int64) (*SubmitOperation, error) {
	if api == nil || maxRequestBytes < 1 || maxRequestBytes > 16<<20 {
		return nil, router.ErrInvalid
	}
	return &SubmitOperation{api: api, maxRequestBytes: maxRequestBytes}, nil
}

func (operation *SubmitOperation) Descriptor() router.PublicOperationDescriptor {
	if operation == nil {
		return router.PublicOperationDescriptor{}
	}
	return router.PublicOperationDescriptor{ID: SubmitOperationID, ContractVersion: 1,
		Methods: []string{http.MethodPost}, MaxRequestBytes: operation.maxRequestBytes,
		AllowedContentTypes: []string{"application/json"}}
}

func (operation *SubmitOperation) ValidateRouteConfig(raw json.RawMessage) error {
	_, err := decodeBinding(raw)
	return err
}

func (operation *SubmitOperation) ValidateRouteConfigWithContext(ctx context.Context, raw json.RawMessage) error {
	config, err := decodeBinding(raw)
	if err != nil {
		return err
	}
	return validateBindingWithForms(ctx, operation.api, config)
}

func (operation *SubmitOperation) Execute(ctx context.Context, raw json.RawMessage, request router.PublicOperationRequest) (router.DeliveryResponse, error) {
	if operation == nil || operation.api == nil || ctx == nil {
		return router.DeliveryResponse{}, router.ErrUnavailable
	}
	config, err := decodeBinding(raw)
	if err != nil {
		return router.DeliveryResponse{}, router.ErrInvalidTarget
	}
	binding := config.Binding()
	if request.Method != http.MethodPost || request.Query != "" || request.ContentLength < -1 || request.ContentLength == 0 ||
		request.ContentLength > operation.maxRequestBytes || request.IdempotencyKey == "" || len(request.EscapedPath) > 8192 {
		if request.ContentLength > operation.maxRequestBytes {
			return router.DeliveryResponse{}, router.ErrTooLarge
		}
		return router.DeliveryResponse{}, router.ErrInvalid
	}
	token := tokenFor(request, binding.FormID)
	if token == "" {
		return formErrorResponse(forms.ErrNotFound, binding.FormID, false)
	}
	var body submitBody
	if err := readJSON(request, operation.maxRequestBytes, &body); err != nil {
		return formErrorResponse(err, binding.FormID, false)
	}
	if body.Values == nil {
		return formErrorResponse(forms.ErrInvalid, binding.FormID, false)
	}
	receipt, err := operation.api.SubmitVisitorResponse(ctx, forms.SubmitVisitorResponseRequest{
		PublicFormBinding: binding, Token: token, IdempotencyKey: request.IdempotencyKey, Values: body.Values,
	})
	if err != nil {
		return formErrorResponse(err, binding.FormID, errors.Is(err, forms.ErrExpired) || errors.Is(err, forms.ErrNotFound))
	}
	// Keep the capability cookie after success so a client that did not receive
	// this response can safely retry with the same idempotency key and receive
	// the original receipt. A new session replaces the cookie; DELETE clears it.
	return successResponse(http.StatusCreated, receipt, nil)
}
