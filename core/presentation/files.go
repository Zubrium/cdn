package presentation

import (
	"context"
	"io"

	"github.com/Zubrium/cdn/core/application/storage"
)

// PutStagedObject stores opaque temporary bytes before they become a file.
// Core enforces size, current group access, principal binding and checksum;
// the caller owns its upload session and chunk ordering.
func (r *Runtime) PutStagedObject(ctx context.Context, session Session, id string, groups []string, size int64, checksum string, body io.Reader) error {
	actor, err := r.actor(ctx, session)
	if err != nil {
		return err
	}
	return r.staging.Put(ctx, actor, id, groups, size, checksum, body)
}

// ReadStagedObject returns a stream while holding a cleanup lock until Close.
func (r *Runtime) ReadStagedObject(ctx context.Context, session Session, id string) (io.ReadCloser, int64, string, error) {
	actor, err := r.actor(ctx, session)
	if err != nil {
		return nil, 0, "", err
	}
	return r.staging.Read(ctx, actor, id)
}

// DeleteStagedObject permanently removes an opaque temporary object.
func (r *Runtime) DeleteStagedObject(ctx context.Context, session Session, id string) error {
	actor, err := r.actor(ctx, session)
	if err != nil {
		return err
	}
	return r.staging.Delete(ctx, actor, id)
}

var ErrFileNotFound = storage.ErrNotFound
var ErrRevisionConflict = storage.ErrRevisionConflict
var ErrFileSetConflict = storage.ErrFileSetConflict
var ErrUploadInProgress = storage.ErrUploadInProgress
var ErrRequestConflict = storage.ErrRequestConflict
var ErrRequestDeleted = storage.ErrRequestDeleted
var ErrChecksumMismatch = storage.ErrChecksumMismatch
var ErrInvalidSearch = storage.ErrInvalidSearch

type File struct {
	ID          string
	Groups      []string
	Metadata    map[string]string
	ContentType string
	SHA256      string
	Revision    int64
	Size        int64
}

type FileSet = storage.FileSet

type UploadRequestStatus = storage.UploadRequestStatus

func exposeFile(object storage.Object) File {
	return File{
		ID: object.ID, Groups: append([]string(nil), object.Groups...), Metadata: cloneMap(object.Metadata),
		ContentType: object.ContentType, SHA256: object.SHA256, Revision: object.Revision, Size: object.Size,
	}
}

func (r *Runtime) CreateFile(ctx context.Context, session Session, requestKey string, groups []string, metadata map[string]string, contentType string, size int64, body io.Reader) (string, error) {
	actor, err := r.actor(ctx, session)
	if err != nil {
		return "", err
	}
	return r.files.Create(ctx, actor, requestKey, groups, metadata, contentType, size, body)
}

func (r *Runtime) CreateFileVerified(ctx context.Context, session Session, requestKey string, groups []string, metadata map[string]string, contentType string, size int64, expectedSHA256 string, body io.Reader) (string, error) {
	actor, err := r.actor(ctx, session)
	if err != nil {
		return "", err
	}
	return r.files.CreateVerified(ctx, actor, requestKey, groups, metadata, contentType, size, expectedSHA256, body)
}

func (r *Runtime) ReadFile(ctx context.Context, session Session, id string) (io.ReadCloser, File, error) {
	actor, err := r.actor(ctx, session)
	if err != nil {
		return nil, File{}, err
	}
	body, object, err := r.files.Read(ctx, actor, id)
	if err != nil {
		return nil, File{}, err
	}
	return body, exposeFile(object), nil
}

func (r *Runtime) ReadFileRange(ctx context.Context, session Session, id string, start, end int64) (io.ReadCloser, File, error) {
	actor, err := r.actor(ctx, session)
	if err != nil {
		return nil, File{}, err
	}
	body, object, err := r.files.ReadRange(ctx, actor, id, start, end)
	if err != nil {
		return nil, File{}, err
	}
	return body, exposeFile(object), nil
}

func (r *Runtime) StatFile(ctx context.Context, session Session, id string) (File, error) {
	actor, err := r.actor(ctx, session)
	if err != nil {
		return File{}, err
	}
	object, err := r.files.Stat(ctx, actor, id)
	if err != nil {
		return File{}, err
	}
	return exposeFile(object), nil
}

func (r *Runtime) ListFiles(ctx context.Context, session Session, group, cursor string, limit int) ([]File, string, error) {
	actor, err := r.actor(ctx, session)
	if err != nil {
		return nil, "", err
	}
	objects, next, err := r.fileIndex.ListVisible(ctx, actor, group, cursor, limit)
	if err != nil {
		return nil, "", err
	}
	return exposeFiles(objects), next, nil
}

func (r *Runtime) UpdateFile(ctx context.Context, session Session, id string, expected int64, metadata map[string]string) error {
	actor, err := r.actor(ctx, session)
	if err != nil {
		return err
	}
	return r.files.Update(ctx, actor, id, expected, metadata)
}

func (r *Runtime) AddFileToGroup(ctx context.Context, session Session, id, group string, expected int64) error {
	actor, err := r.actor(ctx, session)
	if err != nil {
		return err
	}
	return r.files.AddToGroup(ctx, actor, id, group, expected)
}

func (r *Runtime) RemoveFileFromGroup(ctx context.Context, session Session, id, group string, expected int64) error {
	actor, err := r.actor(ctx, session)
	if err != nil {
		return err
	}
	return r.files.RemoveFromGroup(ctx, actor, id, group, expected)
}

func (r *Runtime) DeleteFile(ctx context.Context, session Session, id string, expected int64) error {
	actor, err := r.actor(ctx, session)
	if err != nil {
		return err
	}
	return r.files.Delete(ctx, actor, id, expected)
}

func (r *Runtime) CreateManagedFileSet(ctx context.Context, session Session, namespace, name, managementGroup string) (FileSet, error) {
	actor, err := r.actor(ctx, session)
	if err != nil {
		return FileSet{}, err
	}
	return r.fileIndex.CreateSet(ctx, actor, namespace, name, managementGroup)
}

func (r *Runtime) GetManagedFileSet(ctx context.Context, session Session, value string) (FileSet, error) {
	actor, err := r.actor(ctx, session)
	if err != nil {
		return FileSet{}, err
	}
	return r.fileIndex.GetSet(ctx, actor, value)
}

func (r *Runtime) UpdateManagedFileSet(ctx context.Context, session Session, value string, expected int64, name string) (FileSet, error) {
	actor, err := r.actor(ctx, session)
	if err != nil {
		return FileSet{}, err
	}
	return r.fileIndex.UpdateSet(ctx, actor, value, expected, name)
}

func (r *Runtime) DeleteManagedFileSet(ctx context.Context, session Session, value string, expected int64) error {
	actor, err := r.actor(ctx, session)
	if err != nil {
		return err
	}
	return r.fileIndex.DeleteSet(ctx, actor, value, expected)
}

func (r *Runtime) AddFileToManagedSet(ctx context.Context, session Session, setValue, fileValue string, expected int64) (FileSet, error) {
	actor, err := r.actor(ctx, session)
	if err != nil {
		return FileSet{}, err
	}
	return r.fileIndex.AddToSet(ctx, actor, setValue, fileValue, expected)
}

func (r *Runtime) RemoveFileFromManagedSet(ctx context.Context, session Session, setValue, fileValue string, expected int64) (FileSet, error) {
	actor, err := r.actor(ctx, session)
	if err != nil {
		return FileSet{}, err
	}
	return r.fileIndex.RemoveFromSet(ctx, actor, setValue, fileValue, expected)
}

func (r *Runtime) ListManagedFileSets(ctx context.Context, session Session, namespace, query, cursor string, limit int) ([]FileSet, string, error) {
	actor, err := r.actor(ctx, session)
	if err != nil {
		return nil, "", err
	}
	return r.fileIndex.ListSets(ctx, actor, namespace, query, cursor, limit)
}

func (r *Runtime) ListFileSet(ctx context.Context, session Session, setID, cursor string, limit int) ([]File, string, error) {
	actor, err := r.actor(ctx, session)
	if err != nil {
		return nil, "", err
	}
	objects, next, err := r.fileIndex.ListSet(ctx, actor, setID, cursor, limit)
	if err != nil {
		return nil, "", err
	}
	return exposeFiles(objects), next, nil
}

func (r *Runtime) SearchFiles(ctx context.Context, session Session, query, group, cursor, setID string, limit int) ([]File, string, error) {
	actor, err := r.actor(ctx, session)
	if err != nil {
		return nil, "", err
	}
	objects, next, err := r.fileIndex.Search(ctx, actor, query, group, cursor, setID, limit)
	if err != nil {
		return nil, "", err
	}
	return exposeFiles(objects), next, nil
}

func exposeFiles(objects []storage.Object) []File {
	files := make([]File, 0, len(objects))
	for _, object := range objects {
		files = append(files, exposeFile(object))
	}
	return files
}

func cloneMap(source map[string]string) map[string]string {
	copy := make(map[string]string, len(source))
	for key, value := range source {
		copy[key] = value
	}
	return copy
}
