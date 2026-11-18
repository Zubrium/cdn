package postgres

import (
	"context"
	"errors"

	"github.com/Zubrium/cdn/core/domain/access"
	"github.com/Zubrium/cdn/core/domain/files"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// FileIndexRepository adapts searchable file sets and listings to Core's
// application port. Membership and access policy remain enforced by the
// underlying transactional SQL operations.
type FileIndexRepository struct{ Pool *pgxpool.Pool }

func (r FileIndexRepository) ListVisible(ctx context.Context, actor access.Principal, group, cursor string, limit int) ([]files.Object, string, error) {
	return (Objects{Pool: r.Pool}).ListVisible(ctx, actor, group, cursor, limit)
}

func (r FileIndexRepository) ListSet(ctx context.Context, actor access.Principal, setID, cursor string, limit int) ([]files.Object, string, error) {
	return (Objects{Pool: r.Pool}).ListFileSet(ctx, actor, setID, cursor, limit)
}

func (r FileIndexRepository) Search(ctx context.Context, actor access.Principal, query, group, cursor, setID string, limit int) ([]files.Object, string, error) {
	return (Objects{Pool: r.Pool}).SearchFiles(ctx, actor, query, group, cursor, setID, limit)
}

func (r FileIndexRepository) CreateSet(ctx context.Context, actor access.Principal, namespace, name, managementGroup string) (files.FileSet, error) {
	set, err := CreateManagedFileSet(ctx, r.Pool, actor, namespace, name, managementGroup)
	return coreFileSet(set), err
}

func (r FileIndexRepository) GetSet(ctx context.Context, actor access.Principal, value string) (files.FileSet, error) {
	id, err := parseFileIndexID(value)
	if err != nil {
		return files.FileSet{}, err
	}
	set, err := GetFileSet(ctx, r.Pool, actor, id)
	return coreFileSet(set), err
}

func (r FileIndexRepository) UpdateSet(ctx context.Context, actor access.Principal, value string, expected int64, name string) (files.FileSet, error) {
	id, err := parseFileIndexID(value)
	if err != nil {
		return files.FileSet{}, err
	}
	set, err := UpdateFileSet(ctx, r.Pool, actor, id, expected, name)
	return coreFileSet(set), err
}

func (r FileIndexRepository) DeleteSet(ctx context.Context, actor access.Principal, value string, expected int64) error {
	id, err := parseFileIndexID(value)
	if err != nil {
		return err
	}
	return DeleteManagedFileSet(ctx, r.Pool, actor, id, expected)
}

func (r FileIndexRepository) AddToSet(ctx context.Context, actor access.Principal, setValue, fileValue string, expected int64) (files.FileSet, error) {
	setID, err := parseFileIndexID(setValue)
	if err != nil {
		return files.FileSet{}, err
	}
	fileID, err := parseFileIndexID(fileValue)
	if err != nil {
		return files.FileSet{}, err
	}
	set, err := AddFileToManagedSet(ctx, r.Pool, actor, setID, fileID, expected)
	return coreFileSet(set), err
}

func (r FileIndexRepository) RemoveFromSet(ctx context.Context, actor access.Principal, setValue, fileValue string, expected int64) (files.FileSet, error) {
	setID, err := parseFileIndexID(setValue)
	if err != nil {
		return files.FileSet{}, err
	}
	fileID, err := parseFileIndexID(fileValue)
	if err != nil {
		return files.FileSet{}, err
	}
	set, err := RemoveFileFromManagedSet(ctx, r.Pool, actor, setID, fileID, expected)
	return coreFileSet(set), err
}

func (r FileIndexRepository) ListSets(ctx context.Context, actor access.Principal, namespace, query, cursor string, limit int) ([]files.FileSet, string, error) {
	sets, next, err := ListManagedFileSets(ctx, r.Pool, actor, namespace, query, cursor, limit)
	if err != nil {
		return nil, "", err
	}
	result := make([]files.FileSet, 0, len(sets))
	for _, set := range sets {
		result = append(result, coreFileSet(set))
	}
	return result, next, nil
}

func parseFileIndexID(value string) (uuid.UUID, error) {
	id, err := uuid.Parse(value)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, errors.New("invalid file index identifier")
	}
	return id, nil
}

func coreFileSet(set FileSet) files.FileSet {
	return files.FileSet{ID: set.ID, Namespace: set.Namespace, Name: set.Name, ManagementGroup: set.ManagementGroup,
		Revision: set.Revision, CreatedAt: set.CreatedAt, UpdatedAt: set.UpdatedAt}
}

var _ files.FileIndexRepository = FileIndexRepository{}
