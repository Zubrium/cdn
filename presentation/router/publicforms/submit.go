package publicforms

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/Zubrium/cdn/pkg/contracts"
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
		return nil, contracts.ErrPublicInvalid
	}
	return &SubmitOperation{api: api, maxRequestBytes: maxRequestBytes}, nil
}

func (operation *SubmitOperation) Descriptor() contracts.PublicOperationDescriptor {
	if operation == nil {
		return contracts.PublicOperationDescriptor{}
	}
	return contracts.PublicOperationDescriptor{ID: SubmitOperationID, ContractVersion: 1,
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

func (operation *SubmitOperation) Execute(ctx context.Context, raw json.RawMessage, request contracts.PublicOperationRequest) (contracts.DeliveryResponse, error) {
	if operation == nil || operation.api == nil || ctx == nil {
		return contracts.DeliveryResponse{}, contracts.ErrPublicUnavailable
	}
	config, err := decodeBinding(raw)
	if err != nil {
		return contracts.DeliveryResponse{}, contracts.ErrInvalidTarget
	}
	binding := config.Binding()
	if request.Method != http.MethodPost || request.Query != "" || request.ContentLength < -1 || request.ContentLength == 0 ||
		request.ContentLength > operation.maxRequestBytes || request.IdempotencyKey == "" || len(request.EscapedPath) > 8192 {
		if request.ContentLength > operation.maxRequestBytes {
			return contracts.DeliveryResponse{}, contracts.ErrPublicTooLarge
		}
		return contracts.DeliveryResponse{}, contracts.ErrPublicInvalid
	}
	token := tokenFor(request, binding.FormID)
	if token == "" {
		return formErrorResponse(contracts.ErrPublicNotFound, binding.FormID, false)
	}
	var body submitBody
	if err := readJSON(request, operation.maxRequestBytes, &body); err != nil {
		return formErrorResponse(err, binding.FormID, false)
	}
	if body.Values == nil {
		return formErrorResponse(contracts.ErrPublicInvalid, binding.FormID, false)
	}
	receipt, err := operation.api.SubmitVisitorResponse(ctx, contracts.PublicSubmitVisitorResponseRequest{
		PublicFormBinding: binding, Token: token, IdempotencyKey: request.IdempotencyKey, Values: body.Values,
	})
	if err != nil {
		return formErrorResponse(err, binding.FormID, errors.Is(err, contracts.ErrPublicExpired) || errors.Is(err, contracts.ErrPublicNotFound))
	}
	// Keep the capability cookie after success so a client that did not receive
	// this response can safely retry with the same idempotency key and receive
	// the original receipt. A new session replaces the cookie; DELETE clears it.
	return successResponse(http.StatusCreated, receipt, nil)
}
