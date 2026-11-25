package contracts

import (
	"context"
	"errors"
	"time"
)

// Neutral identity-administration DTOs shared by the Admin HTTP adapter and
// the composition root. They carry no Core runtime or repository types.

// ErrAccessManagerInvariant reports that an administrator-management change
// would violate the last-active-manager invariant.
var ErrAccessManagerInvariant = errors.New("administrator management invariant would be violated")

// CreatedSession is the one-time result of exchanging a credential for a
// revocable session bound to that exact credential.
type CreatedSession struct {
	Token             string    `json:"token"`
	Principal         Principal `json:"principal"`
	IdleExpiresAt     time.Time `json:"idle_expires_at"`
	AbsoluteExpiresAt time.Time `json:"absolute_expires_at"`
}

// Credential describes one issued credential without its secret value.
type Credential struct {
	ID          string     `json:"id"`
	PrincipalID string     `json:"principal_id"`
	CreatedAt   time.Time  `json:"created_at"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty"`
}

// AccessGroup is one configured static access group and the requesting
// principal's current direct level in it.
type AccessGroup struct {
	Key    string      `json:"key"`
	Active bool        `json:"active"`
	Level  AccessLevel `json:"level"`
}

// CreatePrincipalInput is the neutral administrator-create payload. Group
// keys are validated against the module's DataBus allowlist before dispatch.
type CreatePrincipalInput struct {
	Name                    string                 `json:"name"`
	ExpiresAt               *time.Time             `json:"expires_at,omitempty"`
	CanManageAdministrators bool                   `json:"can_manage_administrators"`
	GroupAccess             map[string]AccessLevel `json:"group_access,omitempty"`
}

type AdminCreatePrincipalRequest struct {
	Subject AuthorizationSubject `json:"subject"`
	Input   CreatePrincipalInput `json:"input"`
}

type AdminCreatePrincipalResponse struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Credential string `json:"credential"`
}

type AdminSubjectRequest struct {
	Subject AuthorizationSubject `json:"subject"`
}

type AdminPrincipalRequest struct {
	Subject     AuthorizationSubject `json:"subject"`
	PrincipalID string               `json:"principal_id"`
}

type AdminPrincipalManagementRequest struct {
	Subject AuthorizationSubject `json:"subject"`
	ID      string               `json:"id"`
	Enabled bool                 `json:"enabled"`
}

type AdminSetGroupAccessRequest struct {
	Subject     AuthorizationSubject `json:"subject"`
	PrincipalID string               `json:"principal_id"`
	Group       string               `json:"group"`
	Level       AccessLevel          `json:"level"`
}

type AdminGroupAccessRequest struct {
	Subject     AuthorizationSubject `json:"subject"`
	PrincipalID string               `json:"principal_id"`
	Group       string               `json:"group"`
}

type AdminGroupAccessResponse struct {
	Level AccessLevel `json:"level"`
}

type AdminPrincipalListResponse struct {
	Principals []Principal `json:"principals"`
}

type AdminIssueCredentialRequest struct {
	Subject     AuthorizationSubject `json:"subject"`
	PrincipalID string               `json:"principal_id"`
	ExpiresAt   *time.Time           `json:"expires_at,omitempty"`
}

type AdminIssueCredentialResponse struct {
	ID         string `json:"id"`
	Credential string `json:"credential"`
}

type AdminCredentialListRequest struct {
	Subject     AuthorizationSubject `json:"subject"`
	PrincipalID string               `json:"principal_id"`
}

type AdminCredentialListResponse struct {
	Credentials []Credential `json:"credentials"`
}

type AdminRevokeCredentialRequest struct {
	Subject      AuthorizationSubject `json:"subject"`
	CredentialID string               `json:"credential_id"`
}

type AdminAccessGroupListResponse struct {
	Groups []AccessGroup `json:"groups"`
}

type SessionCreateRequest struct {
	Credential string `json:"credential"`
}

type SessionCreateResponse struct {
	Session CreatedSession `json:"session"`
}

type SessionRevokeRequest struct {
	Token string `json:"token"`
}

type SessionRevokeResponse struct{}

func validAdminSubject(subject AuthorizationSubject) error {
	if subject.Validate() != nil {
		return ErrInvalidOperation
	}
	return nil
}

func AdminCreatePrincipalOperation() Operation[AdminCreatePrincipalRequest, AdminCreatePrincipalResponse] {
	return Operation[AdminCreatePrincipalRequest, AdminCreatePrincipalResponse]{
		Key: Key{ID: "core.admin.principal.create", Version: 1},
		Validate: func(request AdminCreatePrincipalRequest) error {
			if validAdminSubject(request.Subject) != nil || request.Input.Name == "" || len(request.Input.GroupAccess) == 0 {
				return ErrInvalidOperation
			}
			for group, level := range request.Input.GroupAccess {
				if !accessGroupKeyPattern.MatchString(group) || level == AccessLevelDenied || level > AccessLevelWrite {
					return ErrInvalidOperation
				}
			}
			return nil
		},
	}
}

func AdminListPrincipalsOperation() Operation[AdminSubjectRequest, AdminPrincipalListResponse] {
	return Operation[AdminSubjectRequest, AdminPrincipalListResponse]{
		Key: Key{ID: "core.admin.principal.list", Version: 1},
		Validate: func(request AdminSubjectRequest) error {
			return validAdminSubject(request.Subject)
		},
	}
}

func AdminSetPrincipalManagementOperation() Operation[AdminPrincipalManagementRequest, struct{}] {
	return Operation[AdminPrincipalManagementRequest, struct{}]{
		Key: Key{ID: "core.admin.principal.set-management", Version: 1},
		Validate: func(request AdminPrincipalManagementRequest) error {
			if validAdminSubject(request.Subject) != nil || request.ID == "" {
				return ErrInvalidOperation
			}
			return nil
		},
	}
}

func AdminDeactivatePrincipalOperation() Operation[AdminPrincipalRequest, struct{}] {
	return Operation[AdminPrincipalRequest, struct{}]{
		Key: Key{ID: "core.admin.principal.deactivate", Version: 1},
		Validate: func(request AdminPrincipalRequest) error {
			if validAdminSubject(request.Subject) != nil || request.PrincipalID == "" {
				return ErrInvalidOperation
			}
			return nil
		},
	}
}

func AdminIssueCredentialOperation() Operation[AdminIssueCredentialRequest, AdminIssueCredentialResponse] {
	return Operation[AdminIssueCredentialRequest, AdminIssueCredentialResponse]{
		Key: Key{ID: "core.admin.credential.issue", Version: 1},
		Validate: func(request AdminIssueCredentialRequest) error {
			if validAdminSubject(request.Subject) != nil || request.PrincipalID == "" {
				return ErrInvalidOperation
			}
			return nil
		},
	}
}

func AdminListCredentialsOperation() Operation[AdminCredentialListRequest, AdminCredentialListResponse] {
	return Operation[AdminCredentialListRequest, AdminCredentialListResponse]{
		Key: Key{ID: "core.admin.credential.list", Version: 1},
		Validate: func(request AdminCredentialListRequest) error {
			if validAdminSubject(request.Subject) != nil || request.PrincipalID == "" {
				return ErrInvalidOperation
			}
			return nil
		},
	}
}

func AdminRevokeCredentialOperation() Operation[AdminRevokeCredentialRequest, struct{}] {
	return Operation[AdminRevokeCredentialRequest, struct{}]{
		Key: Key{ID: "core.admin.credential.revoke", Version: 1},
		Validate: func(request AdminRevokeCredentialRequest) error {
			if validAdminSubject(request.Subject) != nil || request.CredentialID == "" {
				return ErrInvalidOperation
			}
			return nil
		},
	}
}

func AdminSetGroupAccessOperation() Operation[AdminSetGroupAccessRequest, struct{}] {
	return Operation[AdminSetGroupAccessRequest, struct{}]{
		Key: Key{ID: "core.admin.group.set", Version: 1},
		Validate: func(request AdminSetGroupAccessRequest) error {
			if validAdminSubject(request.Subject) != nil || request.PrincipalID == "" ||
				!accessGroupKeyPattern.MatchString(request.Group) || request.Level == AccessLevelDenied || request.Level > AccessLevelWrite {
				return ErrInvalidOperation
			}
			return nil
		},
	}
}

func AdminGroupAccessOperation() Operation[AdminGroupAccessRequest, AdminGroupAccessResponse] {
	return Operation[AdminGroupAccessRequest, AdminGroupAccessResponse]{
		Key: Key{ID: "core.admin.group.get", Version: 1},
		Validate: func(request AdminGroupAccessRequest) error {
			if validAdminSubject(request.Subject) != nil || request.PrincipalID == "" || !accessGroupKeyPattern.MatchString(request.Group) {
				return ErrInvalidOperation
			}
			return nil
		},
	}
}

func AdminListAccessGroupsOperation() Operation[AdminSubjectRequest, AdminAccessGroupListResponse] {
	return Operation[AdminSubjectRequest, AdminAccessGroupListResponse]{
		Key: Key{ID: "core.admin.group.list", Version: 1},
		Validate: func(request AdminSubjectRequest) error {
			return validAdminSubject(request.Subject)
		},
	}
}

func SessionCreateOperation() Operation[SessionCreateRequest, SessionCreateResponse] {
	return Operation[SessionCreateRequest, SessionCreateResponse]{
		Key: Key{ID: "core.session.create", Version: 1},
		Validate: func(request SessionCreateRequest) error {
			if request.Credential == "" {
				return ErrInvalidOperation
			}
			return nil
		},
	}
}

func SessionRevokeOperation() Operation[SessionRevokeRequest, SessionRevokeResponse] {
	return Operation[SessionRevokeRequest, SessionRevokeResponse]{
		Key: Key{ID: "core.session.revoke", Version: 1},
		Validate: func(request SessionRevokeRequest) error {
			if request.Token == "" {
				return ErrInvalidOperation
			}
			return nil
		},
	}
}

// AdminIdentityClient is the consumer-facing adapter. Every request carries an
// already authenticated subject; Core re-resolves it and checks group rights.
type AdminIdentityClient struct{ dispatcher Dispatcher }

func NewAdminIdentityClient(dispatcher Dispatcher) (*AdminIdentityClient, error) {
	if dispatcher == nil {
		return nil, ErrDispatcherUnavailable
	}
	return &AdminIdentityClient{dispatcher: dispatcher}, nil
}

func (client *AdminIdentityClient) CreatePrincipal(ctx context.Context, subject AuthorizationSubject, input CreatePrincipalInput) (AdminCreatePrincipalResponse, error) {
	response, err := Call(ctx, client.dispatcher, AdminCreatePrincipalOperation(), AdminCreatePrincipalRequest{Subject: subject, Input: input})
	return response, err
}

func (client *AdminIdentityClient) ListPrincipals(ctx context.Context, subject AuthorizationSubject) ([]Principal, error) {
	response, err := Call(ctx, client.dispatcher, AdminListPrincipalsOperation(), AdminSubjectRequest{Subject: subject})
	return response.Principals, err
}

func (client *AdminIdentityClient) SetPrincipalManagement(ctx context.Context, subject AuthorizationSubject, id string, enabled bool) error {
	_, err := Call(ctx, client.dispatcher, AdminSetPrincipalManagementOperation(), AdminPrincipalManagementRequest{Subject: subject, ID: id, Enabled: enabled})
	return err
}

func (client *AdminIdentityClient) DeactivatePrincipal(ctx context.Context, subject AuthorizationSubject, id string) error {
	_, err := Call(ctx, client.dispatcher, AdminDeactivatePrincipalOperation(), AdminPrincipalRequest{Subject: subject, PrincipalID: id})
	return err
}

func (client *AdminIdentityClient) IssueCredential(ctx context.Context, subject AuthorizationSubject, principalID string, expires *time.Time) (AdminIssueCredentialResponse, error) {
	response, err := Call(ctx, client.dispatcher, AdminIssueCredentialOperation(), AdminIssueCredentialRequest{Subject: subject, PrincipalID: principalID, ExpiresAt: expires})
	return response, err
}

func (client *AdminIdentityClient) ListCredentials(ctx context.Context, subject AuthorizationSubject, principalID string) ([]Credential, error) {
	response, err := Call(ctx, client.dispatcher, AdminListCredentialsOperation(), AdminCredentialListRequest{Subject: subject, PrincipalID: principalID})
	return response.Credentials, err
}

func (client *AdminIdentityClient) RevokeCredential(ctx context.Context, subject AuthorizationSubject, credentialID string) error {
	_, err := Call(ctx, client.dispatcher, AdminRevokeCredentialOperation(), AdminRevokeCredentialRequest{Subject: subject, CredentialID: credentialID})
	return err
}

func (client *AdminIdentityClient) SetGroupAccess(ctx context.Context, subject AuthorizationSubject, principalID, group string, level AccessLevel) error {
	_, err := Call(ctx, client.dispatcher, AdminSetGroupAccessOperation(), AdminSetGroupAccessRequest{Subject: subject, PrincipalID: principalID, Group: group, Level: level})
	return err
}

func (client *AdminIdentityClient) GroupAccess(ctx context.Context, subject AuthorizationSubject, principalID, group string) (AccessLevel, error) {
	response, err := Call(ctx, client.dispatcher, AdminGroupAccessOperation(), AdminGroupAccessRequest{Subject: subject, PrincipalID: principalID, Group: group})
	return response.Level, err
}

func (client *AdminIdentityClient) ListAccessGroups(ctx context.Context, subject AuthorizationSubject) ([]AccessGroup, error) {
	response, err := Call(ctx, client.dispatcher, AdminListAccessGroupsOperation(), AdminSubjectRequest{Subject: subject})
	return response.Groups, err
}

func (client *AdminIdentityClient) CreateSession(ctx context.Context, credential string) (CreatedSession, error) {
	response, err := Call(ctx, client.dispatcher, SessionCreateOperation(), SessionCreateRequest{Credential: credential})
	return response.Session, err
}

func (client *AdminIdentityClient) RevokeSession(ctx context.Context, token string) error {
	_, err := Call(ctx, client.dispatcher, SessionRevokeOperation(), SessionRevokeRequest{Token: token})
	return err
}
