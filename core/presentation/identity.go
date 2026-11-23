package presentation

import (
	"context"
	"time"

	"github.com/Zubrium/cdn/core/domain/access"
)

var ErrForbidden = access.ErrForbidden
var ErrUnauthorized = access.ErrUnauthorized
var ErrManagerInvariant = access.ErrManagerInvariant

type Principal = access.Principal
type Credential = access.Credential
type Group = access.ConfiguredGroup

func (r *Runtime) requireAdmin(ctx context.Context, session Session) (access.Principal, error) {
	actor, err := r.actor(ctx, session)
	if err != nil {
		return access.Principal{}, err
	}
	if r.identities == nil {
		return access.Principal{}, ErrUnavailable
	}
	if err := r.identities.RequireAdministratorManagement(ctx, actor); err != nil {
		return access.Principal{}, err
	}
	return actor, nil
}

// CurrentPrincipal resolves the authenticated session to its current identity
// so owner-scoped capabilities can bind durable state without trusting a caller ID.
func (r *Runtime) CurrentPrincipal(ctx context.Context, session Session) (Principal, error) {
	return r.actor(ctx, session)
}

func (r *Runtime) CreatePrincipal(ctx context.Context, session Session, name string, expires *time.Time) (string, string, error) {
	actor, err := r.requireAdmin(ctx, session)
	if err != nil {
		return "", "", err
	}
	return r.identities.CreatePrincipal(ctx, actor, name, expires)
}

// CreatePrincipalWithAccess atomically creates one principal and its explicitly
// selected initial group levels. The application service verifies the caller
// before the infrastructure adapter repeats the check within its transaction.
func (r *Runtime) CreatePrincipalWithAccess(ctx context.Context, session Session, name string, expires *time.Time, canManageAdministrators bool, groups map[string]access.Level) (string, string, error) {
	actor, err := r.actor(ctx, session)
	if err != nil {
		return "", "", err
	}
	if r.identities == nil {
		return "", "", ErrUnavailable
	}
	return r.identities.CreatePrincipalWithAccess(ctx, actor, name, expires, canManageAdministrators, groups)
}

func (r *Runtime) ListPrincipals(ctx context.Context, session Session) ([]Principal, error) {
	actor, err := r.requireAdmin(ctx, session)
	if err != nil {
		return nil, err
	}
	return r.identities.ListPrincipals(ctx, actor)
}

func (r *Runtime) SetPrincipalManagement(ctx context.Context, session Session, value string, enabled bool) error {
	actor, err := r.requireAdmin(ctx, session)
	if err != nil {
		return err
	}
	return r.identities.SetPrincipalManagement(ctx, actor, value, enabled)
}

func (r *Runtime) DeactivatePrincipal(ctx context.Context, session Session, value string) error {
	actor, err := r.requireAdmin(ctx, session)
	if err != nil {
		return err
	}
	return r.identities.DeactivatePrincipal(ctx, actor, value)
}

func (r *Runtime) IssueCredential(ctx context.Context, session Session, value string, expires *time.Time) (string, string, error) {
	actor, err := r.requireAdmin(ctx, session)
	if err != nil {
		return "", "", err
	}
	return r.identities.IssueCredential(ctx, actor, value, expires)
}

func (r *Runtime) ListCredentials(ctx context.Context, session Session, value string) ([]Credential, error) {
	actor, err := r.requireAdmin(ctx, session)
	if err != nil {
		return nil, err
	}
	return r.identities.ListCredentials(ctx, actor, value)
}

func (r *Runtime) RevokeCredential(ctx context.Context, session Session, value string) error {
	actor, err := r.requireAdmin(ctx, session)
	if err != nil {
		return err
	}
	return r.identities.RevokeCredential(ctx, actor, value)
}

func (r *Runtime) SetGroupAccess(ctx context.Context, session Session, target, group string, level access.Level) error {
	actor, err := r.requireAdmin(ctx, session)
	if err != nil {
		return err
	}
	return r.identities.SetGroupAccess(ctx, actor, target, group, level)
}

func (r *Runtime) GroupAccess(ctx context.Context, session Session, target, group string) (access.Level, error) {
	actor, err := r.requireAdmin(ctx, session)
	if err != nil {
		return access.LevelNone, err
	}
	return r.identities.GroupAccess(ctx, actor, target, group)
}

func (r *Runtime) ListAccessGroups(ctx context.Context, session Session) ([]Group, error) {
	actor, err := r.requireAdmin(ctx, session)
	if err != nil {
		return nil, err
	}
	return r.identities.ListAccessGroups(ctx, actor)
}
