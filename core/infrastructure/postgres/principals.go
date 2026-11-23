package postgres

import (
	"context"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Zubrium/cdn/core/domain/access"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PrincipalRecord struct {
	ID                      uuid.UUID
	Name                    string
	Active                  bool
	CanManageAdministrators bool
}

var ErrManagerInvariant = access.ErrManagerInvariant

func ListPrincipals(ctx context.Context, pool *pgxpool.Pool, actor access.Principal) ([]PrincipalRecord, error) {
	if pool == nil {
		return nil, errors.New("nil database pool")
	}
	actorID, err := parsePrincipalID(actor.ID)
	if err != nil || !actor.Active {
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
	rows, err := tx.Query(ctx, `SELECT id,name,active,can_manage_administrators FROM access.users ORDER BY created_at,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	principals := make([]PrincipalRecord, 0)
	for rows.Next() {
		var principal PrincipalRecord
		if err := rows.Scan(&principal.ID, &principal.Name, &principal.Active, &principal.CanManageAdministrators); err != nil {
			return nil, err
		}
		principals = append(principals, principal)
	}
	return principals, rows.Err()
}

func SetPrincipalManagement(ctx context.Context, pool *pgxpool.Pool, actor access.Principal, targetID uuid.UUID, enabled bool) error {
	if pool == nil || targetID == uuid.Nil {
		return errors.New("invalid principal management update")
	}
	actorID, err := parsePrincipalID(actor.ID)
	if err != nil || !actor.Active {
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
	var targetActive, currentlyManaging bool
	if err := tx.QueryRow(ctx, `SELECT active,can_manage_administrators FROM access.users WHERE id=$1 FOR UPDATE`, targetID).Scan(&targetActive, &currentlyManaging); errors.Is(err, pgx.ErrNoRows) || (err == nil && !targetActive) {
		return ErrForbidden
	} else if err != nil {
		return err
	}
	if currentlyManaging && !enabled {
		var hasLiveCredential bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM access.credentials WHERE user_id=$1 AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at>now()))`, targetID).Scan(&hasLiveCredential); err != nil {
			return err
		}
		if hasLiveCredential {
			count, err := countOtherManagers(ctx, tx, targetID, uuid.Nil)
			if err != nil {
				return err
			}
			if count == 0 {
				return ErrManagerInvariant
			}
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE access.users SET can_manage_administrators=$2 WHERE id=$1`, targetID, enabled); err != nil {
		return err
	}
	if err := bumpAccessEpoch(ctx, tx, targetID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_notify('core_cache_invalidation','*')`); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func DeactivatePrincipal(ctx context.Context, pool *pgxpool.Pool, actor access.Principal, principalID uuid.UUID) error {
	if pool == nil || principalID == uuid.Nil {
		return errors.New("invalid principal")
	}
	actorID, err := parsePrincipalID(actor.ID)
	if err != nil || !actor.Active {
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
	var targetActive, targetManaging bool
	if err := tx.QueryRow(ctx, `SELECT active,can_manage_administrators FROM access.users WHERE id=$1 FOR UPDATE`, principalID).Scan(&targetActive, &targetManaging); errors.Is(err, pgx.ErrNoRows) {
		return ErrForbidden
	} else if err != nil {
		return err
	}
	if targetActive && targetManaging {
		var hasLiveCredential bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM access.credentials WHERE user_id=$1 AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at>now()))`, principalID).Scan(&hasLiveCredential); err != nil {
			return err
		}
		if hasLiveCredential {
			count, err := countOtherManagers(ctx, tx, principalID, uuid.Nil)
			if err != nil {
				return err
			}
			if count == 0 {
				return ErrManagerInvariant
			}
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE access.users SET active=false WHERE id=$1`, principalID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE access.credentials SET revoked_at=COALESCE(revoked_at,now()) WHERE user_id=$1`, principalID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE access.sessions SET revoked_at=COALESCE(revoked_at,now())
		WHERE credential_id IN (SELECT id FROM access.credentials WHERE user_id=$1)`, principalID); err != nil {
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

func requireAdministratorManager(ctx context.Context, tx pgx.Tx, actorID uuid.UUID) error {
	var allowed bool
	err := tx.QueryRow(ctx, `SELECT p.active AND p.can_manage_administrators AND EXISTS (
		SELECT 1 FROM access.credentials c WHERE c.user_id=p.id AND c.revoked_at IS NULL AND (c.expires_at IS NULL OR c.expires_at>now())
	) FROM access.users p WHERE p.id=$1 FOR SHARE`, actorID).Scan(&allowed)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !allowed) {
		return ErrForbidden
	}
	return err
}

func validPrincipalName(name string) bool {
	if name == "" || len(name) > 200 || !utf8.ValidString(name) || strings.TrimSpace(name) != name {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func parsePrincipalID(value string) (uuid.UUID, error) {
	id, err := uuid.Parse(value)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, ErrUnauthorized
	}
	return id, nil
}
