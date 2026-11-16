package platform

import (
	"context"
	"errors"
	"time"

	"github.com/Zubrium/cdn/core/access"
	"github.com/Zubrium/cdn/core/internal/postgres"
	"github.com/google/uuid"
)

var ErrForbidden = access.ErrForbidden
var ErrUnauthorized = access.ErrUnauthorized

type Principal struct {
	ID                      string
	Name                    string
	Active                  bool
	CanManageAdministrators bool
}

type Credential struct {
	ID, PrincipalID      string
	CreatedAt            time.Time
	ExpiresAt, RevokedAt *time.Time
}

// Group is a statically configured access group and the requesting principal's
// direct level. File sets are a separate search/indexing concept.
type Group struct {
	Key    string
	Active bool
	Level  access.Level
}

func parseID(value string) (uuid.UUID, error) {
	id, err := uuid.Parse(value)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, errors.New("invalid identifier")
	}
	return id, nil
}

func (r *Runtime) requireAdmin(ctx context.Context, session Session) (access.Principal, error) {
	actor, err := r.actor(ctx, session)
	if err != nil {
		return access.Principal{}, err
	}
	if !actor.CanManageAdministrators {
		return access.Principal{}, ErrForbidden
	}
	return actor, nil
}

func (r *Runtime) CreatePrincipal(ctx context.Context, session Session, name string, expires *time.Time) (string, string, error) {
	actor, err := r.requireAdmin(ctx, session)
	if err != nil {
		return "", "", err
	}
	id, secret, err := postgres.CreatePrincipal(ctx, r.database, actor, name, expires)
	if err != nil {
		return "", "", err
	}
	return id.String(), secret, nil
}

func (r *Runtime) ListPrincipals(ctx context.Context, session Session) ([]Principal, error) {
	actor, err := r.requireAdmin(ctx, session)
	if err != nil {
		return nil, err
	}
	items, err := postgres.ListPrincipals(ctx, r.database, actor)
	if err != nil {
		return nil, err
	}
	principals := make([]Principal, 0, len(items))
	for _, item := range items {
		principals = append(principals, Principal{ID: item.ID.String(), Name: item.Name, Active: item.Active, CanManageAdministrators: item.CanManageAdministrators})
	}
	return principals, nil
}

func (r *Runtime) SetPrincipalManagement(ctx context.Context, session Session, value string, enabled bool) error {
	actor, err := r.requireAdmin(ctx, session)
	if err != nil {
		return err
	}
	id, err := parseID(value)
	if err != nil {
		return err
	}
	return postgres.SetPrincipalManagement(ctx, r.database, actor, id, enabled)
}

func (r *Runtime) DeactivatePrincipal(ctx context.Context, session Session, value string) error {
	actor, err := r.requireAdmin(ctx, session)
	if err != nil {
		return err
	}
	id, err := parseID(value)
	if err != nil {
		return err
	}
	return postgres.DeactivatePrincipal(ctx, r.database, actor, id)
}

func (r *Runtime) IssueCredential(ctx context.Context, session Session, value string, expires *time.Time) (string, string, error) {
	actor, err := r.requireAdmin(ctx, session)
	if err != nil {
		return "", "", err
	}
	id, err := parseID(value)
	if err != nil {
		return "", "", err
	}
	credentialID, secret, err := postgres.IssueCredential(ctx, r.database, actor, id, expires)
	if err != nil {
		return "", "", err
	}
	return credentialID.String(), secret, nil
}

func (r *Runtime) ListCredentials(ctx context.Context, session Session, value string) ([]Credential, error) {
	actor, err := r.requireAdmin(ctx, session)
	if err != nil {
		return nil, err
	}
	principalID, err := parseID(value)
	if err != nil {
		return nil, err
	}
	items, err := postgres.ListCredentials(ctx, r.database, actor, principalID)
	if err != nil {
		return nil, err
	}
	credentials := make([]Credential, 0, len(items))
	for _, item := range items {
		credentials = append(credentials, Credential{ID: item.ID.String(), PrincipalID: item.PrincipalID.String(), CreatedAt: item.CreatedAt, ExpiresAt: item.ExpiresAt, RevokedAt: item.RevokedAt})
	}
	return credentials, nil
}

func (r *Runtime) RevokeCredential(ctx context.Context, session Session, value string) error {
	actor, err := r.requireAdmin(ctx, session)
	if err != nil {
		return err
	}
	id, err := parseID(value)
	if err != nil {
		return err
	}
	return postgres.RevokeCredential(ctx, r.database, actor, id)
}

func (r *Runtime) SetGroupAccess(ctx context.Context, session Session, target, group string, level access.Level) error {
	actor, err := r.requireAdmin(ctx, session)
	if err != nil {
		return err
	}
	targetID, err := parseID(target)
	if err != nil {
		return err
	}
	return postgres.SetGroupAccess(ctx, r.database, parsePrincipalID(actor.ID), targetID, group, level)
}

func (r *Runtime) GroupAccess(ctx context.Context, session Session, target, group string) (access.Level, error) {
	actor, err := r.requireAdmin(ctx, session)
	if err != nil {
		return access.LevelNone, err
	}
	targetID, err := parseID(target)
	if err != nil {
		return access.LevelNone, err
	}
	return postgres.GroupAccess(ctx, r.database, parsePrincipalID(actor.ID), targetID, group)
}

func (r *Runtime) ListAccessGroups(ctx context.Context, session Session) ([]Group, error) {
	actor, err := r.actor(ctx, session)
	if err != nil {
		return nil, err
	}
	items, err := postgres.ListAccessGroups(ctx, r.database, actor.ID)
	if err != nil {
		return nil, err
	}
	groups := make([]Group, 0, len(items))
	for _, item := range items {
		groups = append(groups, Group{Key: item.Key, Active: item.Active, Level: item.Level})
	}
	return groups, nil
}

func parsePrincipalID(value string) uuid.UUID {
	id, _ := uuid.Parse(value)
	return id
}
