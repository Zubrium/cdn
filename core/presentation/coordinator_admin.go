package presentation

import (
	"context"

	appaccess "github.com/Zubrium/cdn/core/application/access"
	"github.com/Zubrium/cdn/core/domain/access"
)

// CoordinatorAuthorizationCapability authenticates one Core principal and
// checks its current direct access to a configured group. It exposes no user
// administration, file, SQL, or object-storage operations.
type CoordinatorAuthorizationCapability interface {
	Authenticate(context.Context, string) (Session, error)
	RequireGroupLevel(context.Context, Session, string, access.Level) error
}

type coordinatorAuthorizationCapability struct{ runtime *Runtime }

func (r *Runtime) CoordinatorAuthorization() CoordinatorAuthorizationCapability {
	return &coordinatorAuthorizationCapability{runtime: r}
}

func (c *coordinatorAuthorizationCapability) runtimeOrError() (*Runtime, error) {
	if c == nil || c.runtime == nil {
		return nil, ErrUnavailable
	}
	runtime := c.runtime
	if err := runtime.valid(); err != nil {
		return nil, err
	}
	if runtime.Registry == nil {
		return nil, ErrCoordinatorInvalid
	}
	if _, err := runtime.Registry.Lookup("coordinator"); err != nil {
		return nil, ErrCoordinatorInvalid
	}
	return runtime, nil
}

func (c *coordinatorAuthorizationCapability) Authenticate(ctx context.Context, credential string) (Session, error) {
	runtime, err := c.runtimeOrError()
	if err != nil {
		return Session{}, err
	}
	if ctx == nil {
		return Session{}, ErrCoordinatorInvalid
	}
	return runtime.Authenticate(ctx, credential)
}

func (c *coordinatorAuthorizationCapability) RequireGroupLevel(ctx context.Context, session Session, group string, minimum access.Level) error {
	runtime, err := c.runtimeOrError()
	if err != nil {
		return err
	}
	if ctx == nil || !appaccess.ValidGroupKey(group) || (minimum != access.LevelRead && minimum != access.LevelWrite) {
		return ErrCoordinatorInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	principal, err := runtime.actor(ctx, session)
	if err != nil {
		return err
	}
	if runtime.identities == nil {
		return ErrUnavailable
	}
	return runtime.identities.RequireGroupLevel(ctx, principal, group, minimum)
}
