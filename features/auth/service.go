package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"websiteengine/pkg/principal"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

type Service struct {
	repo      *Repository
	listLimit int
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

// Authenticate resolves a bearer secret into an active token principal.
func (s *Service) Authenticate(ctx context.Context, secret string) (Principal, error) {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return Principal{}, ErrUnauthorized
	}
	p, err := s.repo.FindActiveByHash(ctx, HashSecret(secret))
	if err != nil {
		if errors.Is(err, ErrUnauthorized) {
			return Principal{}, ErrUnauthorized
		}
		return Principal{}, fmt.Errorf("authenticate token: %w", err)
	}
	s.repo.Touch(ctx, p.TokenID)
	return p, nil
}

// Bootstrap creates the initial administrator token from the environment,
// only when no active admin token exists. It never resets or escalates an
// existing token.
func (s *Service) Bootstrap(ctx context.Context, secret string) (bool, error) {
	if secret == "" {
		return false, nil
	}
	exists, err := s.repo.HasActiveAdmin(ctx)
	if err != nil {
		return false, err
	}
	if exists {
		return false, nil
	}
	hash := HashSecret(secret)
	// A revoked/expired bootstrap token is never recreated or re-enabled using
	// the same environment secret. Bootstrap is strictly one-time.
	used, err := s.repo.HashExists(ctx, hash)
	if err != nil {
		return false, err
	}
	if used {
		return false, nil
	}
	_, err = s.repo.Create(ctx, uuid.New(), "bootstrap admin", hash, string(RoleAdmin), nil, nil)
	if err != nil {
		// Two instances starting at once can race; the unique token hash makes
		// that safe, and neither instance should fail because of it.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (s *Service) Create(ctx context.Context, actor Principal, name, role string, expiresAt *time.Time) (NewToken, error) {
	if !IsStaff(actor.Role) {
		return NewToken{}, ErrForbidden
	}
	name, ok := CleanName(name)
	if !ok {
		return NewToken{}, ErrValidation
	}
	if !ValidRole(role) {
		return NewToken{}, ErrValidation
	}
	if actor.Role == RoleModerator && principal.Role(role) != principal.RoleUser {
		return NewToken{}, ErrForbidden
	}
	if expiresAt != nil {
		expiresAt = ptr(expiresAt.UTC())
		if !expiresAt.After(time.Now().Add(time.Minute)) {
			return NewToken{}, ErrValidation
		}
	}

	secret, err := NewSecret()
	if err != nil {
		return NewToken{}, err
	}
	creator := actor.TokenID
	token, err := s.repo.Create(ctx, uuid.New(), name, HashSecret(secret), string(role), &creator, expiresAt)
	if err != nil {
		return NewToken{}, err
	}
	return NewToken{Token: token, Secret: secret}, nil
}

func (s *Service) List(ctx context.Context, actor Principal) ([]Token, error) {
	if !IsStaff(actor.Role) {
		return nil, ErrForbidden
	}
	if s.listLimit < 1 {
		return nil, errors.New("token list limit is not configured")
	}
	var creator *uuid.UUID
	if actor.Role == RoleModerator {
		id := actor.TokenID
		creator = &id
	}
	return s.repo.List(ctx, creator, s.listLimit)
}

func (s *Service) SetListLimit(limit int) error {
	if limit < 1 {
		return errors.New("token list limit must be positive")
	}
	s.listLimit = limit
	return nil
}

func (s *Service) Get(ctx context.Context, actor Principal, id uuid.UUID) (Token, error) {
	if !IsStaff(actor.Role) {
		return Token{}, ErrForbidden
	}
	token, err := s.repo.Get(ctx, id)
	if err != nil {
		return Token{}, err
	}
	if actor.Role == RoleModerator && (token.CreatedBy == nil || *token.CreatedBy != actor.TokenID) {
		return Token{}, ErrNotFound
	}
	return token, nil
}

func (s *Service) Revoke(ctx context.Context, actor Principal, id uuid.UUID) error {
	if !IsStaff(actor.Role) {
		return ErrForbidden
	}
	token, err := s.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	if actor.Role == RoleModerator && (token.CreatedBy == nil || *token.CreatedBy != actor.TokenID) {
		return ErrNotFound
	}
	return s.repo.Revoke(ctx, id)
}

func (s *Service) LookupForBootstrap(ctx context.Context) (bool, error) {
	return s.repo.HasActiveAdmin(ctx)
}

func ptr[T any](v T) *T { return &v }

// IsSentinels helps handlers distinguish policy errors from infrastructure
// failures without exposing database details.
func IsSentinels(err error) bool {
	return errors.Is(err, ErrUnauthorized) || errors.Is(err, ErrForbidden) ||
		errors.Is(err, ErrNotFound) || errors.Is(err, ErrConflict) ||
		errors.Is(err, ErrValidation)
}
