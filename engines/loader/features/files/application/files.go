package application

import (
	"context"
	"io"

	core "github.com/Zubrium/cdn/core/platform"
	api "github.com/Zubrium/cdn/engines/loader/presentation"
)

func (s *Service) ListFiles(ctx context.Context, query api.ListQuery) (api.FilePage, error) {
	request, err := s.request(ctx)
	if err != nil {
		return api.FilePage{}, err
	}
	files, next, err := s.files.SearchFiles(ctx, request.session, query.Query, query.Group, query.Cursor, "", query.Limit)
	if err != nil {
		return api.FilePage{}, mapCoreError(err)
	}
	return mapFilePage(files, next), nil
}

func (s *Service) StatFile(ctx context.Context, id string) (api.File, error) {
	request, err := s.request(ctx)
	if err != nil {
		return api.File{}, err
	}
	file, err := s.files.StatFile(ctx, request.session, id)
	if err != nil {
		return api.File{}, mapCoreError(err)
	}
	return exposeFile(file), nil
}

func (s *Service) ReadFile(ctx context.Context, id string) (io.ReadCloser, api.File, error) {
	request, err := s.request(ctx)
	if err != nil {
		return nil, api.File{}, err
	}
	body, file, err := s.files.ReadFile(ctx, request.session, id)
	if err != nil {
		return nil, api.File{}, mapCoreError(err)
	}
	return body, exposeFile(file), nil
}

func (s *Service) ReadFileRange(ctx context.Context, id string, start, end int64) (io.ReadCloser, api.File, error) {
	request, err := s.request(ctx)
	if err != nil {
		return nil, api.File{}, err
	}
	body, file, err := s.files.ReadFileRange(ctx, request.session, id, start, end)
	if err != nil {
		return nil, api.File{}, mapCoreError(err)
	}
	return body, exposeFile(file), nil
}

func (s *Service) UpdateFile(ctx context.Context, id string, revision int64, patch api.FileMetadataPatch) (api.File, error) {
	request, err := s.request(ctx)
	if err != nil {
		return api.File{}, err
	}
	current, err := s.files.StatFile(ctx, request.session, id)
	if err != nil {
		return api.File{}, mapCoreError(err)
	}
	if current.Revision != revision {
		return api.File{}, api.ErrConflict
	}
	metadata := clone(current.Metadata)
	if metadata == nil {
		metadata = map[string]string{}
	}
	if patch.Name != nil {
		metadata["name"] = *patch.Name
	}
	if patch.Attributes != nil {
		name := metadata["name"]
		metadata = clone(*patch.Attributes)
		if metadata == nil {
			metadata = map[string]string{}
		}
		metadata["name"] = name
	}
	contentType := current.ContentType
	if patch.ContentType != nil {
		contentType = *patch.ContentType
	}
	if err := s.files.UpdateFileWithContentType(ctx, request.session, id, revision, metadata, contentType); err != nil {
		return api.File{}, mapCoreError(err)
	}
	updated, err := s.files.StatFile(ctx, request.session, id)
	if err != nil {
		return api.File{}, mapCoreError(err)
	}
	return exposeFile(updated), nil
}

func (s *Service) DeleteFile(ctx context.Context, id string, revision int64) error {
	request, err := s.request(ctx)
	if err != nil {
		return err
	}
	return mapCoreError(s.files.DeleteFile(ctx, request.session, id, revision))
}

func (s *Service) AddFileToGroup(ctx context.Context, id, group string, revision int64) (api.File, error) {
	return s.changeFileGroup(ctx, id, group, revision, true)
}

func (s *Service) RemoveFileFromGroup(ctx context.Context, id, group string, revision int64) (api.File, error) {
	return s.changeFileGroup(ctx, id, group, revision, false)
}

func (s *Service) changeFileGroup(ctx context.Context, id, group string, revision int64, add bool) (api.File, error) {
	request, err := s.request(ctx)
	if err != nil {
		return api.File{}, err
	}
	if add {
		err = s.files.AddFileToGroup(ctx, request.session, id, group, revision)
	} else {
		err = s.files.RemoveFileFromGroup(ctx, request.session, id, group, revision)
	}
	if err != nil {
		return api.File{}, mapCoreError(err)
	}
	file, err := s.files.StatFile(ctx, request.session, id)
	if err != nil {
		return api.File{}, mapCoreError(err)
	}
	return exposeFile(file), nil
}

func (s *Service) ListFileSets(ctx context.Context, query api.SetListQuery) (api.FileSetPage, error) {
	request, err := s.request(ctx)
	if err != nil {
		return api.FileSetPage{}, err
	}
	sets, next, err := s.files.ListManagedFileSets(ctx, request.session, query.Namespace, query.Query, query.Cursor, query.Limit)
	if err != nil {
		return api.FileSetPage{}, mapCoreError(err)
	}
	page := api.FileSetPage{Items: make([]api.FileSet, 0, len(sets)), NextCursor: next}
	for _, set := range sets {
		page.Items = append(page.Items, exposeSet(set))
	}
	return page, nil
}

func (s *Service) CreateFileSet(ctx context.Context, input api.CreateFileSetInput) (api.FileSet, error) {
	request, err := s.request(ctx)
	if err != nil {
		return api.FileSet{}, err
	}
	set, err := s.files.CreateManagedFileSet(ctx, request.session, input.Namespace, input.Name, input.ManagementGroup)
	if err != nil {
		return api.FileSet{}, mapCoreError(err)
	}
	return exposeSet(set), nil
}

func (s *Service) GetFileSet(ctx context.Context, id string) (api.FileSet, error) {
	request, err := s.request(ctx)
	if err != nil {
		return api.FileSet{}, err
	}
	set, err := s.files.GetManagedFileSet(ctx, request.session, id)
	if err != nil {
		return api.FileSet{}, mapCoreError(err)
	}
	return exposeSet(set), nil
}

func (s *Service) UpdateFileSet(ctx context.Context, id string, revision int64, patch api.FileSetMetadataPatch) (api.FileSet, error) {
	request, err := s.request(ctx)
	if err != nil {
		return api.FileSet{}, err
	}
	if patch.Name == nil {
		return api.FileSet{}, api.ErrInvalid
	}
	set, err := s.files.UpdateManagedFileSet(ctx, request.session, id, revision, *patch.Name)
	if err != nil {
		return api.FileSet{}, mapCoreError(err)
	}
	return exposeSet(set), nil
}

func (s *Service) DeleteFileSet(ctx context.Context, id string, revision int64) error {
	request, err := s.request(ctx)
	if err != nil {
		return err
	}
	return mapCoreError(s.files.DeleteManagedFileSet(ctx, request.session, id, revision))
}

func (s *Service) ListSetFiles(ctx context.Context, setID string, query api.ListQuery) (api.FilePage, error) {
	request, err := s.request(ctx)
	if err != nil {
		return api.FilePage{}, err
	}
	if _, err := s.files.GetManagedFileSet(ctx, request.session, setID); err != nil {
		return api.FilePage{}, mapCoreError(err)
	}
	var files []core.File
	var next string
	if query.Query == "" && query.Group == "" {
		files, next, err = s.files.ListFileSet(ctx, request.session, setID, query.Cursor, query.Limit)
	} else {
		files, next, err = s.files.SearchFiles(ctx, request.session, query.Query, query.Group, query.Cursor, setID, query.Limit)
	}
	if err != nil {
		return api.FilePage{}, mapCoreError(err)
	}
	return mapFilePage(files, next), nil
}

func (s *Service) AddFileToSet(ctx context.Context, setID, fileID string, revision int64) (api.FileSet, error) {
	return s.changeSetMember(ctx, setID, fileID, revision, true)
}

func (s *Service) RemoveFileFromSet(ctx context.Context, setID, fileID string, revision int64) (api.FileSet, error) {
	return s.changeSetMember(ctx, setID, fileID, revision, false)
}

func (s *Service) changeSetMember(ctx context.Context, setID, fileID string, revision int64, add bool) (api.FileSet, error) {
	request, err := s.request(ctx)
	if err != nil {
		return api.FileSet{}, err
	}
	var set core.FileSet
	if add {
		set, err = s.files.AddFileToManagedSet(ctx, request.session, setID, fileID, revision)
	} else {
		set, err = s.files.RemoveFileFromManagedSet(ctx, request.session, setID, fileID, revision)
	}
	if err != nil {
		return api.FileSet{}, mapCoreError(err)
	}
	return exposeSet(set), nil
}

func exposeSet(set core.FileSet) api.FileSet {
	return api.FileSet{ID: set.ID, Namespace: set.Namespace, Name: set.Name, ManagementGroup: set.ManagementGroup, Revision: set.Revision, CreatedAt: set.CreatedAt, UpdatedAt: set.UpdatedAt}
}

func mapFilePage(files []core.File, next string) api.FilePage {
	page := api.FilePage{Items: make([]api.File, 0, len(files)), NextCursor: next}
	for _, file := range files {
		page.Items = append(page.Items, exposeFile(file))
	}
	return page
}
