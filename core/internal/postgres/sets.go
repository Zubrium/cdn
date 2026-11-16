package postgres

import (
	"context"
	"errors"
	"regexp"

	"github.com/Zubrium/cdn/core/access"
	"github.com/Zubrium/cdn/core/internal/storage"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var namespacePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

func CreateFileSet(ctx context.Context, pool *pgxpool.Pool, actor access.Principal, namespace, name string) (uuid.UUID, error) {
	if pool == nil || !actor.Active || !namespacePattern.MatchString(namespace) || !storage.ValidName(name) {
		return uuid.Nil, errors.New("invalid file set")
	}
	actorID, err := uuid.Parse(actor.ID)
	if err != nil || actorID == uuid.Nil {
		return uuid.Nil, ErrUnauthorized
	}
	id, err := uuid.NewRandom()
	if err != nil {
		return uuid.Nil, err
	}
	tag, err := pool.Exec(ctx, `INSERT INTO core_file_sets(id,created_by,namespace,name)
		SELECT $1,id,$3,$4 FROM core_principals WHERE id=$2 AND active`, id, actorID, namespace, name)
	if err != nil {
		return uuid.Nil, err
	}
	if tag.RowsAffected() != 1 {
		return uuid.Nil, ErrUnauthorized
	}
	return id, nil
}

func AddFileToSet(ctx context.Context, pool *pgxpool.Pool, actor access.Principal, setID, objectID uuid.UUID) error {
	return changeFileSetMember(ctx, pool, actor, setID, objectID, true)
}

func RemoveFileFromSet(ctx context.Context, pool *pgxpool.Pool, actor access.Principal, setID, objectID uuid.UUID) error {
	return changeFileSetMember(ctx, pool, actor, setID, objectID, false)
}

func changeFileSetMember(ctx context.Context, pool *pgxpool.Pool, actor access.Principal, setID, objectID uuid.UUID, add bool) error {
	if pool == nil || !actor.Active || setID == uuid.Nil || objectID == uuid.Nil {
		return errors.New("invalid file set membership")
	}
	actorID, err := uuid.Parse(actor.ID)
	if err != nil || actorID == uuid.Nil {
		return ErrUnauthorized
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var ownsSet bool
	err = tx.QueryRow(ctx, `SELECT created_by=$2 AND EXISTS(SELECT 1 FROM core_principals WHERE id=$2 AND active)
		FROM core_file_sets WHERE id=$1 FOR UPDATE`, setID, actorID).Scan(&ownsSet)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !ownsSet) {
		return ErrForbidden
	}
	if err != nil {
		return err
	}
	if add {
		var canRead bool
		err := tx.QueryRow(ctx, `SELECT o.state='ready' AND EXISTS(
			SELECT 1 FROM core_file_groups fg JOIN core_access_groups g ON g.group_key=fg.group_key AND g.active
			JOIN core_group_access ga ON ga.group_key=fg.group_key AND ga.principal_id=$2 AND ga.access_level>=1
			WHERE fg.object_id=o.id) FROM core_objects o WHERE o.id=$1`, objectID, actorID).Scan(&canRead)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && !canRead) {
			return ErrForbidden
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO core_file_set_members(file_set_id,object_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, setID, objectID); err != nil {
			return err
		}
	} else if _, err := tx.Exec(ctx, `DELETE FROM core_file_set_members WHERE file_set_id=$1 AND object_id=$2`, setID, objectID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func DeleteFileSet(ctx context.Context, pool *pgxpool.Pool, actor access.Principal, setID uuid.UUID) error {
	if pool == nil || !actor.Active || setID == uuid.Nil {
		return errors.New("invalid file set")
	}
	actorID, err := uuid.Parse(actor.ID)
	if err != nil || actorID == uuid.Nil {
		return ErrUnauthorized
	}
	tag, err := pool.Exec(ctx, `DELETE FROM core_file_sets WHERE id=$1 AND created_by=$2
		AND EXISTS(SELECT 1 FROM core_principals WHERE id=$2 AND active)`, setID, actorID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrForbidden
	}
	return nil
}
