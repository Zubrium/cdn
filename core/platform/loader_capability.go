package platform

import (
	"context"
	"io"

	"github.com/Zubrium/cdn/core/access"
	"github.com/Zubrium/cdn/core/internal/postgres"
)

// LoaderIdentityCapability adds the upload-specific group preflight to the
// common service identity surface.
type LoaderIdentityCapability interface {
	EngineIdentityCapability
	RequireGroupLevel(context.Context, Session, []string, access.Level) error
}

// LoaderFilesCapability includes the common authorized file surface plus the
// staging operations that are reserved for resumable upload handling.
type LoaderFilesCapability interface {
	EngineFilesCapability
	PutStagedObject(context.Context, Session, string, []string, int64, string, io.Reader) error
	ReadStagedObject(context.Context, Session, string) (io.ReadCloser, int64, string, error)
	PurgeStagedObject(context.Context, string) error
	ResolveUploadRequest(context.Context, string, string) (UploadRequestStatus, error)
}

type loaderIdentityCapability struct{ *engineIdentityCapability }
type loaderFilesCapability struct{ *engineFilesCapability }

func (r *Runtime) LoaderIdentity() LoaderIdentityCapability {
	return &loaderIdentityCapability{engineIdentityCapability: &engineIdentityCapability{runtime: r}}
}

func (r *Runtime) LoaderFiles() LoaderFilesCapability {
	return &loaderFilesCapability{engineFilesCapability: &engineFilesCapability{runtime: r}}
}

func (c *loaderIdentityCapability) RequireGroupLevel(ctx context.Context, session Session, groups []string, required access.Level) error {
	if c == nil || c.engineIdentityCapability == nil || c.runtime == nil || len(groups) == 0 || (required != access.LevelRead && required != access.LevelWrite) {
		return ErrForbidden
	}
	actor, err := c.runtime.actor(ctx, session)
	if err != nil {
		return err
	}
	assignments, err := postgres.Assignments(ctx, c.runtime.database, actor.ID)
	if err != nil {
		return err
	}
	action := access.Read
	if required == access.LevelWrite {
		action = access.Write
	}
	for _, group := range groups {
		if !access.AuthorizeGroup(actor, action, group, assignments) {
			return ErrForbidden
		}
	}
	return nil
}

func (c *loaderFilesCapability) PutStagedObject(ctx context.Context, session Session, id string, groups []string, size int64, checksum string, body io.Reader) error {
	r, err := c.runtimeOrError()
	if err != nil {
		return err
	}
	return r.PutStagedObject(ctx, session, id, groups, size, checksum, body)
}

func (c *loaderFilesCapability) ReadStagedObject(ctx context.Context, session Session, id string) (io.ReadCloser, int64, string, error) {
	r, err := c.runtimeOrError()
	if err != nil {
		return nil, 0, "", err
	}
	return r.ReadStagedObject(ctx, session, id)
}

// PurgeStagedObject removes only the exact opaque Loader staging handle and is
// retryable after the initiating principal or its group assignment is revoked.
func (c *loaderFilesCapability) PurgeStagedObject(ctx context.Context, id string) error {
	r, err := c.runtimeOrError()
	if err != nil {
		return err
	}
	return postgres.PurgeStagedObject(ctx, r.database, r.objects, id)
}

func (c *loaderFilesCapability) ResolveUploadRequest(ctx context.Context, principalID, requestKey string) (UploadRequestStatus, error) {
	r, err := c.runtimeOrError()
	if err != nil {
		return UploadRequestStatus{}, err
	}
	status, err := postgres.FindUploadRequest(ctx, r.database, principalID, requestKey)
	return UploadRequestStatus{State: status.State, FileID: status.FileID}, err
}

func (c *loaderFilesCapability) runtimeOrError() (*Runtime, error) {
	if c == nil || c.engineFilesCapability == nil {
		return nil, ErrUnavailable
	}
	return c.engineFilesCapability.runtimeOrError()
}

var _ LoaderIdentityCapability = (*loaderIdentityCapability)(nil)
var _ LoaderFilesCapability = (*loaderFilesCapability)(nil)
