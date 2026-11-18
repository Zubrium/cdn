package presentation

import "context"

// SessionCapability exchanges a valid credential for a revocable Core session
// bound to that credential. Presentation decides how a client transports it.
type SessionCapability interface {
	CreateSession(context.Context, string) (CreatedSession, error)
	AuthenticateSession(context.Context, string) (Session, Principal, error)
	RevokeSession(context.Context, string) error
}

func (r *Runtime) Sessions() SessionCapability { return r }

var _ SessionCapability = (*Runtime)(nil)
