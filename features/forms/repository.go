package forms

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"websiteengine/pkg/environment"
)

var (
	ErrNotFound   = errors.New("submission not found")
	ErrConflict   = errors.New("submission conflict")
	ErrValidation = errors.New("submission validation")
)

type DBTX interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

type Submission struct {
	ID              uuid.UUID       `json:"id"`
	FeatureID       uuid.UUID       `json:"feature_id"`
	FeatureKey      string          `json:"feature_key"`
	SchemaVersionID uuid.UUID       `json:"schema_version_id"`
	Environment     string          `json:"environment"`
	Data            json.RawMessage `json:"data"`
	Status          string          `json:"status"`
	ProcessedBy     *uuid.UUID      `json:"processed_by"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
	ArchivedAt      *time.Time      `json:"archived_at,omitempty"`
	DeletedAt       *time.Time      `json:"deleted_at,omitempty"`
}

// MarshalJSON exposes the configured branch identity while preserving the
// historical environment alias for existing consumers.
func (s Submission) MarshalJSON() ([]byte, error) {
	type plain Submission
	return json.Marshal(struct {
		plain
		BranchID string `json:"branchId"`
	}{plain(s), s.Environment})
}

type FormSchema struct {
	FeatureID   uuid.UUID
	FeatureKey  string
	VersionID   uuid.UUID
	Version     int
	Schema      json.RawMessage
	Environment environment.Environment
}

type ListParams struct {
	FeatureID       uuid.UUID
	Environment     environment.Environment
	Status          string
	CreatedFrom     *time.Time
	CreatedTo       *time.Time
	SearchFields    []string
	Search          string
	IncludeArchived bool
	SortAsc         bool
	BeforeTime      *time.Time
	BeforeID        *uuid.UUID
	Limit           int
}

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

func (r *Repository) Begin(ctx context.Context) (pgx.Tx, error) { return r.pool.Begin(ctx) }

func (r *Repository) InsertSubmission(ctx context.Context, db DBTX, s Submission) error {
	return db.QueryRow(ctx, `
		INSERT INTO form_submissions (id, feature_id, schema_version_id, environment, data, status)
		VALUES ($1, $2, $3, $4, $5, 'new')
		RETURNING status, created_at, updated_at`,
		s.ID, s.FeatureID, s.SchemaVersionID, s.Environment, s.Data,
	).Scan(&s.Status, &s.CreatedAt, &s.UpdatedAt)
}

func (r *Repository) Get(ctx context.Context, db DBTX, id uuid.UUID) (Submission, error) {
	var s Submission
	err := db.QueryRow(ctx, `
		SELECT s.id, s.feature_id, f.key, s.schema_version_id, s.environment, s.data, s.status,
		       s.processed_by, s.created_at, s.updated_at, s.archived_at, s.deleted_at
		  FROM form_submissions s
		  JOIN features f ON f.id = s.feature_id
		 WHERE s.id = $1 AND s.deleted_at IS NULL `, id,
	).Scan(&s.ID, &s.FeatureID, &s.FeatureKey, &s.SchemaVersionID, &s.Environment, &s.Data, &s.Status,
		&s.ProcessedBy, &s.CreatedAt, &s.UpdatedAt, &s.ArchivedAt, &s.DeletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Submission{}, ErrNotFound
	}
	return s, err
}

func (r *Repository) List(ctx context.Context, db DBTX, p ListParams) ([]Submission, error) {
	var featureID any
	if p.FeatureID != uuid.Nil {
		featureID = p.FeatureID
	}
	args := []any{featureID, p.Environment.String(), p.Status, p.CreatedFrom, p.CreatedTo}
	where := []string{
		"($1::uuid IS NULL OR s.feature_id = $1)",
		"s.deleted_at IS NULL",
		"($2 = '' OR s.environment = $2)",
		"($3 = '' OR s.status = $3)",
		"($4::timestamptz IS NULL OR s.created_at >= $4)",
		"($5::timestamptz IS NULL OR s.created_at < $5)",
	}
	if !p.IncludeArchived {
		where = append(where, "s.archived_at IS NULL")
	}
	if p.Search != "" && len(p.SearchFields) > 0 {
		expressions := make([]string, 0, len(p.SearchFields))
		for _, field := range p.SearchFields {
			args = append(args, field, "%"+escapeLikeSearch(p.Search)+"%")
			expressions = append(expressions, fmt.Sprintf("(s.data->>$%d) ILIKE $%d", len(args)-1, len(args)))
		}
		where = append(where, "("+strings.Join(expressions, " OR ")+")")
	}
	if p.BeforeTime != nil && p.BeforeID != nil {
		args = append(args, p.BeforeTime, p.BeforeID)
		if p.SortAsc {
			where = append(where, fmt.Sprintf("(s.created_at, s.id) > ($%d, $%d)", len(args)-1, len(args)))
		} else {
			where = append(where, fmt.Sprintf("(s.created_at, s.id) < ($%d, $%d)", len(args)-1, len(args)))
		}
	}
	args = append(args, p.Limit)
	order := "DESC"
	if p.SortAsc {
		order = "ASC"
	}
	query := `
		SELECT s.id, s.feature_id, f.key, s.schema_version_id, s.environment, s.data, s.status,
		       s.processed_by, s.created_at, s.updated_at, s.archived_at, s.deleted_at
		  FROM form_submissions s
		  JOIN features f ON f.id = s.feature_id
		 WHERE ` + strings.Join(where, " AND ") + `
		 ORDER BY s.created_at ` + order + `, s.id ` + order + `
		 LIMIT $` + fmt.Sprint(len(args))
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Submission, 0, p.Limit)
	for rows.Next() {
		var s Submission
		if err := rows.Scan(&s.ID, &s.FeatureID, &s.FeatureKey, &s.SchemaVersionID, &s.Environment, &s.Data,
			&s.Status, &s.ProcessedBy, &s.CreatedAt, &s.UpdatedAt, &s.ArchivedAt, &s.DeletedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *Repository) UpdateStatus(ctx context.Context, id uuid.UUID, status string, actor uuid.UUID) (Submission, error) {
	tag, err := r.pool.Exec(ctx, `
		UPDATE form_submissions
		   SET status = $2, processed_by = $3
		 WHERE id = $1 AND deleted_at IS NULL`, id, status, actor)
	if err != nil {
		return Submission{}, err
	}
	if tag.RowsAffected() == 0 {
		return Submission{}, ErrNotFound
	}
	return r.Get(ctx, r.pool, id)
}

func (r *Repository) Archive(ctx context.Context, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE form_submissions
		   SET archived_at = COALESCE(archived_at, now())
		 WHERE id = $1 AND deleted_at IS NULL`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *Repository) Delete(ctx context.Context, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE form_submissions
		   SET deleted_at = COALESCE(deleted_at, now())
		 WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func escapeLikeSearch(value string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(value)
}

// HistoricalSubmissionSchemas survives branch removal and feature upgrades.
func (r *Repository) HistoricalSubmissionSchemas(ctx context.Context, featureID uuid.UUID, branch environment.Environment) ([]json.RawMessage, error) {
	var selectedFeature any
	if featureID != uuid.Nil {
		selectedFeature = featureID
	}
	rows, err := r.pool.Query(ctx, `SELECT DISTINCT v.schema FROM form_submissions s JOIN feature_versions v ON v.id=s.schema_version_id WHERE ($1::uuid IS NULL OR s.feature_id=$1) AND ($2='' OR s.environment=$2) AND s.deleted_at IS NULL`, selectedFeature, branch.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	schemas := []json.RawMessage{}
	for rows.Next() {
		var schema json.RawMessage
		if err := rows.Scan(&schema); err != nil {
			return nil, err
		}
		schemas = append(schemas, schema)
	}
	return schemas, rows.Err()
}

type BranchChoice struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

func (r *Repository) BranchChoices(ctx context.Context) ([]BranchChoice, error) {
	rows, err := r.pool.Query(ctx, `SELECT e.key,e.name,e.enabled FROM site_environments e WHERE e.enabled OR EXISTS(SELECT 1 FROM form_submissions s WHERE s.environment=e.key AND s.deleted_at IS NULL) ORDER BY e.name,e.key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []BranchChoice{}
	for rows.Next() {
		var item BranchChoice
		if err := rows.Scan(&item.ID, &item.Name, &item.Enabled); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
