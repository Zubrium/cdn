package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"time"

	"github.com/Zubrium/cdn/core/domain/access"
)

var ErrSessionInvalid = errors.New("invalid Core session")

const maxSessionLifetime = 365 * 24 * time.Hour

type SessionService struct {
	repository  access.SessionRepository
	idleTTL     time.Duration
	absoluteTTL time.Duration
	now         func() time.Time
}

type IssuedSession struct {
	Token   string
	Session access.CredentialSession
}

func NewSessionService(repository access.SessionRepository, idleTTL, absoluteTTL time.Duration) (*SessionService, error) {
	if repository == nil || idleTTL <= 0 || absoluteTTL <= 0 || idleTTL > absoluteTTL || absoluteTTL > maxSessionLifetime {
		return nil, ErrSessionInvalid
	}
	return &SessionService{repository: repository, idleTTL: idleTTL, absoluteTTL: absoluteTTL, now: time.Now}, nil
}

func (s *SessionService) Create(ctx context.Context, credential string) (IssuedSession, error) {
	if s == nil || s.repository == nil || ctx == nil || len(credential) < 32 || len(credential) > 512 {
		return IssuedSession{}, access.ErrUnauthorized
	}
	if err := ctx.Err(); err != nil {
		return IssuedSession{}, err
	}
	token, err := newOpaqueToken("ss_")
	if err != nil {
		return IssuedSession{}, err
	}
	now := s.now().UTC()
	absoluteExpires := now.Add(s.absoluteTTL)
	if !absoluteExpires.After(now) {
		return IssuedSession{}, ErrSessionInvalid
	}
	idleExpires := now.Add(s.idleTTL)
	if idleExpires.After(absoluteExpires) {
		idleExpires = absoluteExpires
	}
	session, err := s.repository.CreateSession(ctx, digest(credential), digest(token), now, idleExpires, absoluteExpires)
	if err != nil {
		return IssuedSession{}, err
	}
	if session.ID == "" || session.CredentialID == "" || session.Principal.ID == "" || !session.Principal.Active ||
		!session.IdleExpiresAt.After(now) || !session.AbsoluteExpires.After(now) {
		return IssuedSession{}, access.ErrUnauthorized
	}
	return IssuedSession{Token: token, Session: session}, nil
}

func (s *SessionService) Authenticate(ctx context.Context, token string) (access.CredentialSession, error) {
	if s == nil || s.repository == nil || ctx == nil || !validOpaqueToken(token, "ss_") {
		return access.CredentialSession{}, access.ErrUnauthorized
	}
	if err := ctx.Err(); err != nil {
		return access.CredentialSession{}, err
	}
	session, err := s.repository.AuthenticateSession(ctx, digest(token), s.now().UTC(), s.idleTTL)
	if err != nil {
		return access.CredentialSession{}, err
	}
	if session.ID == "" || session.CredentialID == "" || session.Principal.ID == "" || !session.Principal.Active {
		return access.CredentialSession{}, access.ErrUnauthorized
	}
	return session, nil
}

func (s *SessionService) ResolveHash(ctx context.Context, sessionHash []byte) (access.CredentialSession, error) {
	if s == nil || s.repository == nil || ctx == nil || len(sessionHash) != sha256.Size {
		return access.CredentialSession{}, access.ErrUnauthorized
	}
	if err := ctx.Err(); err != nil {
		return access.CredentialSession{}, err
	}
	session, err := s.repository.AuthenticateSession(ctx, append([]byte(nil), sessionHash...), s.now().UTC(), s.idleTTL)
	if err != nil {
		return access.CredentialSession{}, err
	}
	if session.ID == "" || session.Principal.ID == "" || !session.Principal.Active {
		return access.CredentialSession{}, access.ErrUnauthorized
	}
	return session, nil
}

func (s *SessionService) Revoke(ctx context.Context, token string) error {
	if s == nil || s.repository == nil || ctx == nil || !validOpaqueToken(token, "ss_") {
		return access.ErrUnauthorized
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.repository.RevokeSession(ctx, digest(token), s.now().UTC())
}

func newOpaqueToken(prefix string) (string, error) {
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(random[:]), nil
}

func validOpaqueToken(value, prefix string) bool {
	if len(value) != len(prefix)+43 || value[:len(prefix)] != prefix {
		return false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value[len(prefix):])
	return err == nil && len(decoded) == 32
}

func digest(value string) []byte {
	hash := sha256.Sum256([]byte(value))
	return hash[:]
}
