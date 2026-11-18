package presentation

import (
	"context"
	"io"

	"github.com/Zubrium/cdn/core/domain/access"
)

// GroupAuthorizationCapability authenticates a principal and checks access levels
// across a set of configured groups.
type GroupAuthorizationCapability interface {
	EngineIdentityCapability
	RequireGroupLevel(context.Context, Session, []string, access.Level) error
}

// StagedFilesCapability exposes authorized file operations and opaque
// temporary-byte operations without exposing storage infrastructure.
type StagedFilesCapability interface {
	EngineFilesCapability
	PutStagedObject(context.Context, Session, string, []string, int64, string, io.Reader) error
	ReadStagedObject(context.Context, Session, string) (io.ReadCloser, int64, string, error)
	PurgeStagedObject(context.Context, string) error
	ResolveUploadRequest(context.Context, string, string) (UploadRequestStatus, error)
}

type groupAuthorizationCapability struct{ *engineIdentityCapability }
type stagedFilesCapability struct{ *engineFilesCapability }

func (r *Runtime) GroupAuthorization() GroupAuthorizationCapability {
	return &groupAuthorizationCapability{engineIdentityCapability: &engineIdentityCapability{runtime: r}}
}

func (r *Runtime) StagedFiles() StagedFilesCapability {
	return &stagedFilesCapability{engineFilesCapability: &engineFilesCapability{runtime: r}}
}

func (c *groupAuthorizationCapability) RequireGroupLevel(ctx context.Context, session Session, groups []string, required access.Level) error {
	if c == nil || c.engineIdentityCapability == nil || c.runtime == nil || len(groups) == 0 || (required != access.LevelRead && required != access.LevelWrite) {
		return ErrForbidden
	}
	actor, err := c.runtime.actor(ctx, session)
	if err != nil {
		return err
	}
	if c.runtime.identities == nil {
		return ErrUnavailable
	}
	return c.runtime.identities.RequireGroupsLevel(ctx, actor, groups, required)
}

func (c *stagedFilesCapability) PutStagedObject(ctx context.Context, session Session, id string, groups []string, size int64, checksum string, body io.Reader) error {
	r, err := c.runtimeOrError()
	if err != nil {
		return err
	}
	return r.PutStagedObject(ctx, session, id, groups, size, checksum, body)
}

func (c *stagedFilesCapability) ReadStagedObject(ctx context.Context, session Session, id string) (io.ReadCloser, int64, string, error) {
	r, err := c.runtimeOrError()
	if err != nil {
		return nil, 0, "", err
	}
	return r.ReadStagedObject(ctx, session, id)
}

// PurgeStagedObject removes only the exact opaque staging handle and is
// retryable when the initiating principal or group assignment is no longer active.
func (c *stagedFilesCapability) PurgeStagedObject(ctx context.Context, id string) error {
	r, err := c.runtimeOrError()
	if err != nil {
		return err
	}
	return r.staging.Purge(ctx, id)
}

func (c *stagedFilesCapability) ResolveUploadRequest(ctx context.Context, principalID, requestKey string) (UploadRequestStatus, error) {
	r, err := c.runtimeOrError()
	if err != nil {
		return UploadRequestStatus{}, err
	}
	return r.staging.ResolveUploadRequest(ctx, principalID, requestKey)
}

func (c *stagedFilesCapability) runtimeOrError() (*Runtime, error) {
	if c == nil || c.engineFilesCapability == nil {
		return nil, ErrUnavailable
	}
	return c.engineFilesCapability.runtimeOrError()
}

var _ GroupAuthorizationCapability = (*groupAuthorizationCapability)(nil)
var _ StagedFilesCapability = (*stagedFilesCapability)(nil)
