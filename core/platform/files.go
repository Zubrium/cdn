package platform

import (
	"context"
	"io"

	"github.com/Zubrium/cdn/core/internal/postgres"
	"github.com/Zubrium/cdn/core/internal/storage"
)

// PutStagedObject stores opaque upload bytes for Loader. Loader owns the
// session, part ordering and expiry; Core enforces its object limit, current
// group access, principal binding and content checksum.
func (r *Runtime) PutStagedObject(ctx context.Context, session Session, id string, groups []string, size int64, checksum string, body io.Reader) error {
	actor, err := r.actor(ctx, session)
	if err != nil {
		return err
	}
	return postgres.PutStagedObject(ctx, r.database, r.objects, actor, id, groups, r.maxObjectBytes, size, checksum, body)
}

// ReadStagedObject returns a stream while holding a cleanup lock until Close.
func (r *Runtime) ReadStagedObject(ctx context.Context, session Session, id string) (io.ReadCloser, int64, string, error) {
	actor, err := r.actor(ctx, session)
	if err != nil {
		return nil, 0, "", err
	}
	return postgres.ReadStagedObject(ctx, r.database, r.objects, actor, id)
}

// DeleteStagedObject permanently removes an opaque Loader staging object.
func (r *Runtime) DeleteStagedObject(ctx context.Context, session Session, id string) error {
	actor, err := r.actor(ctx, session)
	if err != nil {
		return err
	}
	return postgres.DeleteStagedObject(ctx, r.database, r.objects, actor, id)
}

var ErrFileNotFound = storage.ErrNotFound
var ErrRevisionConflict = storage.ErrRevisionConflict
var ErrUploadInProgress = storage.ErrUploadInProgress
var ErrRequestConflict = storage.ErrRequestConflict
var ErrRequestDeleted = storage.ErrRequestDeleted

type File struct {
	ID          string
	Groups      []string
	Metadata    map[string]string
	ContentType string
	Revision    int64
	Size        int64
}

func exposeFile(object storage.Object) File {
	return File{
		ID: object.ID, Groups: append([]string(nil), object.Groups...), Metadata: cloneMap(object.Metadata),
		ContentType: object.ContentType, Revision: object.Revision, Size: object.Size,
	}
}

func (r *Runtime) CreateFile(ctx context.Context, session Session, requestKey string, groups []string, metadata map[string]string, contentType string, size int64, body io.Reader) (string, error) {
	actor, err := r.actor(ctx, session)
	if err != nil {
		return "", err
	}
	return r.files.Create(ctx, actor, requestKey, groups, metadata, contentType, size, body)
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
	objects, next, err := (postgres.Objects{Pool: r.database}).ListVisible(ctx, actor, group, cursor, limit)
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

func (r *Runtime) CreateFileSet(ctx context.Context, session Session, namespace, name string) (string, error) {
	actor, err := r.actor(ctx, session)
	if err != nil {
		return "", err
	}
	id, err := postgres.CreateFileSet(ctx, r.database, actor, namespace, name)
	if err != nil {
		return "", err
	}
	return id.String(), nil
}

func (r *Runtime) AddFileToSet(ctx context.Context, session Session, setValue, fileValue string) error {
	actor, err := r.actor(ctx, session)
	if err != nil {
		return err
	}
	setID, err := parseID(setValue)
	if err != nil {
		return err
	}
	fileID, err := parseID(fileValue)
	if err != nil {
		return err
	}
	return postgres.AddFileToSet(ctx, r.database, actor, setID, fileID)
}

func (r *Runtime) RemoveFileFromSet(ctx context.Context, session Session, setValue, fileValue string) error {
	actor, err := r.actor(ctx, session)
	if err != nil {
		return err
	}
	setID, err := parseID(setValue)
	if err != nil {
		return err
	}
	fileID, err := parseID(fileValue)
	if err != nil {
		return err
	}
	return postgres.RemoveFileFromSet(ctx, r.database, actor, setID, fileID)
}

func (r *Runtime) DeleteFileSet(ctx context.Context, session Session, value string) error {
	actor, err := r.actor(ctx, session)
	if err != nil {
		return err
	}
	setID, err := parseID(value)
	if err != nil {
		return err
	}
	return postgres.DeleteFileSet(ctx, r.database, actor, setID)
}

func (r *Runtime) ListFileSet(ctx context.Context, session Session, setID, cursor string, limit int) ([]File, string, error) {
	actor, err := r.actor(ctx, session)
	if err != nil {
		return nil, "", err
	}
	objects, next, err := (postgres.Objects{Pool: r.database}).ListFileSet(ctx, actor, setID, cursor, limit)
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
	objects, next, err := (postgres.Objects{Pool: r.database}).SearchFiles(ctx, actor, query, group, cursor, setID, limit)
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
