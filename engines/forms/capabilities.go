package forms

import (
	"context"

	"github.com/Zubrium/cdn/pkg/contracts"
)

// These ports are implemented by main with module-scoped DataBus clients.
// Forms depends only on neutral identity subjects, principals and access levels.
type Identity interface {
	Authenticate(context.Context, string) (contracts.AuthorizationSubject, contracts.Principal, error)
}

type Sessions interface {
	AuthenticateSession(context.Context, string) (contracts.AuthorizationSubject, contracts.Principal, error)
}

type Authorization interface {
	RequireGroupLevel(context.Context, contracts.AuthorizationSubject, []string, contracts.AccessLevel) error
}
