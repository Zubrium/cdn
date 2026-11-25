package composition

import (
	"context"

	platform "github.com/Zubrium/cdn/core/presentation"
	"github.com/Zubrium/cdn/pkg/contracts"
)

// resolveCoreSession is a Core-facing composition helper. It resolves an
// already authenticated subject into a Core session for adapters registered
// inside the composition root; callers outside composition only see DataBus
// clients.
func resolveCoreSession(ctx context.Context, r *platform.Runtime, subject contracts.AuthorizationSubject) (platform.Session, error) {
	if r == nil || subject.Validate() != nil {
		return platform.Session{}, contracts.ErrAccessInvalid
	}
	if subject.Credential != "" {
		session, _, err := r.Identity().Authenticate(ctx, subject.Credential)
		return session, err
	}
	session, _, err := r.Sessions().AuthenticateSession(ctx, subject.SessionToken)
	return session, err
}
