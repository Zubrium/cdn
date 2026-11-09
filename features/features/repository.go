package features

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("feature not found")

// Feature is the stable identity created by an imported immutable build.
// Schemas and versions are stored on feature_builds, not on global pointers.
type Feature struct {
	ID        uuid.UUID  `json:"id"`
	Key       string     `json:"key"`
	Name      string     `json:"name"`
	Type      string     `json:"type"`
	Status    string     `json:"status"`
	Revision  int64      `json:"revision"`
	CreatedBy *uuid.UUID `json:"created_by,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// Version is an immutable schema version recorded when a feature build is
// imported. Submission history uses it to render the field labels applicable
// when each submission was received.
type Version struct {
	ID          uuid.UUID       `json:"id"`
	FeatureID   uuid.UUID       `json:"feature_id"`
	Version     int             `json:"version"`
	Schema      json.RawMessage `json:"schema"`
	Status      string          `json:"status"`
	CreatedBy   *uuid.UUID      `json:"created_by,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	PublishedAt *time.Time      `json:"published_at"`
}

type ListParams struct {
	Status     string
	Type       string
	Search     string
	BeforeTime *time.Time
	BeforeID   *uuid.UUID
	Limit      int
}

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

func (r *Repository) GetByID(ctx context.Context, id uuid.UUID) (Feature, error) {
	return scanFeature(r.pool.QueryRow(ctx, `
		SELECT f.id, f.key, f.name, CASE WHEN EXISTS (SELECT 1 FROM feature_versions v WHERE v.feature_id=f.id AND v.status='published') THEN 'form' ELSE 'feature' END,
		       f.status, f.revision, f.created_by, f.created_at, f.updated_at
		  FROM features f WHERE f.id = $1`, id))
}

// GetIdentityByKey resolves feature metadata for historical submissions. The
// active schema always comes from the branch's selected snapshot.
func (r *Repository) GetIdentityByKey(ctx context.Context, key string) (Feature, error) {
	return scanFeature(r.pool.QueryRow(ctx, `
		SELECT f.id, f.key, f.name, CASE WHEN EXISTS (SELECT 1 FROM feature_versions v WHERE v.feature_id=f.id AND v.status='published') THEN 'form' ELSE 'feature' END,
		       f.status, f.revision, f.created_by, f.created_at, f.updated_at
		  FROM features f WHERE f.key = $1`, key))
}

func (r *Repository) List(ctx context.Context, p ListParams) ([]Feature, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT f.id, f.key, f.name, CASE WHEN EXISTS (SELECT 1 FROM feature_versions v WHERE v.feature_id=f.id AND v.status='published') THEN 'form' ELSE 'feature' END,
		       f.status, f.revision, f.created_by, f.created_at, f.updated_at
		  FROM features f
		 WHERE ($1 = '' OR f.status = $1)
		   AND ($2 = '' OR ($2 = 'form' AND EXISTS (SELECT 1 FROM feature_versions v WHERE v.feature_id=f.id AND v.status='published')))
		   AND ($3 = '' OR f.name ILIKE '%' || $3 || '%' OR f.key ILIKE '%' || $3 || '%')
		   AND ($4::timestamptz IS NULL OR (f.created_at, f.id) < ($4, $5))
		 ORDER BY f.created_at DESC, f.id DESC
		 LIMIT $6`, p.Status, p.Type, p.Search, p.BeforeTime, p.BeforeID, p.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Feature, 0, p.Limit)
	for rows.Next() {
		feature, err := scanFeature(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, feature)
	}
	return out, rows.Err()
}

func (r *Repository) Versions(ctx context.Context, featureID uuid.UUID, limit int) ([]Version, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, feature_id, version, schema, status, created_by, created_at, published_at
		  FROM feature_versions
		 WHERE feature_id = $1
		 ORDER BY version DESC
		 LIMIT $2`, featureID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Version, 0)
	for rows.Next() {
		version, err := scanVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, version)
	}
	return out, rows.Err()
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanFeature(row rowScanner) (Feature, error) {
	var feature Feature
	err := row.Scan(&feature.ID, &feature.Key, &feature.Name, &feature.Type, &feature.Status, &feature.Revision,
		&feature.CreatedBy, &feature.CreatedAt, &feature.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Feature{}, ErrNotFound
	}
	return feature, err
}

func scanVersion(row rowScanner) (Version, error) {
	var version Version
	err := row.Scan(&version.ID, &version.FeatureID, &version.Version, &version.Schema, &version.Status,
		&version.CreatedBy, &version.CreatedAt, &version.PublishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Version{}, ErrNotFound
	}
	return version, err
}
