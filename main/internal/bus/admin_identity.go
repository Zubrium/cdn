package bus

import (
	"context"
	"time"

	"github.com/Zubrium/cdn/pkg/contracts"
)

func AdminIdentityOperationKeys() []contracts.Key {
	return []contracts.Key{
		contracts.AdminCreatePrincipalOperation().Key,
		contracts.AdminListPrincipalsOperation().Key,
		contracts.AdminSetPrincipalManagementOperation().Key,
		contracts.AdminDeactivatePrincipalOperation().Key,
		contracts.AdminIssueCredentialOperation().Key,
		contracts.AdminListCredentialsOperation().Key,
		contracts.AdminRevokeCredentialOperation().Key,
		contracts.AdminSetGroupAccessOperation().Key,
		contracts.AdminGroupAccessOperation().Key,
		contracts.AdminListAccessGroupsOperation().Key,
		contracts.SessionCreateOperation().Key,
		contracts.SessionRevokeOperation().Key,
	}
}

// AdminIdentityOperations is a Core-facing port. Only the composition root
// supplies it; consumers receive neutral DTOs over the DataBus and never see a
// Core session or repository.
type AdminIdentityOperations interface {
	CreatePrincipal(context.Context, contracts.AuthorizationSubject, contracts.CreatePrincipalInput) (contracts.AdminCreatePrincipalResponse, error)
	ListPrincipals(context.Context, contracts.AuthorizationSubject) ([]contracts.Principal, error)
	SetPrincipalManagement(context.Context, contracts.AuthorizationSubject, string, bool) error
	DeactivatePrincipal(context.Context, contracts.AuthorizationSubject, string) error
	IssueCredential(context.Context, contracts.AuthorizationSubject, string, *time.Time) (contracts.AdminIssueCredentialResponse, error)
	ListCredentials(context.Context, contracts.AuthorizationSubject, string) ([]contracts.Credential, error)
	RevokeCredential(context.Context, contracts.AuthorizationSubject, string) error
	SetGroupAccess(context.Context, contracts.AuthorizationSubject, string, string, contracts.AccessLevel) error
	GroupAccess(context.Context, contracts.AuthorizationSubject, string, string) (contracts.AccessLevel, error)
	ListAccessGroups(context.Context, contracts.AuthorizationSubject) ([]contracts.AccessGroup, error)
	CreateSession(context.Context, string) (contracts.CreatedSession, error)
	RevokeSession(context.Context, string) error
}

func RegisterAdminIdentity(dispatcher *Dispatcher, operations AdminIdentityOperations) error {
	if dispatcher == nil || operations == nil {
		return contracts.ErrInvalidOperation
	}
	module := func(ctx context.Context) error {
		switch ModuleIDFromContext(ctx) {
		case "admin.identity", "admin.api":
			return nil
		default:
			return ErrInvalidModuleIdentity
		}
	}
	if err := Register(dispatcher, contracts.AdminCreatePrincipalOperation(), func(ctx context.Context, request contracts.AdminCreatePrincipalRequest) (contracts.AdminCreatePrincipalResponse, error) {
		if err := module(ctx); err != nil {
			return contracts.AdminCreatePrincipalResponse{}, err
		}
		return operations.CreatePrincipal(ctx, request.Subject, request.Input)
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.AdminListPrincipalsOperation(), func(ctx context.Context, request contracts.AdminSubjectRequest) (contracts.AdminPrincipalListResponse, error) {
		if err := module(ctx); err != nil {
			return contracts.AdminPrincipalListResponse{}, err
		}
		principals, err := operations.ListPrincipals(ctx, request.Subject)
		return contracts.AdminPrincipalListResponse{Principals: principals}, err
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.AdminSetPrincipalManagementOperation(), func(ctx context.Context, request contracts.AdminPrincipalManagementRequest) (struct{}, error) {
		if err := module(ctx); err != nil {
			return struct{}{}, err
		}
		return struct{}{}, operations.SetPrincipalManagement(ctx, request.Subject, request.ID, request.Enabled)
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.AdminDeactivatePrincipalOperation(), func(ctx context.Context, request contracts.AdminPrincipalRequest) (struct{}, error) {
		if err := module(ctx); err != nil {
			return struct{}{}, err
		}
		return struct{}{}, operations.DeactivatePrincipal(ctx, request.Subject, request.PrincipalID)
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.AdminIssueCredentialOperation(), func(ctx context.Context, request contracts.AdminIssueCredentialRequest) (contracts.AdminIssueCredentialResponse, error) {
		if err := module(ctx); err != nil {
			return contracts.AdminIssueCredentialResponse{}, err
		}
		return operations.IssueCredential(ctx, request.Subject, request.PrincipalID, request.ExpiresAt)
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.AdminListCredentialsOperation(), func(ctx context.Context, request contracts.AdminCredentialListRequest) (contracts.AdminCredentialListResponse, error) {
		if err := module(ctx); err != nil {
			return contracts.AdminCredentialListResponse{}, err
		}
		credentials, err := operations.ListCredentials(ctx, request.Subject, request.PrincipalID)
		return contracts.AdminCredentialListResponse{Credentials: credentials}, err
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.AdminRevokeCredentialOperation(), func(ctx context.Context, request contracts.AdminRevokeCredentialRequest) (struct{}, error) {
		if err := module(ctx); err != nil {
			return struct{}{}, err
		}
		return struct{}{}, operations.RevokeCredential(ctx, request.Subject, request.CredentialID)
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.AdminSetGroupAccessOperation(), func(ctx context.Context, request contracts.AdminSetGroupAccessRequest) (struct{}, error) {
		if err := module(ctx); err != nil {
			return struct{}{}, err
		}
		return struct{}{}, operations.SetGroupAccess(ctx, request.Subject, request.PrincipalID, request.Group, request.Level)
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.AdminGroupAccessOperation(), func(ctx context.Context, request contracts.AdminGroupAccessRequest) (contracts.AdminGroupAccessResponse, error) {
		if err := module(ctx); err != nil {
			return contracts.AdminGroupAccessResponse{}, err
		}
		level, err := operations.GroupAccess(ctx, request.Subject, request.PrincipalID, request.Group)
		return contracts.AdminGroupAccessResponse{Level: level}, err
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.AdminListAccessGroupsOperation(), func(ctx context.Context, request contracts.AdminSubjectRequest) (contracts.AdminAccessGroupListResponse, error) {
		if err := module(ctx); err != nil {
			return contracts.AdminAccessGroupListResponse{}, err
		}
		groups, err := operations.ListAccessGroups(ctx, request.Subject)
		return contracts.AdminAccessGroupListResponse{Groups: groups}, err
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.SessionCreateOperation(), func(ctx context.Context, request contracts.SessionCreateRequest) (contracts.SessionCreateResponse, error) {
		if err := module(ctx); err != nil {
			return contracts.SessionCreateResponse{}, err
		}
		session, err := operations.CreateSession(ctx, request.Credential)
		return contracts.SessionCreateResponse{Session: session}, err
	}); err != nil {
		return err
	}
	return Register(dispatcher, contracts.SessionRevokeOperation(), func(ctx context.Context, request contracts.SessionRevokeRequest) (contracts.SessionRevokeResponse, error) {
		if err := module(ctx); err != nil {
			return contracts.SessionRevokeResponse{}, err
		}
		return contracts.SessionRevokeResponse{}, operations.RevokeSession(ctx, request.Token)
	})
}
