package identity

import (
	"context"
	"errors"
	"time"

	appaccess "github.com/Zubrium/cdn/core/application/access"
	"github.com/Zubrium/cdn/core/domain/access"
)

var ErrInvalid = errors.New("invalid identity operation")
var ErrManagerInvariant = access.ErrManagerInvariant

// Repository is the domain persistence port implemented by Core
// infrastructure. The service owns application-level identity and delegation
// checks; the adapter repeats security-critical checks inside transactions.
type Repository interface{ access.IdentityRepository }

type Service struct{ repository Repository }

func NewService(repository Repository) (*Service, error) {
	if repository == nil {
		return nil, ErrInvalid
	}
	return &Service{repository: repository}, nil
}

func (s *Service) Bootstrap(ctx context.Context, configuredSecret string, groups map[string]access.Level) (string, bool, error) {
	if s == nil || s.repository == nil || ctx == nil {
		return "", false, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	return s.repository.Bootstrap(ctx, configuredSecret, groups)
}

func (s *Service) Authenticate(ctx context.Context, secret string) (access.Principal, error) {
	if s == nil || s.repository == nil || ctx == nil {
		return access.Principal{}, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return access.Principal{}, err
	}
	return s.repository.Authenticate(ctx, secret)
}

func (s *Service) AuthenticateHash(ctx context.Context, hash []byte) (access.Principal, error) {
	if s == nil || s.repository == nil || ctx == nil {
		return access.Principal{}, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return access.Principal{}, err
	}
	return s.repository.AuthenticateHash(ctx, hash)
}

func (s *Service) CreatePrincipal(ctx context.Context, actor access.Principal, name string, expires *time.Time) (string, string, error) {
	if err := s.requireManager(ctx, actor); err != nil {
		return "", "", err
	}
	return s.repository.CreatePrincipal(ctx, actor, name, expires)
}

func (s *Service) CreatePrincipalWithAccess(ctx context.Context, actor access.Principal, name string, expires *time.Time, manage bool, grants map[string]access.Level) (string, string, error) {
	if err := s.requireManager(ctx, actor); err != nil {
		return "", "", err
	}
	if err := s.requireWriteOnGroups(ctx, actor, grants); err != nil {
		return "", "", err
	}
	return s.repository.CreatePrincipalWithAccess(ctx, actor, name, expires, manage, grants)
}

func (s *Service) ListPrincipals(ctx context.Context, actor access.Principal) ([]access.Principal, error) {
	if err := s.requireManager(ctx, actor); err != nil {
		return nil, err
	}
	return s.repository.ListPrincipals(ctx, actor)
}

func (s *Service) SetPrincipalManagement(ctx context.Context, actor access.Principal, principalID string, enabled bool) error {
	if err := s.requireManager(ctx, actor); err != nil {
		return err
	}
	return s.repository.SetPrincipalManagement(ctx, actor, principalID, enabled)
}

func (s *Service) DeactivatePrincipal(ctx context.Context, actor access.Principal, principalID string) error {
	if err := s.requireManager(ctx, actor); err != nil {
		return err
	}
	return s.repository.DeactivatePrincipal(ctx, actor, principalID)
}

func (s *Service) IssueCredential(ctx context.Context, actor access.Principal, principalID string, expires *time.Time) (string, string, error) {
	if err := s.requireManager(ctx, actor); err != nil {
		return "", "", err
	}
	return s.repository.IssueCredential(ctx, actor, principalID, expires)
}

func (s *Service) ListCredentials(ctx context.Context, actor access.Principal, principalID string) ([]access.Credential, error) {
	if err := s.requireManager(ctx, actor); err != nil {
		return nil, err
	}
	return s.repository.ListCredentials(ctx, actor, principalID)
}

func (s *Service) RevokeCredential(ctx context.Context, actor access.Principal, credentialID string) error {
	if err := s.requireManager(ctx, actor); err != nil {
		return err
	}
	return s.repository.RevokeCredential(ctx, actor, credentialID)
}

func (s *Service) SetGroupAccess(ctx context.Context, actor access.Principal, principalID, group string, level access.Level) error {
	if err := s.requireManager(ctx, actor); err != nil {
		return err
	}
	if level > access.LevelWrite {
		return ErrInvalid
	}
	if err := s.RequireGroupLevel(ctx, actor, group, access.LevelWrite); err != nil {
		return err
	}
	return s.repository.SetGroupAccess(ctx, actor, principalID, group, level)
}

func (s *Service) GroupAccess(ctx context.Context, actor access.Principal, principalID, group string) (access.Level, error) {
	if err := s.requireManager(ctx, actor); err != nil {
		return access.LevelNone, err
	}
	if err := s.RequireGroupLevel(ctx, actor, group, access.LevelWrite); err != nil {
		return access.LevelNone, err
	}
	return s.repository.GroupAccess(ctx, actor, principalID, group)
}

func (s *Service) ListAccessGroups(ctx context.Context, actor access.Principal) ([]access.ConfiguredGroup, error) {
	if err := s.requireManager(ctx, actor); err != nil {
		return nil, err
	}
	return s.repository.ListAccessGroups(ctx, actor.ID)
}

func (s *Service) Assignments(ctx context.Context, principalID string) ([]access.GroupAssignment, error) {
	if s == nil || s.repository == nil || ctx == nil || principalID == "" {
		return nil, ErrInvalid
	}
	return s.repository.Assignments(ctx, principalID)
}

func (s *Service) RequireGroupLevel(ctx context.Context, actor access.Principal, group string, minimum access.Level) error {
	return s.RequireGroupsLevel(ctx, actor, []string{group}, minimum)
}

func (s *Service) RequireGroupsLevel(ctx context.Context, actor access.Principal, groups []string, minimum access.Level) error {
	if s == nil || s.repository == nil || ctx == nil || len(groups) == 0 ||
		(minimum != access.LevelRead && minimum != access.LevelWrite) {
		return ErrInvalid
	}
	for _, group := range groups {
		if !appaccess.ValidGroupKey(group) {
			return ErrInvalid
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	assignments, err := s.repository.Assignments(ctx, actor.ID)
	if err != nil {
		return err
	}
	action := access.Read
	if minimum == access.LevelWrite {
		action = access.Write
	}
	for _, group := range groups {
		if !appaccess.AuthorizeGroup(actor, action, group, assignments) {
			return access.ErrForbidden
		}
	}
	return nil
}

func (s *Service) RequireAdministratorManagement(ctx context.Context, actor access.Principal) error {
	return s.requireManager(ctx, actor)
}

func (s *Service) RecoverBootstrapCredential(ctx context.Context) (string, error) {
	if s == nil || s.repository == nil || ctx == nil {
		return "", ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return s.repository.RecoverBootstrapCredential(ctx)
}

func (s *Service) requireManager(ctx context.Context, actor access.Principal) error {
	if s == nil || s.repository == nil || ctx == nil {
		return ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !actor.Active || actor.ID == "" {
		return access.ErrUnauthorized
	}
	if !actor.CanManageAdministrators {
		return access.ErrForbidden
	}
	return nil
}

func (s *Service) requireWriteOnGroups(ctx context.Context, actor access.Principal, grants map[string]access.Level) error {
	if len(grants) == 0 {
		return nil
	}
	assignments, err := s.repository.Assignments(ctx, actor.ID)
	if err != nil {
		return err
	}
	for group := range grants {
		if !appaccess.AuthorizeGroup(actor, access.Write, group, assignments) {
			return access.ErrForbidden
		}
	}
	return nil
}
