package auth

import (
	"context"
	"errors"
	"strconv"
	"time"
	"websiteengine/pkg/principal"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

func (r *Repository) FindActiveByHash(ctx context.Context, hash []byte) (principal.Principal, error) {
	var p principal.Principal
	err := r.pool.QueryRow(ctx, `
		SELECT id, name, role, created_by, expires_at, created_at
		  FROM access_tokens
		 WHERE token_hash = $1
		   AND revoked_at IS NULL
		   AND (expires_at IS NULL OR expires_at > now())`,
		hash,
	).Scan(&p.TokenID, &p.Name, &p.Role, &p.CreatedBy, &p.ExpiresAt, &p.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return principal.Principal{}, ErrUnauthorized
	}
	p.ID = p.TokenID
	return p, err
}

func (r *Repository) Touch(ctx context.Context, id uuid.UUID) {
	_, _ = r.pool.Exec(ctx, "UPDATE access_tokens SET last_used_at = now() WHERE id = $1", id)
}

func (r *Repository) Create(ctx context.Context, id uuid.UUID, name string, hash []byte, role string, createdBy *uuid.UUID, expiresAt *time.Time) (Token, error) {
	var t Token
	err := r.pool.QueryRow(ctx, `
		INSERT INTO access_tokens (id, name, token_hash, role, created_by, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, name, role, created_by, expires_at, revoked_at, last_used_at, created_at`,
		id, name, hash, role, createdBy, expiresAt,
	).Scan(&t.ID, &t.Name, &t.Role, &t.CreatedBy, &t.ExpiresAt, &t.RevokedAt, &t.LastUsedAt, &t.CreatedAt)
	return t, err
}

func (r *Repository) List(ctx context.Context, creator *uuid.UUID, beforeCreatedAt *time.Time, beforeID *uuid.UUID, limit int) ([]Token, error) {
	args := []any{creator}
	pagination := ""
	if beforeCreatedAt != nil && beforeID != nil {
		args = append(args, *beforeCreatedAt, *beforeID)
		pagination = ` AND (created_at, id) < ($2, $3)`
	}
	args = append(args, limit)
	query := `
		SELECT id, name, role, created_by, expires_at, revoked_at, last_used_at, created_at
		  FROM access_tokens
		 WHERE ($1::uuid IS NULL OR created_by = $1::uuid)` + pagination + `
		 ORDER BY created_at DESC, id DESC
		 LIMIT $` + strconv.Itoa(len(args))
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]Token, 0, limit)
	for rows.Next() {
		var t Token
		if err := rows.Scan(&t.ID, &t.Name, &t.Role, &t.CreatedBy, &t.ExpiresAt, &t.RevokedAt, &t.LastUsedAt, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r *Repository) Get(ctx context.Context, id uuid.UUID) (Token, error) {
	var t Token
	err := r.pool.QueryRow(ctx, `
		SELECT id, name, role, created_by, expires_at, revoked_at, last_used_at, created_at
		  FROM access_tokens
		 WHERE id = $1`, id,
	).Scan(&t.ID, &t.Name, &t.Role, &t.CreatedBy, &t.ExpiresAt, &t.RevokedAt, &t.LastUsedAt, &t.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Token{}, ErrNotFound
	}
	return t, err
}

func (r *Repository) Revoke(ctx context.Context, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE access_tokens
		   SET revoked_at = COALESCE(revoked_at, now())
		 WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *Repository) HashExists(ctx context.Context, hash []byte) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM access_tokens WHERE token_hash = $1)`, hash).Scan(&exists)
	return exists, err
}

func (r *Repository) HasActiveAdmin(ctx context.Context) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM access_tokens
			 WHERE role = 'admin'
			   AND revoked_at IS NULL
			   AND (expires_at IS NULL OR expires_at > now())
		)`).Scan(&exists)
	return exists, err
}
