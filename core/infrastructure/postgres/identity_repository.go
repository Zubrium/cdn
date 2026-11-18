package postgres

import (
	"context"
	"time"

	"github.com/Zubrium/cdn/core/domain/access"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// IdentityRepository is the PostgreSQL adapter for the domain identity port.
// Transactional authorization checks remain in the SQL operations so a
// permission change cannot race an account or credential mutation.
type IdentityRepository struct{ Pool *pgxpool.Pool }

func (r IdentityRepository) Bootstrap(ctx context.Context, configuredSecret string, groups map[string]access.Level) (string, bool, error) {
	return BootstrapWithGroups(ctx, r.Pool, configuredSecret, groups)
}

func (r IdentityRepository) Authenticate(ctx context.Context, secret string) (access.Principal, error) {
	return Authenticate(ctx, r.Pool, secret)
}

func (r IdentityRepository) AuthenticateHash(ctx context.Context, hash []byte) (access.Principal, error) {
	return AuthenticateHash(ctx, r.Pool, hash)
}

func (r IdentityRepository) CreateSession(ctx context.Context, credentialHash, sessionHash []byte, now, idleExpiresAt, absoluteUntil time.Time) (access.CredentialSession, error) {
	return CreateSession(ctx, r.Pool, credentialHash, sessionHash, now, idleExpiresAt, absoluteUntil)
}

func (r IdentityRepository) AuthenticateSession(ctx context.Context, sessionHash []byte, now time.Time, idleTTL time.Duration) (access.CredentialSession, error) {
	return AuthenticateSession(ctx, r.Pool, sessionHash, now, idleTTL)
}

func (r IdentityRepository) RevokeSession(ctx context.Context, sessionHash []byte, now time.Time) error {
	return RevokeSession(ctx, r.Pool, sessionHash, now)
}

func (r IdentityRepository) CreatePrincipal(ctx context.Context, actor access.Principal, name string, expires *time.Time) (string, string, error) {
	id, secret, err := CreatePrincipal(ctx, r.Pool, actor, name, expires)
	if err != nil {
		return "", "", err
	}
	return id.String(), secret, nil
}

func (r IdentityRepository) CreatePrincipalWithAccess(ctx context.Context, actor access.Principal, name string, expires *time.Time, manage bool, grants map[string]access.Level) (string, string, error) {
	id, secret, err := CreatePrincipalWithAccess(ctx, r.Pool, actor, name, expires, manage, grants)
	if err != nil {
		return "", "", err
	}
	return id.String(), secret, nil
}

func (r IdentityRepository) ListPrincipals(ctx context.Context, actor access.Principal) ([]access.Principal, error) {
	rows, err := ListPrincipals(ctx, r.Pool, actor)
	if err != nil {
		return nil, err
	}
	principals := make([]access.Principal, 0, len(rows))
	for _, row := range rows {
		principals = append(principals, access.Principal{ID: row.ID.String(), Name: row.Name, Active: row.Active, CanManageAdministrators: row.CanManageAdministrators})
	}
	return principals, nil
}

func (r IdentityRepository) SetPrincipalManagement(ctx context.Context, actor access.Principal, principalID string, enabled bool) error {
	id, err := parseIdentityID(principalID)
	if err != nil {
		return err
	}
	return SetPrincipalManagement(ctx, r.Pool, actor, id, enabled)
}

func (r IdentityRepository) DeactivatePrincipal(ctx context.Context, actor access.Principal, principalID string) error {
	id, err := parseIdentityID(principalID)
	if err != nil {
		return err
	}
	return DeactivatePrincipal(ctx, r.Pool, actor, id)
}

func (r IdentityRepository) IssueCredential(ctx context.Context, actor access.Principal, principalID string, expires *time.Time) (string, string, error) {
	id, err := parseIdentityID(principalID)
	if err != nil {
		return "", "", err
	}
	credentialID, secret, err := IssueCredential(ctx, r.Pool, actor, id, expires)
	if err != nil {
		return "", "", err
	}
	return credentialID.String(), secret, nil
}

func (r IdentityRepository) ListCredentials(ctx context.Context, actor access.Principal, principalID string) ([]access.Credential, error) {
	id, err := parseIdentityID(principalID)
	if err != nil {
		return nil, err
	}
	rows, err := ListCredentials(ctx, r.Pool, actor, id)
	if err != nil {
		return nil, err
	}
	credentials := make([]access.Credential, 0, len(rows))
	for _, row := range rows {
		credentials = append(credentials, access.Credential{ID: row.ID.String(), PrincipalID: row.PrincipalID.String(), CreatedAt: row.CreatedAt, ExpiresAt: row.ExpiresAt, RevokedAt: row.RevokedAt})
	}
	return credentials, nil
}

func (r IdentityRepository) RevokeCredential(ctx context.Context, actor access.Principal, credentialID string) error {
	id, err := parseIdentityID(credentialID)
	if err != nil {
		return err
	}
	return RevokeCredential(ctx, r.Pool, actor, id)
}

func (r IdentityRepository) SetGroupAccess(ctx context.Context, actor access.Principal, principalID, group string, level access.Level) error {
	actorID, err := parseIdentityID(actor.ID)
	if err != nil {
		return access.ErrUnauthorized
	}
	targetID, err := parseIdentityID(principalID)
	if err != nil {
		return err
	}
	return SetGroupAccess(ctx, r.Pool, actorID, targetID, group, level)
}

func (r IdentityRepository) GroupAccess(ctx context.Context, actor access.Principal, principalID, group string) (access.Level, error) {
	actorID, err := parseIdentityID(actor.ID)
	if err != nil {
		return access.LevelNone, access.ErrUnauthorized
	}
	targetID, err := parseIdentityID(principalID)
	if err != nil {
		return access.LevelNone, err
	}
	return GroupAccess(ctx, r.Pool, actorID, targetID, group)
}

func (r IdentityRepository) ListAccessGroups(ctx context.Context, principalID string) ([]access.ConfiguredGroup, error) {
	rows, err := ListAccessGroups(ctx, r.Pool, principalID)
	if err != nil {
		return nil, err
	}
	groups := make([]access.ConfiguredGroup, 0, len(rows))
	for _, row := range rows {
		groups = append(groups, access.ConfiguredGroup{Key: row.Key, Active: row.Active, Level: row.Level})
	}
	return groups, nil
}

func (r IdentityRepository) Assignments(ctx context.Context, principalID string) ([]access.GroupAssignment, error) {
	return Assignments(ctx, r.Pool, principalID)
}

func (r IdentityRepository) RecoverBootstrapCredential(ctx context.Context) (string, error) {
	_, secret, err := RecoverBootstrapCredential(ctx, r.Pool)
	return secret, err
}

func parseIdentityID(value string) (uuid.UUID, error) {
	id, err := uuid.Parse(value)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, access.ErrInvalid
	}
	return id, nil
}

var _ access.IdentityRepository = IdentityRepository{}
var _ access.SessionRepository = IdentityRepository{}
