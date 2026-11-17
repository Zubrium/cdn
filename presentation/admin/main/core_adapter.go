package adminapi

import (
	"context"
	"errors"
	"time"

	"github.com/Zubrium/cdn/core/access"
	"github.com/Zubrium/cdn/core/platform"
)

// CoreAdapter maps the Core identity capability onto the HTTP-facing API and
// keeps account creation plus its explicit initial group grants atomic.
type CoreAdapter struct{ core platform.IdentityAdminCapability }

func NewCoreAdapter(core platform.IdentityAdminCapability) (*CoreAdapter, error) {
	if core == nil { return nil, errors.New("Core identity administration capability is required") }
	return &CoreAdapter{core: core}, nil
}

func (a *CoreAdapter) Authenticate(ctx context.Context, credential string) (platform.Session, error) {
	return a.core.Authenticate(ctx, credential)
}

func (a *CoreAdapter) CreatePrincipal(ctx context.Context, session platform.Session, input CreatePrincipalRequest) (PrincipalCreated, error) {
	id, credential, err := a.core.CreatePrincipalWithAccess(ctx, session, input.Name, input.ExpiresAt, input.CanManageAdministrators, input.GroupAccess)
	if err != nil { return PrincipalCreated{}, err }
	return PrincipalCreated{ID: id, Name: input.Name, Credential: credential}, nil
}

func (a *CoreAdapter) ListPrincipals(ctx context.Context, session platform.Session) ([]platform.Principal, error) {
	return a.core.ListPrincipals(ctx, session)
}

func (a *CoreAdapter) SetPrincipalManagement(ctx context.Context, session platform.Session, id string, enabled bool) error {
	return a.core.SetPrincipalManagement(ctx, session, id, enabled)
}

func (a *CoreAdapter) DeactivatePrincipal(ctx context.Context, session platform.Session, id string) error {
	return a.core.DeactivatePrincipal(ctx, session, id)
}

func (a *CoreAdapter) IssueCredential(ctx context.Context, session platform.Session, principalID string, expires *time.Time) (string, string, error) {
	return a.core.IssueCredential(ctx, session, principalID, expires)
}

func (a *CoreAdapter) ListCredentials(ctx context.Context, session platform.Session, principalID string) ([]platform.Credential, error) {
	return a.core.ListCredentials(ctx, session, principalID)
}

func (a *CoreAdapter) RevokeCredential(ctx context.Context, session platform.Session, credentialID string) error {
	return a.core.RevokeCredential(ctx, session, credentialID)
}

func (a *CoreAdapter) SetGroupAccess(ctx context.Context, session platform.Session, principalID, group string, level access.Level) error {
	return a.core.SetGroupAccess(ctx, session, principalID, group, level)
}

func (a *CoreAdapter) GroupAccess(ctx context.Context, session platform.Session, principalID, group string) (access.Level, error) {
	return a.core.GroupAccess(ctx, session, principalID, group)
}

func (a *CoreAdapter) ListAccessGroups(ctx context.Context, session platform.Session) ([]platform.Group, error) {
	return a.core.ListAccessGroups(ctx, session)
}

var _ IdentityAdminAPI = (*CoreAdapter)(nil)
