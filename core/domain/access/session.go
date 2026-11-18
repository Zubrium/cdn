package access

import (
	"context"
	"time"
)

// CredentialSession tracks use of one specific Core credential. The Core
// persists only the opaque session token digest and credential relationship.
type CredentialSession struct {
	ID              string
	CredentialID    string
	Principal       Principal
	CreatedAt       time.Time
	LastSeenAt      time.Time
	IdleExpiresAt   time.Time
	AbsoluteExpires time.Time
}

// SessionRepository is Core's persistence port for revocable sessions. Every
// operation checks the exact linked credential and current principal state.
type SessionRepository interface {
	CreateSession(context.Context, []byte, []byte, time.Time, time.Time, time.Time) (CredentialSession, error)
	AuthenticateSession(context.Context, []byte, time.Time, time.Duration) (CredentialSession, error)
	RevokeSession(context.Context, []byte, time.Time) error
}
