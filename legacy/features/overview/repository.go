package overview

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Counts struct {
	FeaturesActive      int64 `json:"features_active"`
	FeaturesArchived    int64 `json:"features_archived"`
	DraftModelSnapshots int64 `json:"draft_model_snapshots"`
	DraftModelRecords   int64 `json:"draft_model_records"`
	NewSubmissions      int64 `json:"new_submissions"`
	MediaReady          int64 `json:"media_ready"`
	FailedMedia         int64 `json:"failed_media"`
}

type DraftModelSnapshot struct {
	SnapshotID uuid.UUID `json:"snapshot_id"`
	Name       string    `json:"name"`
	Revision   int64     `json:"revision"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type NewSubmission struct {
	ID          uuid.UUID `json:"id"`
	FeatureKey  string    `json:"feature_key"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	DataPreview string    `json:"data_preview"`
}

type FailedMedia struct {
	ID               uuid.UUID `json:"id"`
	OriginalFilename string    `json:"original_filename"`
	CreatedAt        time.Time `json:"created_at"`
}

type Attention struct {
	DraftModelSnapshots []DraftModelSnapshot `json:"draft_model_snapshots"`
	NewSubmissions      []NewSubmission      `json:"new_submissions"`
	FailedMedia         []FailedMedia        `json:"failed_media"`
}

type Activity struct {
	Kind       string    `json:"kind"`
	Title      string    `json:"title"`
	FeatureKey *string   `json:"feature_key"`
	OccurredAt time.Time `json:"occurred_at"`
}

type Overview struct {
	Counts         Counts     `json:"counts"`
	Attention      Attention  `json:"attention"`
	RecentActivity []Activity `json:"recent_activity"`
}

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

func (r *Repository) Load(ctx context.Context) (Overview, error) {
	out := Overview{
		Attention: Attention{
			DraftModelSnapshots: []DraftModelSnapshot{},
			NewSubmissions:      []NewSubmission{},
			FailedMedia:         []FailedMedia{},
		},
		RecentActivity: []Activity{},
	}
	if err := r.pool.QueryRow(ctx, `
		SELECT
		  (SELECT count(*) FROM features WHERE status = 'active'),
		  (SELECT count(*) FROM features WHERE status = 'archived'),
		  (SELECT count(*) FROM site_snapshot_model_workspaces WHERE revision > published_revision),
		  (SELECT COALESCE(sum(jsonb_array_length(draft_records)),0) FROM site_snapshot_model_workspaces WHERE revision > published_revision),
		  (SELECT count(*) FROM form_submissions WHERE status = 'new' AND deleted_at IS NULL AND archived_at IS NULL),
		  (SELECT count(*) FROM media_objects WHERE status = 'ready'),
		  (SELECT count(*) FROM media_objects WHERE status = 'failed')`,
	).Scan(&out.Counts.FeaturesActive, &out.Counts.FeaturesArchived,
		&out.Counts.DraftModelSnapshots, &out.Counts.DraftModelRecords, &out.Counts.NewSubmissions,
		&out.Counts.MediaReady, &out.Counts.FailedMedia); err != nil {
		return Overview{}, err
	}

	rows, err := r.pool.Query(ctx, `
		SELECT s.id, s.name, w.revision, w.updated_at
		  FROM site_snapshot_model_workspaces w
		  JOIN site_snapshots s ON s.id = w.snapshot_id
		 WHERE w.revision > w.published_revision
		 ORDER BY w.updated_at DESC
		 LIMIT 5`)
	if err != nil {
		return Overview{}, err
	}
	for rows.Next() {
		var item DraftModelSnapshot
		if err := rows.Scan(&item.SnapshotID, &item.Name, &item.Revision, &item.UpdatedAt); err != nil {
			rows.Close()
			return Overview{}, err
		}
		out.Attention.DraftModelSnapshots = append(out.Attention.DraftModelSnapshots, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return Overview{}, err
	}
	rows.Close()

	rows, err = r.pool.Query(ctx, `
		SELECT s.id, f.key, s.status, s.created_at,
		       COALESCE(NULLIF(s.data->>'name',''), NULLIF(s.data->>'title',''),
		                NULLIF(s.data->>'email',''), left(s.data::text, 120))
		  FROM form_submissions s
		  JOIN features f ON f.id = s.feature_id
		 WHERE s.status = 'new' AND s.deleted_at IS NULL AND s.archived_at IS NULL
		 ORDER BY s.created_at DESC
		 LIMIT 5`)
	if err != nil {
		return Overview{}, err
	}
	for rows.Next() {
		var s NewSubmission
		if err := rows.Scan(&s.ID, &s.FeatureKey, &s.Status, &s.CreatedAt, &s.DataPreview); err != nil {
			rows.Close()
			return Overview{}, err
		}
		out.Attention.NewSubmissions = append(out.Attention.NewSubmissions, s)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return Overview{}, err
	}
	rows.Close()

	rows, err = r.pool.Query(ctx, `
		SELECT id, original_filename, created_at
		  FROM media_objects
		 WHERE status = 'failed'
		 ORDER BY created_at DESC
		 LIMIT 5`)
	if err != nil {
		return Overview{}, err
	}
	for rows.Next() {
		var m FailedMedia
		if err := rows.Scan(&m.ID, &m.OriginalFilename, &m.CreatedAt); err != nil {
			rows.Close()
			return Overview{}, err
		}
		out.Attention.FailedMedia = append(out.Attention.FailedMedia, m)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return Overview{}, err
	}
	rows.Close()

	rows, err = r.pool.Query(ctx, `
		SELECT kind, title, feature_key, occurred_at FROM (
			(SELECT 'feature' AS kind, 'Feature: ' || name AS title, key AS feature_key, updated_at AS occurred_at
			   FROM features ORDER BY updated_at DESC LIMIT 10)
			UNION ALL
			(SELECT 'content' AS kind, 'Черновик данных: ' || s.name AS title, NULL::text AS feature_key, w.updated_at AS occurred_at
			   FROM site_snapshot_model_workspaces w JOIN site_snapshots s ON s.id = w.snapshot_id
			  WHERE w.revision > w.published_revision
			  ORDER BY w.updated_at DESC LIMIT 10)
			UNION ALL
			(SELECT 'content' AS kind, 'Опубликованы данные: ' || s.name AS title, NULL::text AS feature_key, p.created_at AS occurred_at
			   FROM snapshot_model_content_publications p JOIN site_snapshots s ON s.id = p.snapshot_id
			  ORDER BY p.created_at DESC LIMIT 10)
			UNION ALL
			(SELECT 'submission' AS kind, 'Заявка: ' || f.name AS title, f.key AS feature_key, s.created_at AS occurred_at
			   FROM form_submissions s JOIN features f ON f.id = s.feature_id
			  WHERE s.deleted_at IS NULL
			  ORDER BY s.created_at DESC LIMIT 10)
			UNION ALL
			(SELECT 'media' AS kind, 'Медиа: ' || original_filename AS title, NULL::text AS feature_key, created_at AS occurred_at
			   FROM media_objects ORDER BY created_at DESC LIMIT 10)
		) activity
		ORDER BY occurred_at DESC
		LIMIT 10`)
	if err != nil {
		return Overview{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var a Activity
		if err := rows.Scan(&a.Kind, &a.Title, &a.FeatureKey, &a.OccurredAt); err != nil {
			return Overview{}, err
		}
		out.RecentActivity = append(out.RecentActivity, a)
	}
	if err := rows.Err(); err != nil {
		return Overview{}, err
	}
	return out, nil
}
