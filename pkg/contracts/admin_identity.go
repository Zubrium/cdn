package contracts

import (
	"errors"
	"time"
)

// Neutral identity-administration DTOs shared by the Admin HTTP adapter and
// the composition root. They carry no Core runtime or repository types.

// ErrAccessManagerInvariant reports that an administrator-management change
// would violate the last-active-manager invariant.
var ErrAccessManagerInvariant = errors.New("administrator management invariant would be violated")

// CreatedSession is the one-time result of exchanging a credential for a
// revocable session bound to that exact credential.
type CreatedSession struct {
	Token             string    `json:"token"`
	Principal         Principal `json:"principal"`
	IdleExpiresAt     time.Time `json:"idle_expires_at"`
	AbsoluteExpiresAt time.Time `json:"absolute_expires_at"`
}

// Credential describes one issued credential without its secret value.
type Credential struct {
	ID          string     `json:"id"`
	PrincipalID string     `json:"principal_id"`
	CreatedAt   time.Time  `json:"created_at"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty"`
}

// AccessGroup is one configured static access group and the requesting
// principal's current direct level in it.
type AccessGroup struct {
	Key    string      `json:"key"`
	Active bool        `json:"active"`
	Level  AccessLevel `json:"level"`
}