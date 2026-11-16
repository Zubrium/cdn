package auth

import (
	"context"

	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"time"
	"websiteengine/pkg/principal"

	"github.com/google/uuid"
)

const (
	RoleAdmin     principal.Role = principal.RoleAdmin
	RoleModerator principal.Role = "moderator"
	RoleStaff     principal.Role = principal.RoleStaff
	RoleUser      principal.Role = principal.RoleUser
	RolePublic    principal.Role = principal.RolePublic

	tokenPrefix = "ct_live_"
)

var (
	ErrUnauthorized = errors.New("unauthorized")
	ErrForbidden    = errors.New("forbidden")
	ErrNotFound     = errors.New("not found")
	ErrConflict     = errors.New("conflict")
	ErrValidation   = errors.New("validation")
)

type Principal = principal.Principal

type Token struct {
	ID         uuid.UUID      `json:"id"`
	Name       string         `json:"name"`
	Role       principal.Role `json:"role"`
	CreatedBy  *uuid.UUID     `json:"created_by"`
	ExpiresAt  *time.Time     `json:"expires_at"`
	RevokedAt  *time.Time     `json:"revoked_at"`
	LastUsedAt *time.Time     `json:"last_used_at"`
	CreatedAt  time.Time      `json:"created_at"`
}

type NewToken struct {
	Token Token
	// Secret is returned to the caller exactly once and never persisted.
	Secret string
}

func NewSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return tokenPrefix + base64.RawURLEncoding.EncodeToString(buf), nil
}

func HashSecret(secret string) []byte {
	sum := sha256.Sum256([]byte(secret))
	return sum[:]
}

func HashHex(secret string) string {
	return hex.EncodeToString(HashSecret(secret))
}

func IsAdmin(role any) bool {
	var r principal.Role
	switch v := role.(type) {
	case string:
		r = principal.Role(v)
	case principal.Role:
		r = v
	case Principal:
		r = v.Role
	default:
		return false
	}
	return r == principal.RoleAdmin
}

func IsStaff(role any) bool {
	var r principal.Role
	switch v := role.(type) {
	case string:
		r = principal.Role(v)
	case principal.Role:
		r = v
	case Principal:
		r = v.Role
	default:
		return false
	}
	return r == principal.RoleAdmin || r == RoleModerator || r == principal.RoleStaff
}

func ValidRole(role any) bool {
	var r principal.Role
	switch v := role.(type) {
	case string:
		r = principal.Role(v)
	case principal.Role:
		r = v
	case Principal:
		r = v.Role
	default:
		return false
	}
	return r == principal.RoleAdmin || r == RoleModerator || r == principal.RoleUser || r == principal.RoleStaff
}

func CleanName(name string) (string, bool) {
	name = strings.TrimSpace(name)
	if len([]rune(name)) < 1 || len([]rune(name)) > 120 {
		return "", false
	}
	return name, true
}

func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return principal.WithPrincipal(ctx, p)
}

func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	return principal.FromContext(ctx)
}
