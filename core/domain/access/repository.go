package access

import (
	"context"
	"errors"
	"time"
)

var ErrManagerInvariant = errors.New("administrator management invariant would be violated")
var ErrInvalid = errors.New("invalid identity operation")

type Credential struct {
	ID, PrincipalID      string
	CreatedAt            time.Time
	ExpiresAt, RevokedAt *time.Time
}

type ConfiguredGroup struct {
	Key    string
	Active bool
	Level  Level
}

// IdentityRepository is the persistence port for the single user identity
// model. Human operators and service integrations use the same Principal;
// credentials and direct group grants determine their capabilities.
type IdentityRepository interface {
	Bootstrap(context.Context, string, map[string]Level) (string, bool, error)
	Authenticate(context.Context, string) (Principal, error)
	AuthenticateHash(context.Context, []byte) (Principal, error)
	CreatePrincipal(context.Context, Principal, string, *time.Time) (string, string, error)
	CreatePrincipalWithAccess(context.Context, Principal, string, *time.Time, bool, map[string]Level) (string, string, error)
	ListPrincipals(context.Context, Principal) ([]Principal, error)
	SetPrincipalManagement(context.Context, Principal, string, bool) error
	DeactivatePrincipal(context.Context, Principal, string) error
	IssueCredential(context.Context, Principal, string, *time.Time) (string, string, error)
	ListCredentials(context.Context, Principal, string) ([]Credential, error)
	RevokeCredential(context.Context, Principal, string) error
	SetGroupAccess(context.Context, Principal, string, string, Level) error
	GroupAccess(context.Context, Principal, string, string) (Level, error)
	ListAccessGroups(context.Context, string) ([]ConfiguredGroup, error)
	Assignments(context.Context, string) ([]GroupAssignment, error)
	RecoverBootstrapCredential(context.Context) (string, error)
}
