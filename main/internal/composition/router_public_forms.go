package composition

import (
	"context"
	"errors"

	formapi "github.com/Zubrium/cdn/engines/forms/presentation"
	"github.com/Zubrium/cdn/pkg/contracts"
)

type routerPublicFormsProvider struct {
	guard routerPublicModuleGuard
	api   routerPublicFormsEngine
}

func (provider *routerPublicFormsProvider) authenticate(ctx context.Context) error {
	if err := provider.guard.requireModule(ctx, routerPublicFormsModuleID); err != nil {
		return err
	}
	if isNilRouterPublicValue(provider.api) {
		return contracts.ErrPublicUnavailable
	}
	return nil
}

func (provider *routerPublicFormsProvider) validateForm(ctx context.Context, request contracts.PublicFormBinding) (contracts.PublicEmptyResponse, error) {
	if err := provider.authenticate(ctx); err != nil {
		return contracts.PublicEmptyResponse{}, err
	}
	return contracts.PublicEmptyResponse{}, mapFormsPublicError(provider.api.ValidatePublicForm(ctx, publicFormBinding(request)))
}

func (provider *routerPublicFormsProvider) validateUploadRoute(ctx context.Context, request contracts.PublicUploadRouteBinding) (contracts.PublicEmptyResponse, error) {
	if err := provider.authenticate(ctx); err != nil {
		return contracts.PublicEmptyResponse{}, err
	}
	return contracts.PublicEmptyResponse{}, mapFormsPublicError(provider.api.ValidatePublicUploadRoute(ctx, formapi.PublicUploadRouteBinding{
		PublicFormBinding:   publicFormBinding(request.PublicFormBinding),
		AccessGroup:         request.AccessGroup,
		MaxBytes:            request.MaxBytes,
		AllowedContentTypes: append([]string(nil), request.AllowedContentTypes...),
	}))
}

func (provider *routerPublicFormsProvider) createSession(ctx context.Context, request contracts.PublicFormBinding) (contracts.PublicVisitorSessionCredential, error) {
	if err := provider.authenticate(ctx); err != nil {
		return contracts.PublicVisitorSessionCredential{}, err
	}
	created, err := provider.api.CreateVisitorSession(ctx, formapi.CreateVisitorSessionRequest{PublicFormBinding: publicFormBinding(request)})
	if err != nil {
		return contracts.PublicVisitorSessionCredential{}, mapFormsPublicError(err)
	}
	return contracts.PublicVisitorSessionCredential{Token: created.Token, Session: publicVisitorSession(created.Session)}, nil
}

func (provider *routerPublicFormsProvider) getSession(ctx context.Context, request contracts.PublicGetVisitorSessionRequest) (contracts.PublicVisitorSession, error) {
	if err := provider.authenticate(ctx); err != nil {
		return contracts.PublicVisitorSession{}, err
	}
	session, err := provider.api.GetVisitorSession(ctx, formapi.GetVisitorSessionRequest{
		PublicFormBinding: publicFormBinding(request.PublicFormBinding), Token: request.Token,
	})
	if err != nil {
		return contracts.PublicVisitorSession{}, mapFormsPublicError(err)
	}
	return publicVisitorSession(session), nil
}

func (provider *routerPublicFormsProvider) cancelSession(ctx context.Context, request contracts.PublicCancelVisitorSessionRequest) (contracts.PublicEmptyResponse, error) {
	if err := provider.authenticate(ctx); err != nil {
		return contracts.PublicEmptyResponse{}, err
	}
	return contracts.PublicEmptyResponse{}, mapFormsPublicError(provider.api.CancelVisitorSession(ctx, formapi.CancelVisitorSessionRequest{
		PublicFormBinding: publicFormBinding(request.PublicFormBinding), Token: request.Token,
	}))
}

func (provider *routerPublicFormsProvider) beginUpload(ctx context.Context, request contracts.PublicBeginVisitorUploadRequest) (contracts.PublicVisitorUploadReservation, error) {
	if err := provider.authenticate(ctx); err != nil {
		return contracts.PublicVisitorUploadReservation{}, err
	}
	reservation, err := provider.api.BeginVisitorUpload(ctx, formapi.BeginVisitorUploadRequest{
		PublicFormBinding: publicFormBinding(request.PublicFormBinding), Token: request.Token, FieldID: request.FieldID,
		IdempotencyKey: request.IdempotencyKey, ContentType: request.ContentType, DeclaredSize: request.DeclaredSize,
		RouteMaxBytes: request.RouteMaxBytes, RouteContentTypes: append([]string(nil), request.RouteContentTypes...),
		AccessGroup: request.AccessGroup,
	})
	if err != nil {
		return contracts.PublicVisitorUploadReservation{}, mapFormsPublicError(err)
	}
	return contracts.PublicVisitorUploadReservation{
		ID: reservation.ID, FieldID: reservation.FieldID, MaxBytes: reservation.MaxBytes, Completed: reservation.Completed,
		FileID: reservation.FileID, SHA256: reservation.SHA256, AttachmentID: reservation.AttachmentID,
		ContentType: reservation.ContentType, Size: reservation.Size, AccessGroup: reservation.AccessGroup,
	}, nil
}

func (provider *routerPublicFormsProvider) completeUpload(ctx context.Context, request contracts.PublicCompleteVisitorUploadRequest) (contracts.PublicVisitorAttachment, error) {
	if err := provider.authenticate(ctx); err != nil {
		return contracts.PublicVisitorAttachment{}, err
	}
	attachment, err := provider.api.CompleteVisitorUpload(ctx, formapi.CompleteVisitorUploadRequest{
		PublicFormBinding: publicFormBinding(request.PublicFormBinding), Token: request.Token, ReservationID: request.ReservationID,
		FileID: request.FileID, SHA256: request.SHA256, ContentType: request.ContentType, Size: request.Size, AccessGroup: request.AccessGroup,
	})
	if err != nil {
		return contracts.PublicVisitorAttachment{}, mapFormsPublicError(err)
	}
	return contracts.PublicVisitorAttachment{ID: attachment.ID, FieldID: attachment.FieldID}, nil
}

func (provider *routerPublicFormsProvider) abortUpload(ctx context.Context, request contracts.PublicAbortVisitorUploadRequest) (contracts.PublicEmptyResponse, error) {
	if err := provider.authenticate(ctx); err != nil {
		return contracts.PublicEmptyResponse{}, err
	}
	return contracts.PublicEmptyResponse{}, mapFormsPublicError(provider.api.AbortVisitorUpload(ctx, formapi.AbortVisitorUploadRequest{
		PublicFormBinding: publicFormBinding(request.PublicFormBinding), Token: request.Token, ReservationID: request.ReservationID,
	}))
}

func (provider *routerPublicFormsProvider) submitResponse(ctx context.Context, request contracts.PublicSubmitVisitorResponseRequest) (contracts.PublicVisitorSubmissionReceipt, error) {
	if err := provider.authenticate(ctx); err != nil {
		return contracts.PublicVisitorSubmissionReceipt{}, err
	}
	receipt, err := provider.api.SubmitVisitorResponse(ctx, formapi.SubmitVisitorResponseRequest{
		PublicFormBinding: publicFormBinding(request.PublicFormBinding), Token: request.Token,
		IdempotencyKey: request.IdempotencyKey, Values: request.Values,
	})
	if err != nil {
		return contracts.PublicVisitorSubmissionReceipt{}, mapFormsPublicError(err)
	}
	return contracts.PublicVisitorSubmissionReceipt{ID: receipt.ID, CreatedAt: receipt.CreatedAt}, nil
}

func publicFormBinding(binding contracts.PublicFormBinding) formapi.PublicFormBinding {
	return formapi.PublicFormBinding{FormID: binding.FormID, SchemaVersion: binding.SchemaVersion}
}

func publicVisitorSession(session formapi.VisitorSession) contracts.PublicVisitorSession {
	return contracts.PublicVisitorSession{
		ID: session.ID, FormID: session.FormID, SchemaVersion: session.SchemaVersion,
		Fields: session.Fields, ExpiresAt: session.ExpiresAt, Completed: session.Completed,
	}
}

func mapFormsPublicError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, formapi.ErrUnauthorized):
		return contracts.ErrPublicUnauthorized
	case errors.Is(err, formapi.ErrForbidden):
		return contracts.ErrPublicForbidden
	case errors.Is(err, formapi.ErrNotFound), errors.Is(err, formapi.ErrExpired):
		return contracts.ErrPublicNotFound
	case errors.Is(err, formapi.ErrConflict):
		return contracts.ErrPublicConflict
	case errors.Is(err, formapi.ErrInvalid):
		return contracts.ErrPublicInvalid
	case errors.Is(err, formapi.ErrTooLarge):
		return contracts.ErrPublicTooLarge
	default:
		return contracts.ErrPublicUnavailable
	}
}

// routerPublicFormsUnavailable keeps the compiled Forms operation set stable in
// profiles without a Forms engine. Every call fails closed at the provider.
type routerPublicFormsUnavailable struct{}

func (routerPublicFormsUnavailable) ValidatePublicForm(context.Context, formapi.PublicFormBinding) error {
	return contracts.ErrPublicUnavailable
}
func (routerPublicFormsUnavailable) ValidatePublicUploadRoute(context.Context, formapi.PublicUploadRouteBinding) error {
	return contracts.ErrPublicUnavailable
}
func (routerPublicFormsUnavailable) CreateVisitorSession(context.Context, formapi.CreateVisitorSessionRequest) (formapi.VisitorSessionCredential, error) {
	return formapi.VisitorSessionCredential{}, contracts.ErrPublicUnavailable
}
func (routerPublicFormsUnavailable) GetVisitorSession(context.Context, formapi.GetVisitorSessionRequest) (formapi.VisitorSession, error) {
	return formapi.VisitorSession{}, contracts.ErrPublicUnavailable
}
func (routerPublicFormsUnavailable) BeginVisitorUpload(context.Context, formapi.BeginVisitorUploadRequest) (formapi.VisitorUploadReservation, error) {
	return formapi.VisitorUploadReservation{}, contracts.ErrPublicUnavailable
}
func (routerPublicFormsUnavailable) CompleteVisitorUpload(context.Context, formapi.CompleteVisitorUploadRequest) (formapi.VisitorAttachment, error) {
	return formapi.VisitorAttachment{}, contracts.ErrPublicUnavailable
}
func (routerPublicFormsUnavailable) AbortVisitorUpload(context.Context, formapi.AbortVisitorUploadRequest) error {
	return contracts.ErrPublicUnavailable
}
func (routerPublicFormsUnavailable) SubmitVisitorResponse(context.Context, formapi.SubmitVisitorResponseRequest) (formapi.VisitorSubmissionReceipt, error) {
	return formapi.VisitorSubmissionReceipt{}, contracts.ErrPublicUnavailable
}
func (routerPublicFormsUnavailable) CancelVisitorSession(context.Context, formapi.CancelVisitorSessionRequest) error {
	return contracts.ErrPublicUnavailable
}
