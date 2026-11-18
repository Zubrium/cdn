package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/Zubrium/cdn/core/domain/access"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func CreateSession(ctx context.Context, pool *pgxpool.Pool, credentialHash, sessionHash []byte, now, idleExpiresAt, absoluteUntil time.Time) (access.CredentialSession, error) {
	if pool == nil || len(credentialHash) != 32 || len(sessionHash) != 32 ||
		!idleExpiresAt.After(now) || !absoluteUntil.After(idleExpiresAt) {
		return access.CredentialSession{}, access.ErrUnauthorized
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return access.CredentialSession{}, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	var principal access.Principal
	var principalID, credentialID uuid.UUID
	err = tx.QueryRow(ctx, `SELECT c.id,p.id,p.name,p.active,p.can_manage_administrators
		FROM access.credentials c JOIN access.users p ON p.id=c.user_id
		WHERE c.secret_hash=$1 AND c.revoked_at IS NULL AND (c.expires_at IS NULL OR c.expires_at>$2) AND p.active
		FOR SHARE OF c,p`, credentialHash, now).
		Scan(&credentialID, &principalID, &principal.Name, &principal.Active, &principal.CanManageAdministrators)
	if errors.Is(err, pgx.ErrNoRows) {
		return access.CredentialSession{}, access.ErrUnauthorized
	}
	if err != nil {
		return access.CredentialSession{}, err
	}
	principal.ID = principalID.String()
	principal.Active = true
	sessionID, err := uuid.NewRandom()
	if err != nil {
		return access.CredentialSession{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO access.sessions
		(id,credential_id,token_hash,created_at,last_seen_at,idle_expires_at,absolute_expires_at)
		VALUES($1,$2,$3,$4,$4,$5,$6)`, sessionID, credentialID, sessionHash, now, idleExpiresAt, absoluteUntil); err != nil {
		return access.CredentialSession{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return access.CredentialSession{}, err
	}
	return access.CredentialSession{ID: sessionID.String(), CredentialID: credentialID.String(), Principal: principal,
		CreatedAt: now, LastSeenAt: now, IdleExpiresAt: idleExpiresAt, AbsoluteExpires: absoluteUntil}, nil
}

func AuthenticateSession(ctx context.Context, pool *pgxpool.Pool, sessionHash []byte, now time.Time, idleTTL time.Duration) (access.CredentialSession, error) {
	if pool == nil || len(sessionHash) != 32 || idleTTL <= 0 {
		return access.CredentialSession{}, access.ErrUnauthorized
	}
	nextIdleExpiry := now.Add(idleTTL)
	var session access.CredentialSession
	err := pool.QueryRow(ctx, `UPDATE access.sessions s
		SET last_seen_at=$2,idle_expires_at=LEAST($3,s.absolute_expires_at)
		FROM access.users p,access.credentials c
		WHERE s.token_hash=$1 AND s.revoked_at IS NULL AND s.idle_expires_at>$2 AND s.absolute_expires_at>$2
			AND p.id=c.user_id AND p.active
			AND c.id=s.credential_id AND c.revoked_at IS NULL
			AND (c.expires_at IS NULL OR c.expires_at>$2)
		RETURNING s.id::text,c.id::text,p.id::text,p.name,p.active,p.can_manage_administrators,
			s.created_at,s.last_seen_at,s.idle_expires_at,s.absolute_expires_at`,
		sessionHash, now, nextIdleExpiry).
		Scan(&session.ID, &session.CredentialID, &session.Principal.ID, &session.Principal.Name,
			&session.Principal.Active, &session.Principal.CanManageAdministrators, &session.CreatedAt,
			&session.LastSeenAt, &session.IdleExpiresAt, &session.AbsoluteExpires)
	if errors.Is(err, pgx.ErrNoRows) {
		return access.CredentialSession{}, access.ErrUnauthorized
	}
	if err != nil {
		return access.CredentialSession{}, err
	}
	return session, nil
}

func RevokeSession(ctx context.Context, pool *pgxpool.Pool, sessionHash []byte, now time.Time) error {
	if pool == nil || len(sessionHash) != 32 {
		return access.ErrUnauthorized
	}
	_, err := pool.Exec(ctx, `UPDATE access.sessions SET revoked_at=COALESCE(revoked_at,$2) WHERE token_hash=$1`, sessionHash, now)
	return err
}

func PruneSessions(ctx context.Context, pool *pgxpool.Pool, before time.Time, limit int) (int64, error) {
	if pool == nil || before.IsZero() || limit <= 0 {
		return 0, access.ErrInvalid
	}
	tag, err := pool.Exec(ctx, `DELETE FROM access.sessions WHERE id IN (
		SELECT id FROM access.sessions
		WHERE absolute_expires_at<$1 OR idle_expires_at<$1 OR (revoked_at IS NOT NULL AND revoked_at<$1)
		ORDER BY COALESCE(revoked_at,absolute_expires_at),id LIMIT $2
	)`, before, limit)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
