package postgres

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Zubrium/cdn/core/access"
	"github.com/Zubrium/cdn/core/internal/storage"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var namespacePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

type FileSet struct {
	ID              string
	Namespace       string
	Name            string
	ManagementGroup string
	Revision        int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func CreateManagedFileSet(ctx context.Context, pool *pgxpool.Pool, actor access.Principal, namespace, name, managementGroup string) (FileSet, error) {
	if pool == nil || !actor.Active || !namespacePattern.MatchString(namespace) || !storage.ValidName(name) || !storage.ValidGroupKey(managementGroup) {
		return FileSet{}, errors.New("invalid file set")
	}
	actorID, err := parseActorID(actor.ID)
	if err != nil {
		return FileSet{}, err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return FileSet{}, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := requireGroupWrite(ctx, tx, actorID, managementGroup); err != nil {
		return FileSet{}, err
	}
	id, err := uuid.NewRandom()
	if err != nil {
		return FileSet{}, err
	}
	var createdAt, updatedAt time.Time
	err = tx.QueryRow(ctx, `INSERT INTO core_file_sets(id,created_by,namespace,name,management_group)
		VALUES($1,$2,$3,$4,$5) RETURNING created_at,updated_at`, id, actorID, namespace, name, managementGroup).
		Scan(&createdAt, &updatedAt)
	if err != nil {
		return FileSet{}, mapFileSetUniqueConflict(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return FileSet{}, err
	}
	return FileSet{ID: id.String(), Namespace: namespace, Name: name, ManagementGroup: managementGroup, Revision: 1, CreatedAt: createdAt, UpdatedAt: updatedAt}, nil
}

func mapFileSetUniqueConflict(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "core_file_sets_namespace_name_key" {
		return storage.ErrFileSetConflict
	}
	return err
}

func GetFileSet(ctx context.Context, pool *pgxpool.Pool, actor access.Principal, setID uuid.UUID) (FileSet, error) {
	if pool == nil || !actor.Active || setID == uuid.Nil {
		return FileSet{}, errors.New("invalid file set")
	}
	actorID, err := parseActorID(actor.ID)
	if err != nil {
		return FileSet{}, err
	}
	var set FileSet
	err = pool.QueryRow(ctx, `SELECT s.id::text,s.namespace,s.name,s.management_group,s.revision,s.created_at,s.updated_at
		FROM core_file_sets s WHERE s.id=$1 AND EXISTS(SELECT 1 FROM core_principals p WHERE p.id=$2 AND p.active)
		AND EXISTS(SELECT 1 FROM core_access_groups g JOIN core_group_access ga ON ga.group_key=g.group_key
			WHERE g.group_key=s.management_group AND g.active AND ga.principal_id=$2 AND ga.access_level>=1)`, setID, actorID).
		Scan(&set.ID, &set.Namespace, &set.Name, &set.ManagementGroup, &set.Revision, &set.CreatedAt, &set.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return FileSet{}, storage.ErrNotFound
	}
	return set, err
}

func UpdateFileSet(ctx context.Context, pool *pgxpool.Pool, actor access.Principal, setID uuid.UUID, expected int64, name string) (FileSet, error) {
	if pool == nil || !actor.Active || setID == uuid.Nil || expected <= 0 || !storage.ValidName(name) {
		return FileSet{}, errors.New("invalid file set update")
	}
	actorID, err := parseActorID(actor.ID)
	if err != nil {
		return FileSet{}, err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return FileSet{}, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	set, err := lockManagedSet(ctx, tx, actorID, setID)
	if err != nil {
		return FileSet{}, err
	}
	if set.Revision != expected {
		return FileSet{}, storage.ErrRevisionConflict
	}
	if err := requireGroupWrite(ctx, tx, actorID, set.ManagementGroup); err != nil {
		return FileSet{}, err
	}
	var revision int64
	var updatedAt time.Time
	err = tx.QueryRow(ctx, `UPDATE core_file_sets SET name=$3,revision=revision+1,updated_at=now() WHERE id=$1 AND revision=$2 RETURNING revision,updated_at`, setID, expected, name).Scan(&revision, &updatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return FileSet{}, storage.ErrRevisionConflict
	}
	if err != nil {
		return FileSet{}, mapFileSetUniqueConflict(err)
	}
	set.Name, set.Revision, set.UpdatedAt = name, revision, updatedAt
	if err := tx.Commit(ctx); err != nil {
		return FileSet{}, err
	}
	return set, nil
}

func DeleteManagedFileSet(ctx context.Context, pool *pgxpool.Pool, actor access.Principal, setID uuid.UUID, expected int64) error {
	if pool == nil || !actor.Active || setID == uuid.Nil || expected <= 0 {
		return errors.New("invalid file set delete")
	}
	actorID, err := parseActorID(actor.ID)
	if err != nil {
		return err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	set, err := lockManagedSet(ctx, tx, actorID, setID)
	if err != nil {
		return err
	}
	if set.Revision != expected {
		return storage.ErrRevisionConflict
	}
	if err := requireGroupWrite(ctx, tx, actorID, set.ManagementGroup); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM core_file_sets WHERE id=$1 AND revision=$2`, setID, expected)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return storage.ErrRevisionConflict
	}
	return tx.Commit(ctx)
}

func AddFileToManagedSet(ctx context.Context, pool *pgxpool.Pool, actor access.Principal, setID, objectID uuid.UUID, expected int64) (FileSet, error) {
	return changeManagedSetMember(ctx, pool, actor, setID, objectID, expected, true)
}

func RemoveFileFromManagedSet(ctx context.Context, pool *pgxpool.Pool, actor access.Principal, setID, objectID uuid.UUID, expected int64) (FileSet, error) {
	return changeManagedSetMember(ctx, pool, actor, setID, objectID, expected, false)
}

func changeManagedSetMember(ctx context.Context, pool *pgxpool.Pool, actor access.Principal, setID, objectID uuid.UUID, expected int64, add bool) (FileSet, error) {
	if pool == nil || !actor.Active || setID == uuid.Nil || objectID == uuid.Nil || expected <= 0 {
		return FileSet{}, errors.New("invalid file set membership")
	}
	actorID, err := parseActorID(actor.ID)
	if err != nil {
		return FileSet{}, err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return FileSet{}, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	set, err := lockManagedSet(ctx, tx, actorID, setID)
	if err != nil {
		return FileSet{}, err
	}
	if set.Revision != expected {
		return FileSet{}, storage.ErrRevisionConflict
	}
	if err := requireGroupWrite(ctx, tx, actorID, set.ManagementGroup); err != nil {
		return FileSet{}, err
	}
	changed := false
	if add {
		if err := requireObjectWrite(ctx, tx, actorID, objectID); err != nil {
			return FileSet{}, err
		}
		tag, err := tx.Exec(ctx, `INSERT INTO core_file_set_members(file_set_id,object_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, setID, objectID)
		if err != nil {
			return FileSet{}, err
		}
		changed = tag.RowsAffected() == 1
	} else {
		tag, err := tx.Exec(ctx, `DELETE FROM core_file_set_members WHERE file_set_id=$1 AND object_id=$2`, setID, objectID)
		if err != nil {
			return FileSet{}, err
		}
		changed = tag.RowsAffected() == 1
	}
	if !changed {
		if err := tx.Commit(ctx); err != nil {
			return FileSet{}, err
		}
		return set, nil
	}
	var revision int64
	var updatedAt time.Time
	err = tx.QueryRow(ctx, `UPDATE core_file_sets SET revision=revision+1,updated_at=now() WHERE id=$1 AND revision=$2 RETURNING revision,updated_at`, setID, expected).Scan(&revision, &updatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return FileSet{}, storage.ErrRevisionConflict
	}
	if err != nil {
		return FileSet{}, err
	}
	set.Revision, set.UpdatedAt = revision, updatedAt
	if err := tx.Commit(ctx); err != nil {
		return FileSet{}, err
	}
	return set, nil
}

func ListManagedFileSets(ctx context.Context, pool *pgxpool.Pool, actor access.Principal, namespace, query, cursor string, limit int) ([]FileSet, string, error) {
	if pool == nil {
		return nil, "", errors.New("postgres: nil pool")
	}
	if !actor.Active || limit < 1 || limit > 1000 || len(query) > 255 || !utf8.ValidString(query) || strings.ContainsRune(query, '\x00') || (namespace != "" && !namespacePattern.MatchString(namespace)) {
		return nil, "", storage.ErrInvalidSearch
	}
	actorID, err := parseActorID(actor.ID)
	if err != nil {
		return nil, "", err
	}
	var cursorID uuid.UUID
	if cursor != "" {
		cursorID, err = uuid.Parse(cursor)
		if err != nil || cursorID == uuid.Nil {
			return nil, "", storage.ErrInvalidSearch
		}
	}
	rows, err := pool.Query(ctx, `SELECT s.id::text,s.namespace,s.name,s.management_group,s.revision,s.created_at,s.updated_at
		FROM core_file_sets s WHERE EXISTS(SELECT 1 FROM core_principals p WHERE p.id=$1 AND p.active)
		AND ($2='' OR s.namespace=$2) AND ($3='' OR strpos(lower(s.name),lower($3))>0)
		AND ($4::uuid IS NULL OR s.id>$4)
		AND EXISTS(SELECT 1 FROM core_access_groups g JOIN core_group_access ga ON ga.group_key=g.group_key
			WHERE g.group_key=s.management_group AND g.active AND ga.principal_id=$1 AND ga.access_level>=1)
		ORDER BY s.id LIMIT $5`, actorID, namespace, query, nullableUUID(cursor, cursorID), limit+1)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	sets := make([]FileSet, 0, limit+1)
	for rows.Next() {
		var set FileSet
		if err := rows.Scan(&set.ID, &set.Namespace, &set.Name, &set.ManagementGroup, &set.Revision, &set.CreatedAt, &set.UpdatedAt); err != nil {
			return nil, "", err
		}
		sets = append(sets, set)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	if len(sets) > limit {
		sets = sets[:limit]
		return sets, sets[len(sets)-1].ID, nil
	}
	return sets, "", nil
}

func lockManagedSet(ctx context.Context, tx pgx.Tx, actorID, setID uuid.UUID) (FileSet, error) {
	var set FileSet
	err := tx.QueryRow(ctx, `SELECT s.id::text,s.namespace,s.name,s.management_group,s.revision,s.created_at,s.updated_at
		FROM core_file_sets s WHERE s.id=$1
		AND EXISTS(SELECT 1 FROM core_principals p WHERE p.id=$2 AND p.active)
		AND EXISTS(SELECT 1 FROM core_access_groups g JOIN core_group_access ga ON ga.group_key=g.group_key
			WHERE g.group_key=s.management_group AND g.active AND ga.principal_id=$2 AND ga.access_level>=1)
		FOR UPDATE OF s`, setID, actorID).
		Scan(&set.ID, &set.Namespace, &set.Name, &set.ManagementGroup, &set.Revision, &set.CreatedAt, &set.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return FileSet{}, storage.ErrNotFound
	}
	return set, err
}

func parseActorID(value string) (uuid.UUID, error) {
	id, err := uuid.Parse(value)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, ErrUnauthorized
	}
	return id, nil
}
