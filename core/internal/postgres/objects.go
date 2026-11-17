package postgres

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/Zubrium/cdn/core/access"
	"github.com/Zubrium/cdn/core/internal/storage"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Objects struct{ Pool *pgxpool.Pool }

func (r Objects) CurrentPrincipal(ctx context.Context, value string) (access.Principal, error) {
	if r.Pool == nil {
		return access.Principal{}, errors.New("postgres: nil pool")
	}
	id, err := uuid.Parse(value)
	if err != nil || id == uuid.Nil {
		return access.Principal{}, ErrUnauthorized
	}
	var principal access.Principal
	err = r.Pool.QueryRow(ctx, `SELECT id::text,name,active,can_manage_administrators FROM core_principals WHERE id=$1`, id).
		Scan(&principal.ID, &principal.Name, &principal.Active, &principal.CanManageAdministrators)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !principal.Active) {
		return access.Principal{}, ErrUnauthorized
	}
	return principal, err
}

func (r Objects) Assignments(ctx context.Context, id string) ([]access.GroupAssignment, error) {
	return Assignments(ctx, r.Pool, id)
}

// Authorize validates the current direct group access and returns the exact
// object revision observed by the same PostgreSQL statement. Metadata cache
// entries are usable only when their revision matches this authorization view.
func (r Objects) Authorize(ctx context.Context, principalValue, objectValue string, action access.Action) (int64, []string, error) {
	if r.Pool == nil {
		return 0, nil, errors.New("postgres: nil pool")
	}
	principalID, principalErr := uuid.Parse(principalValue)
	objectID, objectErr := uuid.Parse(objectValue)
	if principalErr != nil || principalID == uuid.Nil || objectErr != nil || objectID == uuid.Nil || !access.ValidAction(action) {
		return 0, nil, ErrUnauthorized
	}
	minimum := int16(access.LevelWrite)
	if action == access.Read {
		minimum = int16(access.LevelRead)
	}
	var revision int64
	var visibleGroups []string
	var active, allowed bool
	err := r.Pool.QueryRow(ctx, `SELECT o.revision,p.active,EXISTS(
		SELECT 1 FROM core_file_groups fg JOIN core_access_groups g ON g.group_key=fg.group_key AND g.active
		JOIN core_group_access ga ON ga.group_key=fg.group_key AND ga.principal_id=p.id AND ga.access_level >= $3
		WHERE fg.object_id=o.id),ARRAY(
		SELECT DISTINCT vfg.group_key FROM core_file_groups vfg
		JOIN core_access_groups vg ON vg.group_key=vfg.group_key AND vg.active
		JOIN core_group_access vga ON vga.group_key=vfg.group_key AND vga.principal_id=p.id AND vga.access_level >= $4
		WHERE vfg.object_id=o.id ORDER BY vfg.group_key)
		FROM core_objects o CROSS JOIN core_principals p
		WHERE o.id=$1 AND o.state='ready' AND p.id=$2`, objectID, principalID, minimum, int16(access.LevelRead)).
		Scan(&revision, &active, &allowed, &visibleGroups)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil, storage.ErrNotFound
	}
	if err != nil {
		return 0, nil, err
	}
	if !active {
		return 0, nil, ErrUnauthorized
	}
	if !allowed {
		return 0, nil, ErrForbidden
	}
	return revision, visibleGroups, nil
}

func (r Objects) Find(ctx context.Context, value string) (storage.Object, error) {
	if r.Pool == nil {
		return storage.Object{}, errors.New("postgres: nil pool")
	}
	id, err := uuid.Parse(value)
	if err != nil || id == uuid.Nil {
		return storage.Object{}, storage.ErrNotFound
	}
	var object storage.Object
	var rawMetadata, checksum []byte
	err = r.Pool.QueryRow(ctx, `SELECT o.id::text,o.object_key::text,o.metadata,o.content_type,o.size_bytes,o.sha256,o.revision,o.state,
		COALESCE(array_agg(fg.group_key ORDER BY fg.group_key) FILTER (WHERE fg.group_key IS NOT NULL),ARRAY[]::text[])
		FROM core_objects o LEFT JOIN core_file_groups fg ON fg.object_id=o.id
		WHERE o.id=$1 AND o.state='ready' GROUP BY o.id`, id).
		Scan(&object.ID, &object.Key, &rawMetadata, &object.ContentType, &object.Size, &checksum, &object.Revision, &object.State, &object.Groups)
	if errors.Is(err, pgx.ErrNoRows) {
		return storage.Object{}, storage.ErrNotFound
	}
	if err != nil {
		return storage.Object{}, err
	}
	if err := json.Unmarshal(rawMetadata, &object.Metadata); err != nil {
		return storage.Object{}, fmt.Errorf("decode file metadata: %w", err)
	}
	if object.Metadata == nil {
		object.Metadata = map[string]string{}
	}
	object.SHA256 = hex.EncodeToString(checksum)
	object.Ready = true
	return object, nil
}

type ObjectWriter interface {
	Put(context.Context, string, io.Reader, int64, string) error
	Delete(context.Context, string) error
	Read(context.Context, string) (io.ReadCloser, error)
	ReadRange(context.Context, string, int64, int64) (io.ReadCloser, error)
}

type Creator struct {
	Pool             *pgxpool.Pool
	Store            ObjectWriter
	MaxMetadataBytes int64
}

func (c Creator) Create(ctx context.Context, actor access.Principal, requestKey string, groups []string, metadata map[string]string, contentType string, size int64, body io.Reader) (string, error) {
	id, err := UploadNew(ctx, c.Pool, c.Store, actor, requestKey, groups, metadata, c.MaxMetadataBytes, contentType, size, "", body)
	if err != nil {
		return "", err
	}
	return id.String(), nil
}

func (c Creator) CreateVerified(ctx context.Context, actor access.Principal, requestKey string, groups []string, metadata map[string]string, contentType string, size int64, expectedSHA256 string, body io.Reader) (string, error) {
	id, err := UploadNew(ctx, c.Pool, c.Store, actor, requestKey, groups, metadata, c.MaxMetadataBytes, contentType, size, expectedSHA256, body)
	if err != nil {
		return "", err
	}
	return id.String(), nil
}

func (c Creator) Update(ctx context.Context, actor access.Principal, objectID string, expected int64, metadata map[string]string) error {
	return c.UpdateWithContentType(ctx, actor, objectID, expected, metadata, "")
}

func (c Creator) UpdateWithContentType(ctx context.Context, actor access.Principal, objectID string, expected int64, metadata map[string]string, contentType string) error {
	id, actorID, err := c.validActorAndObject(actor, objectID, expected)
	if err != nil {
		return err
	}
	if contentType != "" && !storage.ValidContentType(contentType) {
		return errors.New("invalid content type")
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	tx, err := c.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := requireObjectWrite(ctx, tx, actorID, id); err != nil {
		return err
	}
	var tag pgconn.CommandTag
	if contentType == "" {
		tag, err = tx.Exec(ctx, `UPDATE core_objects SET metadata=$3::jsonb,revision=revision+1,updated_at=now()
			WHERE id=$1 AND revision=$2 AND state='ready'`, id, expected, string(encoded))
	} else {
		tag, err = tx.Exec(ctx, `UPDATE core_objects SET metadata=$3::jsonb,content_type=$4,revision=revision+1,updated_at=now()
			WHERE id=$1 AND revision=$2 AND state='ready'`, id, expected, string(encoded), contentType)
	}
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return revisionOrMissing(ctx, tx, id)
	}
	if _, err := tx.Exec(ctx, `SELECT pg_notify('core_cache_invalidation',$1)`, id.String()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (c Creator) AddToGroup(ctx context.Context, actor access.Principal, objectID, group string, expected int64) error {
	return c.changeGroup(ctx, actor, objectID, group, expected, true)
}

func (c Creator) RemoveFromGroup(ctx context.Context, actor access.Principal, objectID, group string, expected int64) error {
	return c.changeGroup(ctx, actor, objectID, group, expected, false)
}

func (c Creator) changeGroup(ctx context.Context, actor access.Principal, objectValue, group string, expected int64, add bool) error {
	id, actorID, err := c.validActorAndObject(actor, objectValue, expected)
	if err != nil || !storage.ValidGroupKey(group) {
		return errors.New("invalid file group change")
	}
	tx, err := c.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := requireObjectWrite(ctx, tx, actorID, id); err != nil {
		return err
	}
	if err := requireGroupWrite(ctx, tx, actorID, group); err != nil {
		return err
	}
	if add {
		if _, err := tx.Exec(ctx, `INSERT INTO core_file_groups(object_id,group_key) VALUES($1,$2) ON CONFLICT DO NOTHING`, id, group); err != nil {
			return err
		}
	} else {
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM core_file_groups WHERE object_id=$1`, id).Scan(&count); err != nil {
			return err
		}
		if count <= 1 {
			return errors.New("file must remain in at least one access group")
		}
		if _, err := tx.Exec(ctx, `DELETE FROM core_file_groups WHERE object_id=$1 AND group_key=$2`, id, group); err != nil {
			return err
		}
	}
	tag, err := tx.Exec(ctx, `UPDATE core_objects SET revision=revision+1,updated_at=now() WHERE id=$1 AND revision=$2 AND state='ready'`, id, expected)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return revisionOrMissing(ctx, tx, id)
	}
	if _, err := tx.Exec(ctx, `SELECT pg_notify('core_cache_invalidation',$1)`, id.String()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (c Creator) Delete(ctx context.Context, actor access.Principal, objectValue string, expected int64) error {
	id, actorID, err := c.validActorAndObject(actor, objectValue, expected)
	if err != nil {
		return err
	}
	tx, err := c.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := requireObjectWrite(ctx, tx, actorID, id); err != nil {
		return err
	}
	var key string
	err = tx.QueryRow(ctx, `UPDATE core_objects SET state='deleting',updated_at=now()
		WHERE id=$1 AND revision=$2 AND state IN ('ready','deleting') RETURNING object_key::text`, id, expected).Scan(&key)
	if errors.Is(err, pgx.ErrNoRows) {
		return revisionOrMissing(ctx, tx, id)
	}
	if err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	if err := c.Store.Delete(ctx, key); err != nil {
		return fmt.Errorf("delete stored file: %w", err)
	}
	return c.finalizeDelete(ctx, id)
}

func (c Creator) finalizeDelete(ctx context.Context, id uuid.UUID) error {
	tx, err := c.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(ctx, `UPDATE core_upload_requests SET state='deleted',object_id=NULL,updated_at=now() WHERE object_id=$1`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM core_objects WHERE id=$1 AND state='deleting'`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_notify('core_cache_invalidation',$1)`, id.String()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (c Creator) validActorAndObject(actor access.Principal, objectValue string, expected int64) (uuid.UUID, uuid.UUID, error) {
	if c.Pool == nil || c.Store == nil || !actor.Active || expected < 1 {
		return uuid.Nil, uuid.Nil, ErrUnauthorized
	}
	actorID, actorErr := uuid.Parse(actor.ID)
	objectID, objectErr := uuid.Parse(objectValue)
	if actorErr != nil || actorID == uuid.Nil || objectErr != nil || objectID == uuid.Nil {
		return uuid.Nil, uuid.Nil, errors.New("invalid file operation")
	}
	return objectID, actorID, nil
}

func requireObjectWrite(ctx context.Context, tx pgx.Tx, actorID, objectID uuid.UUID) error {
	var state string
	err := tx.QueryRow(ctx, `SELECT state FROM core_objects WHERE id=$1 FOR UPDATE`, objectID).Scan(&state)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && state != "ready") {
		return storage.ErrNotFound
	}
	if err != nil {
		return err
	}
	if err := requireActivePrincipal(ctx, tx, actorID); err != nil {
		return err
	}
	var group string
	err = tx.QueryRow(ctx, `SELECT fg.group_key FROM core_file_groups fg
		JOIN core_access_groups g ON g.group_key=fg.group_key AND g.active
		JOIN core_group_access ga ON ga.group_key=fg.group_key AND ga.principal_id=$1 AND ga.access_level=2
		WHERE fg.object_id=$2 ORDER BY fg.group_key LIMIT 1 FOR SHARE OF g,ga`, actorID, objectID).Scan(&group)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrForbidden
	}
	return err
}

func requireGroupWrite(ctx context.Context, tx pgx.Tx, principalID uuid.UUID, group string) error {
	if err := requireActivePrincipal(ctx, tx, principalID); err != nil {
		return err
	}
	return requireGroupWriteForActive(ctx, tx, principalID, group)
}

func requireGroupWriteForActive(ctx context.Context, tx pgx.Tx, principalID uuid.UUID, group string) error {
	var active bool
	err := tx.QueryRow(ctx, `SELECT active FROM core_access_groups WHERE group_key=$1 FOR SHARE`, group).Scan(&active)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !active) {
		return ErrForbidden
	}
	if err != nil {
		return err
	}
	var level int16
	err = tx.QueryRow(ctx, `SELECT access_level FROM core_group_access WHERE principal_id=$1 AND group_key=$2 FOR SHARE`, principalID, group).Scan(&level)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && level != int16(access.LevelWrite)) {
		return ErrForbidden
	}
	return err
}

func requireActivePrincipal(ctx context.Context, tx pgx.Tx, principalID uuid.UUID) error {
	var active bool
	err := tx.QueryRow(ctx, `SELECT active FROM core_principals WHERE id=$1 FOR SHARE`, principalID).Scan(&active)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !active) {
		return ErrUnauthorized
	}
	return err
}

func revisionOrMissing(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM core_objects WHERE id=$1 AND state='ready')`, id).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return storage.ErrNotFound
	}
	return storage.ErrRevisionConflict
}
