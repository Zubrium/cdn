package identity

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/Zubrium/cdn/core/domain/access"
)

type sessionRepositoryStub struct {
	access.SessionRepository
	credentialHash []byte
	sessionHash    []byte
	createdAt      time.Time
	idleExpiresAt  time.Time
	absoluteUntil  time.Time
	authHash       []byte
	authIdleTTL    time.Duration
	authNow        time.Time
	session        access.CredentialSession
	authErr        error
	createCalls    int
	authCalls      int
	revokeHash     []byte
	revokeCalls    int
}

func (s *sessionRepositoryStub) CreateSession(_ context.Context, credentialHash, sessionHash []byte, now, idleExpiresAt, absoluteUntil time.Time) (access.CredentialSession, error) {
	s.createCalls++
	s.credentialHash = append([]byte(nil), credentialHash...)
	s.sessionHash = append([]byte(nil), sessionHash...)
	s.createdAt, s.idleExpiresAt, s.absoluteUntil = now, idleExpiresAt, absoluteUntil
	s.session.CreatedAt, s.session.LastSeenAt = now, now
	s.session.IdleExpiresAt, s.session.AbsoluteExpires = idleExpiresAt, absoluteUntil
	return s.session, nil
}

func (s *sessionRepositoryStub) AuthenticateSession(_ context.Context, sessionHash []byte, now time.Time, idleTTL time.Duration) (access.CredentialSession, error) {
	s.authCalls++
	s.authHash = append([]byte(nil), sessionHash...)
	s.authIdleTTL, s.authNow = idleTTL, now
	return s.session, s.authErr
}

func (s *sessionRepositoryStub) RevokeSession(_ context.Context, sessionHash []byte, _ time.Time) error {
	s.revokeCalls++
	s.revokeHash = append([]byte(nil), sessionHash...)
	return nil
}

func TestCreateSessionStoresOnlyDigestsAndBindsItToTheAuthenticatingCredential(t *testing.T) {
	repository := &sessionRepositoryStub{session: access.CredentialSession{
		ID: "session-id", CredentialID: "credential-id",
		Principal: access.Principal{ID: "principal-id", Name: "Operator", Active: true},
	}}
	service, err := NewSessionService(repository, 8*time.Hour, 7*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Create(context.Background(), "ct_live_example-credential-value")
	if err != nil {
		t.Fatal(err)
	}
	if repository.createCalls != 1 || result.Session.CredentialID != "credential-id" {
		t.Fatalf("session creation calls=%d credential=%q", repository.createCalls, result.Session.CredentialID)
	}
	wantCredential := sha256.Sum256([]byte("ct_live_example-credential-value"))
	wantSession := sha256.Sum256([]byte(result.Token))
	if string(repository.credentialHash) != string(wantCredential[:]) {
		t.Fatalf("credential hash = %x, want SHA-256 %x", repository.credentialHash, wantCredential)
	}
	if len(repository.sessionHash) != sha256.Size || string(repository.sessionHash) != string(wantSession[:]) {
		t.Fatalf("persisted session digest = %x, want SHA-256 %x", repository.sessionHash, wantSession)
	}
	if result.Token == "" || result.Session.Principal.ID != "principal-id" {
		t.Fatalf("issued session omitted its one-time token or principal: %#v", result)
	}
	if got := repository.absoluteUntil.Sub(repository.createdAt); got != 7*24*time.Hour {
		t.Fatalf("absolute lifetime = %s, want 168h", got)
	}
	if got := repository.idleExpiresAt.Sub(repository.createdAt); got != 8*time.Hour {
		t.Fatalf("initial idle lifetime = %s, want 8h", got)
	}
}

func TestAuthenticateSessionExtendsOnlyAValidOpaqueSession(t *testing.T) {
	repository := &sessionRepositoryStub{session: access.CredentialSession{
		ID: "session-id", CredentialID: "credential-id", Principal: access.Principal{ID: "principal-id", Active: true},
	}}
	service, err := NewSessionService(repository, time.Hour, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	token := "ss_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	if _, err := service.Authenticate(context.Background(), token); err != nil {
		t.Fatal(err)
	}
	wantHash := sha256.Sum256([]byte(token))
	if string(repository.authHash) != string(wantHash[:]) || repository.authIdleTTL != time.Hour || repository.authNow.IsZero() {
		t.Fatalf("Core did not validate hashed session with configured sliding expiry")
	}
	if _, err := service.Authenticate(context.Background(), "ss_bad"); !errors.Is(err, access.ErrUnauthorized) {
		t.Fatalf("malformed session token error = %v, want unauthorized", err)
	}
	if repository.authCalls != 1 {
		t.Fatalf("malformed session token reached persistence %d times", repository.authCalls-1)
	}
}

func TestSessionCanBeRevokedByItsOpaqueToken(t *testing.T) {
	repository := &sessionRepositoryStub{}
	service, err := NewSessionService(repository, time.Hour, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	token := "ss_BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"
	if err := service.Revoke(context.Background(), token); err != nil {
		t.Fatal(err)
	}
	wantHash := sha256.Sum256([]byte(token))
	if repository.revokeCalls != 1 || string(repository.revokeHash) != string(wantHash[:]) {
		t.Fatalf("revoke calls=%d hash=%x", repository.revokeCalls, repository.revokeHash)
	}
}

func TestSessionServiceRejectsInvalidLifetimes(t *testing.T) {
	for _, tc := range []struct {
		name     string
		idle     time.Duration
		absolute time.Duration
	}{
		{name: "zero idle", idle: 0, absolute: time.Hour},
		{name: "idle exceeds absolute", idle: 2 * time.Hour, absolute: time.Hour},
		{name: "absolute exceeds supported maximum", idle: time.Hour, absolute: 366 * 24 * time.Hour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewSessionService(&sessionRepositoryStub{}, tc.idle, tc.absolute); err == nil {
				t.Fatal("invalid session lifetime was accepted")
			}
		})
	}
}
