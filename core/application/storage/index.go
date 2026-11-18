package storage

import (
	"context"

	"github.com/Zubrium/cdn/core/domain/access"
	"github.com/Zubrium/cdn/core/domain/files"
)

type FileSet = files.FileSet
type FileIndexRepository = files.FileIndexRepository

// IndexService is the application boundary for searching files and managing
// non-security file sets. The repository applies current access-group policy
// atomically with each persistent operation.
type IndexService struct{ repository FileIndexRepository }

func NewIndexService(repository FileIndexRepository) IndexService {
	return IndexService{repository: repository}
}

func (s IndexService) ListVisible(ctx context.Context, actor access.Principal, group, cursor string, limit int) ([]Object, string, error) {
	if s.repository == nil {
		return nil, "", ErrUnavailable
	}
	return s.repository.ListVisible(ctx, actor, group, cursor, limit)
}

func (s IndexService) ListSet(ctx context.Context, actor access.Principal, setID, cursor string, limit int) ([]Object, string, error) {
	if s.repository == nil {
		return nil, "", ErrUnavailable
	}
	return s.repository.ListSet(ctx, actor, setID, cursor, limit)
}

func (s IndexService) Search(ctx context.Context, actor access.Principal, query, group, cursor, setID string, limit int) ([]Object, string, error) {
	if s.repository == nil {
		return nil, "", ErrUnavailable
	}
	return s.repository.Search(ctx, actor, query, group, cursor, setID, limit)
}

func (s IndexService) CreateSet(ctx context.Context, actor access.Principal, namespace, name, managementGroup string) (FileSet, error) {
	if s.repository == nil {
		return FileSet{}, ErrUnavailable
	}
	return s.repository.CreateSet(ctx, actor, namespace, name, managementGroup)
}

func (s IndexService) GetSet(ctx context.Context, actor access.Principal, setID string) (FileSet, error) {
	if s.repository == nil {
		return FileSet{}, ErrUnavailable
	}
	return s.repository.GetSet(ctx, actor, setID)
}

func (s IndexService) UpdateSet(ctx context.Context, actor access.Principal, setID string, expected int64, name string) (FileSet, error) {
	if s.repository == nil {
		return FileSet{}, ErrUnavailable
	}
	return s.repository.UpdateSet(ctx, actor, setID, expected, name)
}

func (s IndexService) DeleteSet(ctx context.Context, actor access.Principal, setID string, expected int64) error {
	if s.repository == nil {
		return ErrUnavailable
	}
	return s.repository.DeleteSet(ctx, actor, setID, expected)
}

func (s IndexService) AddToSet(ctx context.Context, actor access.Principal, setID, fileID string, expected int64) (FileSet, error) {
	if s.repository == nil {
		return FileSet{}, ErrUnavailable
	}
	return s.repository.AddToSet(ctx, actor, setID, fileID, expected)
}

func (s IndexService) RemoveFromSet(ctx context.Context, actor access.Principal, setID, fileID string, expected int64) (FileSet, error) {
	if s.repository == nil {
		return FileSet{}, ErrUnavailable
	}
	return s.repository.RemoveFromSet(ctx, actor, setID, fileID, expected)
}

func (s IndexService) ListSets(ctx context.Context, actor access.Principal, namespace, query, cursor string, limit int) ([]FileSet, string, error) {
	if s.repository == nil {
		return nil, "", ErrUnavailable
	}
	return s.repository.ListSets(ctx, actor, namespace, query, cursor, limit)
}
