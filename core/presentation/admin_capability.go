package presentation

import (
	"context"
	"time"

	"github.com/Zubrium/cdn/core/domain/access"
)

// IdentityAdminCapability contains only account, credential, and direct group
// assignment operations. It is a privilege interface, not a user kind.
type IdentityAdminCapability interface {
	Authenticate(context.Context, string) (Session, error)
	CreatePrincipal(context.Context, Session, string, *time.Time) (string, string, error)
	CreatePrincipalWithAccess(context.Context, Session, string, *time.Time, bool, map[string]access.Level) (string, string, error)
	ListPrincipals(context.Context, Session) ([]Principal, error)
	SetPrincipalManagement(context.Context, Session, string, bool) error
	DeactivatePrincipal(context.Context, Session, string) error
	IssueCredential(context.Context, Session, string, *time.Time) (string, string, error)
	ListCredentials(context.Context, Session, string) ([]Credential, error)
	RevokeCredential(context.Context, Session, string) error
	SetGroupAccess(context.Context, Session, string, string, access.Level) error
	GroupAccess(context.Context, Session, string, string) (access.Level, error)
	ListAccessGroups(context.Context, Session) ([]Group, error)
}

type identityAdminCapability struct{ runtime *Runtime }

func (r *Runtime) IdentityAdmin() IdentityAdminCapability {
	return &identityAdminCapability{runtime: r}
}

func (c *identityAdminCapability) runtimeOrError() (*Runtime, error) {
	if c == nil || c.runtime == nil {
		return nil, ErrUnavailable
	}
	if err := c.runtime.valid(); err != nil {
		return nil, err
	}
	return c.runtime, nil
}

func (c *identityAdminCapability) Authenticate(ctx context.Context, credential string) (Session, error) {
	r, err := c.runtimeOrError()
	if err != nil {
		return Session{}, err
	}
	return r.Authenticate(ctx, credential)
}

func (c *identityAdminCapability) CreatePrincipal(ctx context.Context, session Session, name string, expires *time.Time) (string, string, error) {
	r, err := c.runtimeOrError()
	if err != nil {
		return "", "", err
	}
	return r.CreatePrincipal(ctx, session, name, expires)
}

func (c *identityAdminCapability) CreatePrincipalWithAccess(ctx context.Context, session Session, name string, expires *time.Time, canManageAdministrators bool, groups map[string]access.Level) (string, string, error) {
	r, err := c.runtimeOrError()
	if err != nil {
		return "", "", err
	}
	return r.CreatePrincipalWithAccess(ctx, session, name, expires, canManageAdministrators, groups)
}

func (c *identityAdminCapability) ListPrincipals(ctx context.Context, session Session) ([]Principal, error) {
	r, err := c.runtimeOrError()
	if err != nil {
		return nil, err
	}
	return r.ListPrincipals(ctx, session)
}

func (c *identityAdminCapability) SetPrincipalManagement(ctx context.Context, session Session, principalID string, enabled bool) error {
	r, err := c.runtimeOrError()
	if err != nil {
		return err
	}
	return r.SetPrincipalManagement(ctx, session, principalID, enabled)
}

func (c *identityAdminCapability) DeactivatePrincipal(ctx context.Context, session Session, principalID string) error {
	r, err := c.runtimeOrError()
	if err != nil {
		return err
	}
	return r.DeactivatePrincipal(ctx, session, principalID)
}

func (c *identityAdminCapability) IssueCredential(ctx context.Context, session Session, principalID string, expires *time.Time) (string, string, error) {
	r, err := c.runtimeOrError()
	if err != nil {
		return "", "", err
	}
	return r.IssueCredential(ctx, session, principalID, expires)
}

func (c *identityAdminCapability) ListCredentials(ctx context.Context, session Session, principalID string) ([]Credential, error) {
	r, err := c.runtimeOrError()
	if err != nil {
		return nil, err
	}
	return r.ListCredentials(ctx, session, principalID)
}

func (c *identityAdminCapability) RevokeCredential(ctx context.Context, session Session, credentialID string) error {
	r, err := c.runtimeOrError()
	if err != nil {
		return err
	}
	return r.RevokeCredential(ctx, session, credentialID)
}

func (c *identityAdminCapability) SetGroupAccess(ctx context.Context, session Session, principalID, group string, level access.Level) error {
	r, err := c.runtimeOrError()
	if err != nil {
		return err
	}
	return r.SetGroupAccess(ctx, session, principalID, group, level)
}

func (c *identityAdminCapability) GroupAccess(ctx context.Context, session Session, principalID, group string) (access.Level, error) {
	r, err := c.runtimeOrError()
	if err != nil {
		return access.LevelNone, err
	}
	return r.GroupAccess(ctx, session, principalID, group)
}

func (c *identityAdminCapability) ListAccessGroups(ctx context.Context, session Session) ([]Group, error) {
	r, err := c.runtimeOrError()
	if err != nil {
		return nil, err
	}
	return r.ListAccessGroups(ctx, session)
}

var _ IdentityAdminCapability = (*identityAdminCapability)(nil)
