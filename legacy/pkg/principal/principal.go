package principal

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type Role string

const (
	RoleAdmin  Role = "admin"
	RoleStaff  Role = "staff"
	RoleUser   Role = "user"
	RolePublic Role = "public"
)

// Principal represents the authenticated caller.
type Principal struct {
	ID        uuid.UUID  `json:"id"`
	TokenID   uuid.UUID  `json:"-"`
	Name      string     `json:"name"`
	Token     string     `json:"-"`
	Role      Role       `json:"role"`
	CreatedBy *uuid.UUID `json:"created_by,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
	Revoked   bool       `json:"revoked"`
}

type key struct{}

// WithPrincipal stores the principal in context.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, key{}, p)
}

// FromContext extracts the principal from context.
func FromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(key{}).(Principal)
	return p, ok
}

// MustFromContext extracts the principal and panics if missing.
func MustFromContext(ctx context.Context) Principal {
	p, ok := FromContext(ctx)
	if !ok {
		panic("principal missing from context")
	}
	return p
}
