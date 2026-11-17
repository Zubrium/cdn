package platform

import (
	"context"
	"io"
)

// EngineIdentityCapability authenticates a configured Core user and returns
// only its opaque session and public principal summary.
type EngineIdentityCapability interface {
	Authenticate(context.Context, string) (Session, Principal, error)
}

// EngineFilesCapability exposes authorized file and FileSet operations without
// giving an engine access to Runtime, SQL, S3, or identity administration.
type EngineFilesCapability interface {
	CreateFile(context.Context, Session, string, []string, map[string]string, string, int64, io.Reader) (string, error)
	CreateFileVerified(context.Context, Session, string, []string, map[string]string, string, int64, string, io.Reader) (string, error)
	ReadFile(context.Context, Session, string) (io.ReadCloser, File, error)
	ReadFileRange(context.Context, Session, string, int64, int64) (io.ReadCloser, File, error)
	StatFile(context.Context, Session, string) (File, error)
	SearchFiles(context.Context, Session, string, string, string, string, int) ([]File, string, error)
	UpdateFileWithContentType(context.Context, Session, string, int64, map[string]string, string) error
	AddFileToGroup(context.Context, Session, string, string, int64) error
	RemoveFileFromGroup(context.Context, Session, string, string, int64) error
	DeleteFile(context.Context, Session, string, int64) error
	CreateManagedFileSet(context.Context, Session, string, string, string) (FileSet, error)
	GetManagedFileSet(context.Context, Session, string) (FileSet, error)
	UpdateManagedFileSet(context.Context, Session, string, int64, string) (FileSet, error)
	DeleteManagedFileSet(context.Context, Session, string, int64) error
	AddFileToManagedSet(context.Context, Session, string, string, int64) (FileSet, error)
	RemoveFileFromManagedSet(context.Context, Session, string, string, int64) (FileSet, error)
	ListManagedFileSets(context.Context, Session, string, string, string, int) ([]FileSet, string, error)
	ListFileSet(context.Context, Session, string, string, int) ([]File, string, error)
}

type engineIdentityCapability struct{ runtime *Runtime }
type engineFilesCapability struct{ runtime *Runtime }

func (r *Runtime) EngineIdentity() EngineIdentityCapability {
	return &engineIdentityCapability{runtime: r}
}

func (r *Runtime) EngineFiles() EngineFilesCapability {
	return &engineFilesCapability{runtime: r}
}

func (c *engineIdentityCapability) Authenticate(ctx context.Context, credential string) (Session, Principal, error) {
	if c == nil || c.runtime == nil {
		return Session{}, Principal{}, ErrUnavailable
	}
	session, actor, err := c.runtime.authenticatePrincipal(ctx, credential)
	if err != nil {
		return Session{}, Principal{}, err
	}
	return session, Principal{ID: actor.ID, Name: actor.Name, Active: actor.Active, CanManageAdministrators: actor.CanManageAdministrators}, nil
}

func (c *engineFilesCapability) runtimeOrError() (*Runtime, error) {
	if c == nil || c.runtime == nil {
		return nil, ErrUnavailable
	}
	if err := c.runtime.valid(); err != nil {
		return nil, err
	}
	return c.runtime, nil
}

func (c *engineFilesCapability) CreateFile(ctx context.Context, session Session, requestKey string, groups []string, metadata map[string]string, contentType string, size int64, body io.Reader) (string, error) {
	r, err := c.runtimeOrError()
	if err != nil {
		return "", err
	}
	return r.CreateFile(ctx, session, requestKey, groups, metadata, contentType, size, body)
}

func (c *engineFilesCapability) CreateFileVerified(ctx context.Context, session Session, requestKey string, groups []string, metadata map[string]string, contentType string, size int64, checksum string, body io.Reader) (string, error) {
	r, err := c.runtimeOrError()
	if err != nil {
		return "", err
	}
	return r.CreateFileVerified(ctx, session, requestKey, groups, metadata, contentType, size, checksum, body)
}

func (c *engineFilesCapability) ReadFile(ctx context.Context, session Session, id string) (io.ReadCloser, File, error) {
	r, err := c.runtimeOrError()
	if err != nil {
		return nil, File{}, err
	}
	return r.ReadFile(ctx, session, id)
}

func (c *engineFilesCapability) ReadFileRange(ctx context.Context, session Session, id string, start, end int64) (io.ReadCloser, File, error) {
	r, err := c.runtimeOrError()
	if err != nil {
		return nil, File{}, err
	}
	return r.ReadFileRange(ctx, session, id, start, end)
}

func (c *engineFilesCapability) StatFile(ctx context.Context, session Session, id string) (File, error) {
	r, err := c.runtimeOrError()
	if err != nil {
		return File{}, err
	}
	return r.StatFile(ctx, session, id)
}

func (c *engineFilesCapability) SearchFiles(ctx context.Context, session Session, query, group, cursor, setID string, limit int) ([]File, string, error) {
	r, err := c.runtimeOrError()
	if err != nil {
		return nil, "", err
	}
	return r.SearchFiles(ctx, session, query, group, cursor, setID, limit)
}

func (c *engineFilesCapability) UpdateFileWithContentType(ctx context.Context, session Session, id string, expected int64, metadata map[string]string, contentType string) error {
	r, err := c.runtimeOrError()
	if err != nil {
		return err
	}
	actor, err := r.actor(ctx, session)
	if err != nil {
		return err
	}
	return r.files.UpdateWithContentType(ctx, actor, id, expected, metadata, contentType)
}

func (c *engineFilesCapability) AddFileToGroup(ctx context.Context, session Session, id, group string, expected int64) error {
	r, err := c.runtimeOrError()
	if err != nil {
		return err
	}
	return r.AddFileToGroup(ctx, session, id, group, expected)
}

func (c *engineFilesCapability) RemoveFileFromGroup(ctx context.Context, session Session, id, group string, expected int64) error {
	r, err := c.runtimeOrError()
	if err != nil {
		return err
	}
	return r.RemoveFileFromGroup(ctx, session, id, group, expected)
}

func (c *engineFilesCapability) DeleteFile(ctx context.Context, session Session, id string, expected int64) error {
	r, err := c.runtimeOrError()
	if err != nil {
		return err
	}
	return r.DeleteFile(ctx, session, id, expected)
}

func (c *engineFilesCapability) CreateManagedFileSet(ctx context.Context, session Session, namespace, name, managementGroup string) (FileSet, error) {
	r, err := c.runtimeOrError()
	if err != nil {
		return FileSet{}, err
	}
	return r.CreateManagedFileSet(ctx, session, namespace, name, managementGroup)
}

func (c *engineFilesCapability) GetManagedFileSet(ctx context.Context, session Session, id string) (FileSet, error) {
	r, err := c.runtimeOrError()
	if err != nil {
		return FileSet{}, err
	}
	return r.GetManagedFileSet(ctx, session, id)
}

func (c *engineFilesCapability) UpdateManagedFileSet(ctx context.Context, session Session, id string, expected int64, name string) (FileSet, error) {
	r, err := c.runtimeOrError()
	if err != nil {
		return FileSet{}, err
	}
	return r.UpdateManagedFileSet(ctx, session, id, expected, name)
}

func (c *engineFilesCapability) DeleteManagedFileSet(ctx context.Context, session Session, id string, expected int64) error {
	r, err := c.runtimeOrError()
	if err != nil {
		return err
	}
	return r.DeleteManagedFileSet(ctx, session, id, expected)
}

func (c *engineFilesCapability) AddFileToManagedSet(ctx context.Context, session Session, setID, fileID string, expected int64) (FileSet, error) {
	r, err := c.runtimeOrError()
	if err != nil {
		return FileSet{}, err
	}
	return r.AddFileToManagedSet(ctx, session, setID, fileID, expected)
}

func (c *engineFilesCapability) RemoveFileFromManagedSet(ctx context.Context, session Session, setID, fileID string, expected int64) (FileSet, error) {
	r, err := c.runtimeOrError()
	if err != nil {
		return FileSet{}, err
	}
	return r.RemoveFileFromManagedSet(ctx, session, setID, fileID, expected)
}

func (c *engineFilesCapability) ListManagedFileSets(ctx context.Context, session Session, namespace, query, cursor string, limit int) ([]FileSet, string, error) {
	r, err := c.runtimeOrError()
	if err != nil {
		return nil, "", err
	}
	return r.ListManagedFileSets(ctx, session, namespace, query, cursor, limit)
}

func (c *engineFilesCapability) ListFileSet(ctx context.Context, session Session, setID, cursor string, limit int) ([]File, string, error) {
	r, err := c.runtimeOrError()
	if err != nil {
		return nil, "", err
	}
	return r.ListFileSet(ctx, session, setID, cursor, limit)
}

var _ EngineIdentityCapability = (*engineIdentityCapability)(nil)
var _ EngineFilesCapability = (*engineFilesCapability)(nil)
