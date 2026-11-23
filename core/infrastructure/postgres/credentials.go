package postgres

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	appaccess "github.com/Zubrium/cdn/core/application/access"
	"github.com/Zubrium/cdn/core/domain/access"
	"github.com/Zubrium/cdn/core/infrastructure/identity"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Credential struct {
	ID, PrincipalID      uuid.UUID
	CreatedAt            time.Time
	ExpiresAt, RevokedAt *time.Time
}

func ListCredentials(ctx context.Context, pool *pgxpool.Pool, actor access.Principal, principalID uuid.UUID) ([]Credential, error) {
	if pool == nil || principalID == uuid.Nil {
		return nil, errors.New("invalid credential list")
	}
	if !actor.Active || actor.ID == "" {
		return nil, ErrUnauthorized
	}
	actorID, err := uuid.Parse(actor.ID)
	if err != nil {
		return nil, ErrUnauthorized
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := requireAdministratorManager(ctx, tx, actorID); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT id,user_id,created_at,expires_at,revoked_at FROM access.credentials WHERE user_id=$1 ORDER BY created_at,id`, principalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Credential, 0)
	for rows.Next() {
		var item Credential
		if err := rows.Scan(&item.ID, &item.PrincipalID, &item.CreatedAt, &item.ExpiresAt, &item.RevokedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func CreatePrincipal(ctx context.Context, pool *pgxpool.Pool, actor access.Principal, name string, expiresAt *time.Time) (uuid.UUID, string, error) {
	if !actor.Active || actor.ID == "" {
		return uuid.Nil, "", ErrUnauthorized
	}
	if pool == nil || !validPrincipalName(name) || !validExpiry(expiresAt) {
		return uuid.Nil, "", errors.New("invalid principal")
	}
	actorID, err := uuid.Parse(actor.ID)
	if err != nil {
		return uuid.Nil, "", ErrUnauthorized
	}
	secret, err := identity.NewSecret()
	if err != nil {
		return uuid.Nil, "", err
	}
	principalID, err := uuid.NewRandom()
	if err != nil {
		return uuid.Nil, "", err
	}
	credentialID, err := uuid.NewRandom()
	if err != nil {
		return uuid.Nil, "", err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, "", err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := requireAdministratorManager(ctx, tx, actorID); err != nil {
		return uuid.Nil, "", err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO access.users(id,name,active,can_manage_administrators) VALUES($1,$2,true,false)`, principalID, name); err != nil {
		return uuid.Nil, "", err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO access.credentials(id,user_id,secret_hash,expires_at) VALUES($1,$2,$3,$4)`, credentialID, principalID, identity.HashSecret(secret), expiresAt); err != nil {
		return uuid.Nil, "", err
	}
	if err := bumpAccessEpoch(ctx, tx, principalID); err != nil {
		return uuid.Nil, "", err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_notify('core_cache_invalidation','*')`); err != nil {
		return uuid.Nil, "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, "", fmt.Errorf("commit principal creation: %w", err)
	}
	return principalID, secret, nil
}

// CreatePrincipalWithAccess creates one principal, its first credential, and
// its explicitly selected group levels in one transaction. A failed grant
// cannot leave behind an account with a partial permission set.
func CreatePrincipalWithAccess(ctx context.Context, pool *pgxpool.Pool, actor access.Principal, name string, expiresAt *time.Time, canManageAdministrators bool, groups map[string]access.Level) (uuid.UUID, string, error) {
	if !actor.Active || actor.ID == "" {
		return uuid.Nil, "", ErrUnauthorized
	}
	if pool == nil || !validPrincipalName(name) || !validExpiry(expiresAt) {
		return uuid.Nil, "", errors.New("invalid principal")
	}
	if err := validatePrincipalGroups(groups); err != nil {
		return uuid.Nil, "", err
	}
	actorID, err := uuid.Parse(actor.ID)
	if err != nil {
		return uuid.Nil, "", ErrUnauthorized
	}
	secret, err := identity.NewSecret()
	if err != nil {
		return uuid.Nil, "", err
	}
	principalID, err := uuid.NewRandom()
	if err != nil {
		return uuid.Nil, "", err
	}
	credentialID, err := uuid.NewRandom()
	if err != nil {
		return uuid.Nil, "", err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, "", err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := lockManagerChanges(ctx, tx); err != nil {
		return uuid.Nil, "", err
	}
	if err := requireAdministratorManager(ctx, tx, actorID); err != nil {
		return uuid.Nil, "", err
	}
	groupKeys := sortedGroupKeys(groups)
	for _, group := range groupKeys {
		if err := requireGroupAdministrator(ctx, tx, actorID, group); err != nil {
			return uuid.Nil, "", err
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO access.users(id,name,active,can_manage_administrators) VALUES($1,$2,true,$3)`, principalID, name, canManageAdministrators); err != nil {
		return uuid.Nil, "", err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO access.credentials(id,user_id,secret_hash,expires_at) VALUES($1,$2,$3,$4)`, credentialID, principalID, identity.HashSecret(secret), expiresAt); err != nil {
		return uuid.Nil, "", err
	}
	for _, group := range groupKeys {
		level := groups[group]
		if _, err := tx.Exec(ctx, `INSERT INTO access.grants(user_id,group_key,access_level) VALUES($1,$2,$3)`, principalID, group, int16(level)); err != nil {
			return uuid.Nil, "", err
		}
	}
	if err := bumpAccessEpoch(ctx, tx, principalID); err != nil {
		return uuid.Nil, "", err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_notify('core_cache_invalidation','*')`); err != nil {
		return uuid.Nil, "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, "", fmt.Errorf("commit principal and group access: %w", err)
	}
	return principalID, secret, nil
}

func validatePrincipalGroups(groups map[string]access.Level) error {
	for group, level := range groups {
		if !appaccess.ValidGroupKey(group) || level > access.LevelWrite {
			return errors.New("invalid principal group access")
		}
	}
	return nil
}

func sortedGroupKeys(groups map[string]access.Level) []string {
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func IssueCredential(ctx context.Context, pool *pgxpool.Pool, actor access.Principal, principalID uuid.UUID, expiresAt *time.Time) (uuid.UUID, string, error) {
	if !actor.Active || actor.ID == "" {
		return uuid.Nil, "", ErrUnauthorized
	}
	if pool == nil || principalID == uuid.Nil || !validExpiry(expiresAt) {
		return uuid.Nil, "", errors.New("invalid credential request")
	}
	actorID, err := uuid.Parse(actor.ID)
	if err != nil {
		return uuid.Nil, "", ErrUnauthorized
	}
	secret, err := identity.NewSecret()
	if err != nil {
		return uuid.Nil, "", err
	}
	credentialID, err := uuid.NewRandom()
	if err != nil {
		return uuid.Nil, "", err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, "", err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := lockManagerChanges(ctx, tx); err != nil {
		return uuid.Nil, "", err
	}
	if err := requireAdministratorManager(ctx, tx, actorID); err != nil {
		return uuid.Nil, "", err
	}
	var active bool
	if err := tx.QueryRow(ctx, `SELECT active FROM access.users WHERE id=$1 FOR SHARE`, principalID).Scan(&active); errors.Is(err, pgx.ErrNoRows) || (err == nil && !active) {
		return uuid.Nil, "", ErrForbidden
	} else if err != nil {
		return uuid.Nil, "", err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO access.credentials(id,user_id,secret_hash,expires_at) VALUES($1,$2,$3,$4)`, credentialID, principalID, identity.HashSecret(secret), expiresAt); err != nil {
		return uuid.Nil, "", err
	}
	if err := bumpAccessEpoch(ctx, tx, principalID); err != nil {
		return uuid.Nil, "", err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_notify('core_cache_invalidation','*')`); err != nil {
		return uuid.Nil, "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, "", err
	}
	return credentialID, secret, nil
}

func RevokeCredential(ctx context.Context, pool *pgxpool.Pool, actor access.Principal, credentialID uuid.UUID) error {
	if !actor.Active || actor.ID == "" {
		return ErrUnauthorized
	}
	if pool == nil || credentialID == uuid.Nil {
		return errors.New("invalid credential")
	}
	actorID, err := uuid.Parse(actor.ID)
	if err != nil {
		return ErrUnauthorized
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := lockManagerChanges(ctx, tx); err != nil {
		return err
	}
	if err := requireAdministratorManager(ctx, tx, actorID); err != nil {
		return err
	}
	var principalID uuid.UUID
	var valid bool
	err = tx.QueryRow(ctx, `SELECT c.user_id,c.revoked_at IS NULL AND (c.expires_at IS NULL OR c.expires_at>now())
		FROM access.credentials c WHERE c.id=$1 FOR UPDATE`, credentialID).Scan(&principalID, &valid)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrForbidden
	}
	if err != nil {
		return err
	}
	var managerActive bool
	if err := tx.QueryRow(ctx, `SELECT active AND can_manage_administrators FROM access.users WHERE id=$1 FOR SHARE`, principalID).Scan(&managerActive); err != nil {
		return err
	}
	if managerActive && valid {
		remaining, err := countRemainingManagers(ctx, tx, credentialID)
		if err != nil {
			return err
		}
		if remaining == 0 {
			return ErrManagerInvariant
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE access.credentials SET revoked_at=COALESCE(revoked_at,now()) WHERE id=$1`, credentialID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE access.sessions SET revoked_at=COALESCE(revoked_at,now()) WHERE credential_id=$1`, credentialID); err != nil {
		return err
	}
	if err := bumpAccessEpoch(ctx, tx, principalID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_notify('core_cache_invalidation','*')`); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func RecoverBootstrapCredential(ctx context.Context, pool *pgxpool.Pool) (uuid.UUID, string, error) {
	if pool == nil {
		return uuid.Nil, "", errors.New("nil database pool")
	}
	secret, err := identity.NewSecret()
	if err != nil {
		return uuid.Nil, "", err
	}
	credentialID, err := uuid.NewRandom()
	if err != nil {
		return uuid.Nil, "", err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, "", err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('zubrium_core_v2_bootstrap'))`); err != nil {
		return uuid.Nil, "", err
	}
	if err := lockManagerChanges(ctx, tx); err != nil {
		return uuid.Nil, "", err
	}
	var principalID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT user_id FROM access.bootstrap WHERE singleton=true FOR UPDATE`).Scan(&principalID); err != nil {
		return uuid.Nil, "", ErrUnauthorized
	}
	if _, err := tx.Exec(ctx, `UPDATE access.users SET active=true,can_manage_administrators=true WHERE id=$1`, principalID); err != nil {
		return uuid.Nil, "", err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO access.credentials(id,user_id,secret_hash) VALUES($1,$2,$3)`, credentialID, principalID, identity.HashSecret(secret)); err != nil {
		return uuid.Nil, "", err
	}
	if err := bumpAccessEpoch(ctx, tx, principalID); err != nil {
		return uuid.Nil, "", err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_notify('core_cache_invalidation','*')`); err != nil {
		return uuid.Nil, "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, "", err
	}
	return credentialID, secret, nil
}

func validExpiry(expiresAt *time.Time) bool {
	return expiresAt == nil || expiresAt.After(time.Now())
}

func lockManagerChanges(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('zubrium_core_v2_manager_integrity'))`)
	return err
}

func countOtherManagers(ctx context.Context, tx pgx.Tx, principalID, credentialID uuid.UUID) (int64, error) {
	var count int64
	err := tx.QueryRow(ctx, `SELECT count(*) FROM access.users p
		WHERE p.id<>$1 AND p.active AND p.can_manage_administrators AND EXISTS (
			SELECT 1 FROM access.credentials c WHERE c.user_id=p.id AND c.id<>$2
			AND c.revoked_at IS NULL AND (c.expires_at IS NULL OR c.expires_at>now())
		)`, principalID, credentialID).Scan(&count)
	return count, err
}

func countRemainingManagers(ctx context.Context, tx pgx.Tx, credentialID uuid.UUID) (int64, error) {
	var count int64
	err := tx.QueryRow(ctx, `SELECT count(*) FROM access.users p
		WHERE p.active AND p.can_manage_administrators AND EXISTS (
			SELECT 1 FROM access.credentials c WHERE c.user_id=p.id AND c.id<>$1
			AND c.revoked_at IS NULL AND (c.expires_at IS NULL OR c.expires_at>now())
		)`, credentialID).Scan(&count)
	return count, err
}
