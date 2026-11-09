package media

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound   = errors.New("media not found")
	ErrConflict   = errors.New("media conflict")
	ErrValidation = errors.New("media validation")
	ErrProcessing = errors.New("media processing failed")
)

type Media struct {
	OwnerID          uuid.UUID         `json:"ownerId"`
	Visibility       string            `json:"visibility"`
	SHA256           string            `json:"sha256,omitempty"`
	ID               uuid.UUID         `json:"id"`
	ObjectKey        string            `json:"object_key"`
	OriginalFilename string            `json:"original_filename"`
	ContentType      string            `json:"content_type"`
	SizeBytes        int64             `json:"size_bytes"`
	Status           string            `json:"status"`
	Variants         map[string]string `json:"variants"`
	UploadedBy       *uuid.UUID        `json:"uploaded_by,omitempty"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
}

type Repository struct {
	pool *pgxpool.Pool
}

type StoredOutput struct {
	ObjectKey string `json:"objectKey"`
	MIMEType  string `json:"mimeType"`
	Width     int    `json:"width,omitempty"`
	Height    int    `json:"height,omitempty"`
}

type RenditionJob struct {
	MediaID     uuid.UUID
	ProfileHash string
	Profile     FieldProfile
	Attempts    int
}

type RenditionRecord struct {
	MediaID     uuid.UUID               `json:"mediaId"`
	ProfileHash string                  `json:"profileHash"`
	Status      string                  `json:"status"`
	Outputs     map[string]StoredOutput `json:"outputs"`
	Error       string                  `json:"error,omitempty"`
}

func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

func (r *Repository) Create(ctx context.Context, m Media) (Media, error) {
	variantJSON, _ := json.Marshal(m.Variants)
	err := r.pool.QueryRow(ctx, `
		INSERT INTO media_objects
			(id, object_key, original_filename, content_type, size_bytes, status, variants, uploaded_by, sha256)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NULLIF($9,''))
		RETURNING created_at, updated_at, owner_id, visibility`,
		m.ID, m.ObjectKey, m.OriginalFilename, m.ContentType, m.SizeBytes, m.Status, variantJSON, m.UploadedBy, m.SHA256,
	).Scan(&m.CreatedAt, &m.UpdatedAt, &m.OwnerID, &m.Visibility)
	return m, err
}

func (r *Repository) Get(ctx context.Context, id uuid.UUID) (Media, error) {
	return scanMedia(r.pool.QueryRow(ctx, selectMedia+` WHERE id = $1`, id))
}

// ListParams describes the filters and the ordering of the media list.
type ListParams struct {
	Status             string
	Search             string
	ContentTypePattern string
	CreatedFrom        *time.Time
	CreatedTo          *time.Time
	MinSize            *int64
	MaxSize            *int64
	Sort               string
	CursorTime         *time.Time
	CursorID           *uuid.UUID
	CursorName         *string
	CursorSize         *int64
	Limit              int
}

func (r *Repository) List(ctx context.Context, p ListParams) ([]Media, error) {
	order := "created_at DESC, id DESC"
	keyset := "AND ($8::timestamptz IS NULL OR (created_at, id) < ($8, $9))"
	switch p.Sort {
	case "", "new":
	case "old":
		order = "created_at ASC, id ASC"
		keyset = "AND ($8::timestamptz IS NULL OR (created_at, id) > ($8, $9))"
	case "name":
		order = "original_filename ASC, id ASC"
		keyset = "AND ($10::text IS NULL OR (original_filename, id) > ($10, $9))"
	case "size_desc":
		order = "size_bytes DESC, id DESC"
		keyset = "AND ($11::bigint IS NULL OR (size_bytes, id) < ($11, $9))"
	case "size_asc":
		order = "size_bytes ASC, id ASC"
		keyset = "AND ($11::bigint IS NULL OR (size_bytes, id) > ($11, $9))"
	}
	rows, err := r.pool.Query(ctx, selectMedia+`
		WHERE owner_id=admin_file_owner() AND ($1 = '' OR status = $1)
		  AND ($2 = '' OR original_filename ILIKE '%' || $2 || '%')
		  AND ($3 = '' OR content_type LIKE $3)
		  AND ($4::timestamptz IS NULL OR created_at >= $4)
		  AND ($5::timestamptz IS NULL OR created_at < $5)
		  AND ($6::bigint IS NULL OR size_bytes >= $6)
		  AND ($7::bigint IS NULL OR size_bytes <= $7)
		  AND ($8::timestamptz IS NULL OR TRUE)
		  AND ($9::uuid IS NULL OR TRUE)
		  AND ($10::text IS NULL OR TRUE)
		  AND ($11::bigint IS NULL OR TRUE)
		  `+keyset+`
		ORDER BY `+order+`
		LIMIT $12`,
		p.Status, p.Search, p.ContentTypePattern, p.CreatedFrom, p.CreatedTo,
		p.MinSize, p.MaxSize, p.CursorTime, p.CursorID, p.CursorName, p.CursorSize, p.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Media, 0, p.Limit)
	for rows.Next() {
		m, err := scanMedia(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Rename updates the display filename of a media object. The object key in
// the bucket never changes, so already published content keeps working.
func (r *Repository) Rename(ctx context.Context, id uuid.UUID, filename string) (Media, error) {
	return scanMedia(r.pool.QueryRow(ctx, `
		UPDATE media_objects
		   SET original_filename = $2
		 WHERE id = $1 AND status <> 'deleted'
		RETURNING id, object_key, original_filename, content_type, size_bytes, status,
		          variants, uploaded_by, created_at, updated_at, owner_id, visibility, COALESCE(sha256,'')`, id, filename))
}

func (r *Repository) SetProcessing(ctx context.Context, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE media_objects SET status = 'processing'
		 WHERE id = $1 AND status IN ('processing', 'failed')`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrConflict
	}
	return nil
}

func (r *Repository) SetReady(ctx context.Context, id uuid.UUID, variants map[string]string) (Media, error) {
	raw, _ := json.Marshal(variants)
	return scanMedia(r.pool.QueryRow(ctx, `
		UPDATE media_objects
		   SET status = 'ready', variants = $2
		 WHERE id = $1
		RETURNING id, object_key, original_filename, content_type, size_bytes, status,
		          variants, uploaded_by, created_at, updated_at, owner_id, visibility, COALESCE(sha256,'')`, id, raw))
}

func (r *Repository) SetFailed(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `UPDATE media_objects SET status = 'failed' WHERE id = $1`, id)
	return err
}

func (r *Repository) EnsureRendition(ctx context.Context, mediaID uuid.UUID, profileHash string, profile FieldProfile) (RenditionRecord, error) {
	raw, err := json.Marshal(profile)
	if err != nil {
		return RenditionRecord{}, err
	}
	_, err = r.pool.Exec(ctx, `
		INSERT INTO media_renditions (media_id,profile_hash,profile,status)
		VALUES ($1,$2,$3::jsonb,'pending')
		ON CONFLICT (media_id,profile_hash) DO NOTHING`, mediaID, profileHash, string(raw))
	if err != nil {
		return RenditionRecord{}, err
	}
	return r.GetRendition(ctx, mediaID, profileHash)
}

func (r *Repository) GetRendition(ctx context.Context, mediaID uuid.UUID, profileHash string) (RenditionRecord, error) {
	var record RenditionRecord
	var outputs []byte
	err := r.pool.QueryRow(ctx, `
		SELECT media_id,profile_hash,status,outputs,COALESCE(last_error,'')
		  FROM media_renditions WHERE media_id=$1 AND profile_hash=$2`, mediaID, profileHash).
		Scan(&record.MediaID, &record.ProfileHash, &record.Status, &outputs, &record.Error)
	if errors.Is(err, pgx.ErrNoRows) {
		return RenditionRecord{}, ErrNotFound
	}
	if err != nil {
		return RenditionRecord{}, err
	}
	record.Outputs = map[string]StoredOutput{}
	if len(outputs) > 0 {
		if err := json.Unmarshal(outputs, &record.Outputs); err != nil {
			return RenditionRecord{}, err
		}
	}
	return record, nil
}

func (r *Repository) RetryRendition(ctx context.Context, mediaID uuid.UUID, profileHash string) (RenditionRecord, error) {
	tag, err := r.pool.Exec(ctx, `
		UPDATE media_renditions
		   SET status='pending',last_error=NULL,lease_expires_at=NULL,updated_at=now()
		 WHERE media_id=$1 AND profile_hash=$2 AND status='failed'`, mediaID, profileHash)
	if err != nil {
		return RenditionRecord{}, err
	}
	if tag.RowsAffected() == 0 {
		record, getErr := r.GetRendition(ctx, mediaID, profileHash)
		if getErr != nil {
			return RenditionRecord{}, getErr
		}
		if record.Status == "ready" {
			return record, ErrConflict
		}
	}
	return r.GetRendition(ctx, mediaID, profileHash)
}

func (r *Repository) ClaimRendition(ctx context.Context, lease time.Duration) (RenditionJob, bool, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return RenditionJob{}, false, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var job RenditionJob
	var profileRaw []byte
	err = tx.QueryRow(ctx, `
		WITH candidate AS (
			SELECT media_id,profile_hash
			  FROM media_renditions
			 WHERE status='pending' OR (status='processing' AND lease_expires_at < now())
			 ORDER BY created_at
			 FOR UPDATE SKIP LOCKED
			 LIMIT 1
		)
		UPDATE media_renditions r
		   SET status='processing',attempts=attempts+1,
		       lease_expires_at=now()+$1::interval,last_error=NULL,updated_at=now()
		  FROM candidate c
		 WHERE r.media_id=c.media_id AND r.profile_hash=c.profile_hash
		RETURNING r.media_id,r.profile_hash,r.profile,r.attempts`, lease.String()).
		Scan(&job.MediaID, &job.ProfileHash, &profileRaw, &job.Attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return RenditionJob{}, false, nil
	}
	if err != nil {
		return RenditionJob{}, false, err
	}
	if err := json.Unmarshal(profileRaw, &job.Profile); err != nil {
		return RenditionJob{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return RenditionJob{}, false, err
	}
	return job, true, nil
}

func (r *Repository) CompleteRendition(ctx context.Context, mediaID uuid.UUID, profileHash string, outputs map[string]StoredOutput) error {
	raw, err := json.Marshal(outputs)
	if err != nil {
		return err
	}
	_, err = r.pool.Exec(ctx, `
		UPDATE media_renditions
		   SET status='ready',outputs=$3::jsonb,lease_expires_at=NULL,last_error=NULL,updated_at=now()
		 WHERE media_id=$1 AND profile_hash=$2`, mediaID, profileHash, string(raw))
	return err
}

func (r *Repository) FailRendition(ctx context.Context, mediaID uuid.UUID, profileHash, message string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE media_renditions
		   SET status='failed',lease_expires_at=NULL,last_error=$3,updated_at=now()
		 WHERE media_id=$1 AND profile_hash=$2`, mediaID, profileHash, message)
	return err
}

func (r *Repository) RenditionObjectKeys(ctx context.Context, mediaID uuid.UUID) ([]string, error) {
	rows, err := r.pool.Query(ctx, `SELECT outputs FROM media_renditions WHERE media_id=$1`, mediaID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	keys := make([]string, 0)
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var outputs map[string]StoredOutput
		if err := json.Unmarshal(raw, &outputs); err != nil {
			return nil, err
		}
		for _, output := range outputs {
			if output.ObjectKey != "" {
				keys = append(keys, output.ObjectKey)
			}
		}
	}
	return keys, rows.Err()
}

func (r *Repository) SetDeleted(ctx context.Context, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, `UPDATE media_objects SET status = 'deleted' WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

const selectMedia = `
	SELECT id, object_key, original_filename, content_type, size_bytes, status,
	       variants, uploaded_by, created_at, updated_at, owner_id, visibility, COALESCE(sha256,'')
	  FROM media_objects`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanMedia(row rowScanner) (Media, error) {
	var m Media
	var variants []byte
	err := row.Scan(&m.ID, &m.ObjectKey, &m.OriginalFilename, &m.ContentType, &m.SizeBytes,
		&m.Status, &variants, &m.UploadedBy, &m.CreatedAt, &m.UpdatedAt, &m.OwnerID, &m.Visibility, &m.SHA256)
	if errors.Is(err, pgx.ErrNoRows) {
		return Media{}, ErrNotFound
	}
	if err != nil {
		return Media{}, err
	}
	m.Variants = make(map[string]string)
	if len(variants) > 0 {
		_ = json.Unmarshal(variants, &m.Variants)
	}
	return m, nil
}
