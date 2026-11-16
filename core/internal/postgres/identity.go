package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Zubrium/cdn/core/access"
	"github.com/Zubrium/cdn/core/identity"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrUnauthorized = access.ErrUnauthorized
var ErrForbidden = access.ErrForbidden

func Bootstrap(ctx context.Context, pool *pgxpool.Pool, configuredSecret string) (string, bool, error) {
	return BootstrapWithGroups(ctx, pool, configuredSecret, nil)
}

// BootstrapWithGroups creates the first manager and its group assignments in
// one transaction. A failed first boot therefore cannot strand a generated
// credential that was never returned to the operator.
func BootstrapWithGroups(ctx context.Context, pool *pgxpool.Pool, configuredSecret string, groups map[string]access.Level) (string, bool, error) {
	if pool == nil {
		return "", false, errors.New("nil database pool")
	}
	if configuredSecret != "" && !validSecret(configuredSecret) {
		return "", false, errors.New("invalid bootstrap credential")
	}
	if err := validateConfiguredGroups(groups); err != nil {
		return "", false, err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return "", false, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('zubrium_core_v2_bootstrap'))`); err != nil {
		return "", false, fmt.Errorf("lock Core bootstrap: %w", err)
	}
	var bootstrapID uuid.UUID
	err = tx.QueryRow(ctx, `SELECT principal_id FROM core_bootstrap WHERE singleton=true`).Scan(&bootstrapID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", false, fmt.Errorf("read Core bootstrap principal: %w", err)
	}
	created := errors.Is(err, pgx.ErrNoRows)
	secret := ""
	if created {
		if configuredSecret == "" {
			generatedSecret, secretErr := identity.NewSecret()
			if secretErr != nil {
				return "", false, fmt.Errorf("generate Core bootstrap credential: %w", secretErr)
			}
			secret = generatedSecret
		} else {
			secret = configuredSecret
		}
		bootstrapID, err = uuid.NewRandom()
		if err != nil {
			return "", false, fmt.Errorf("generate Core bootstrap principal ID: %w", err)
		}
		credentialID, idErr := uuid.NewRandom()
		if idErr != nil {
			return "", false, fmt.Errorf("generate Core bootstrap credential ID: %w", idErr)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO core_principals(id,name,active,can_manage_administrators) VALUES($1,'Initial administrator',true,true)`, bootstrapID); err != nil {
			return "", false, fmt.Errorf("create bootstrap principal: %w", err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO core_bootstrap(singleton,principal_id) VALUES(true,$1)`, bootstrapID); err != nil {
			return "", false, fmt.Errorf("record bootstrap principal: %w", err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO core_credentials(id,principal_id,secret_hash) VALUES($1,$2,$3)`, credentialID, bootstrapID, identity.HashSecret(secret)); err != nil {
			return "", false, fmt.Errorf("create bootstrap credential: %w", err)
		}
	}
	if groups != nil {
		if err := reconcileAccessGroupsTx(ctx, tx, groups, bootstrapID); err != nil {
			return "", false, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return "", false, fmt.Errorf("commit bootstrap: %w", err)
	}
	return secret, created, nil
}

func Authenticate(ctx context.Context, pool *pgxpool.Pool, secret string) (access.Principal, error) {
	if pool == nil {
		return access.Principal{}, errors.New("nil database pool")
	}
	if !validSecret(secret) {
		return access.Principal{}, ErrUnauthorized
	}
	return AuthenticateHash(ctx, pool, identity.HashSecret(secret))
}

func AuthenticateHash(ctx context.Context, pool *pgxpool.Pool, hash []byte) (access.Principal, error) {
	if pool == nil {
		return access.Principal{}, errors.New("nil database pool")
	}
	if len(hash) != 32 {
		return access.Principal{}, ErrUnauthorized
	}
	var principal access.Principal
	err := pool.QueryRow(ctx, `SELECT p.id::text,p.active,p.can_manage_administrators
		FROM core_credentials c JOIN core_principals p ON p.id=c.principal_id
		WHERE c.secret_hash=$1 AND c.revoked_at IS NULL AND (c.expires_at IS NULL OR c.expires_at>now()) AND p.active`, hash).
		Scan(&principal.ID, &principal.Active, &principal.CanManageAdministrators)
	if errors.Is(err, pgx.ErrNoRows) {
		return access.Principal{}, ErrUnauthorized
	}
	if err != nil {
		return access.Principal{}, err
	}
	return principal, nil
}

func validSecret(secret string) bool {
	return len(secret) >= 32 && len(secret) <= 512 && strings.TrimSpace(secret) == secret
}
