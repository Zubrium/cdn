package content

import (
	"context"

	"github.com/Zubrium/cdn/pkg/contracts"
)

// Identity, Sessions, and Authorization are module-owned ports. Composition
// supplies DataBus clients; Content knows neither Core runtime types nor its
// implementations.
type Identity interface {
	Authenticate(context.Context, string) (contracts.AuthorizationSubject, contracts.Principal, error)
}

type Sessions interface {
	AuthenticateSession(context.Context, string) (contracts.AuthorizationSubject, contracts.Principal, error)
}

type Authorization interface {
	RequireGroupLevel(context.Context, contracts.AuthorizationSubject, []string, contracts.AccessLevel) error
}
