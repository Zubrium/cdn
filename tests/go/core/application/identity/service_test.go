package identity

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Zubrium/cdn/core/domain/access"
)

type identityRepositoryStub struct {
	access.IdentityRepository
	assignments      []access.GroupAssignment
	created          int
	createdWith      int
	groupAccessCalls int
}

func (s *identityRepositoryStub) Assignments(context.Context, string) ([]access.GroupAssignment, error) {
	return append([]access.GroupAssignment(nil), s.assignments...), nil
}

func (s *identityRepositoryStub) CreatePrincipal(context.Context, access.Principal, string, *time.Time) (string, string, error) {
	s.created++
	return "principal-id", "one-time-secret", nil
}

func (s *identityRepositoryStub) CreatePrincipalWithAccess(context.Context, access.Principal, string, *time.Time, bool, map[string]access.Level) (string, string, error) {
	s.createdWith++
	return "principal-id", "one-time-secret", nil
}

func (s *identityRepositoryStub) SetGroupAccess(context.Context, access.Principal, string, string, access.Level) error {
	s.groupAccessCalls++
	return nil
}

func TestRequireGroupsLevelUsesCurrentActiveGroupAssignments(t *testing.T) {
	service, err := NewService(&identityRepositoryStub{assignments: []access.GroupAssignment{
		{Group: "content", Level: access.LevelRead, Active: true},
		{Group: "provider", Level: access.LevelWrite, Active: true},
		{Group: "removed", Level: access.LevelWrite, Active: false},
	}})
	if err != nil {
		t.Fatal(err)
	}
	actor := access.Principal{ID: "admin-id", Active: true}
	cases := []struct {
		name    string
		groups  []string
		minimum access.Level
		wantErr error
	}{
		{name: "read permitted", groups: []string{"content"}, minimum: access.LevelRead},
		{name: "write permitted", groups: []string{"provider"}, minimum: access.LevelWrite},
		{name: "write denied at read level", groups: []string{"content"}, minimum: access.LevelWrite, wantErr: access.ErrForbidden},
		{name: "inactive group denied", groups: []string{"removed"}, minimum: access.LevelRead, wantErr: access.ErrForbidden},
		{name: "all groups required", groups: []string{"provider", "content"}, minimum: access.LevelWrite, wantErr: access.ErrForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := service.RequireGroupsLevel(context.Background(), actor, tc.groups, tc.minimum)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("RequireGroupsLevel() error = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestSetGroupAccessRejectsUnknownLevelBeforeCheckingGroupAuthorization(t *testing.T) {
	repository := &identityRepositoryStub{}
	service, err := NewService(repository)
	if err != nil {
		t.Fatal(err)
	}
	actor := access.Principal{ID: "manager-id", Active: true, CanManageAdministrators: true}
	err = service.SetGroupAccess(context.Background(), actor, "target-id", "content", access.Level(3))
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid access level returned %v, want invalid operation", err)
	}
	if repository.groupAccessCalls != 0 {
		t.Fatalf("invalid access level reached the repository %d times", repository.groupAccessCalls)
	}
}

func TestCreatePrincipalWithAccessRejectsGrantOutsideCallerWriteGroups(t *testing.T) {
	repository := &identityRepositoryStub{assignments: []access.GroupAssignment{{Group: "content", Level: access.LevelRead, Active: true}}}
	service, err := NewService(repository)
	if err != nil {
		t.Fatal(err)
	}
	actor := access.Principal{ID: "manager-id", Active: true, CanManageAdministrators: true}
	_, _, err = service.CreatePrincipalWithAccess(context.Background(), actor, "Service", nil, false, map[string]access.Level{"content": access.LevelRead})
	if !errors.Is(err, access.ErrForbidden) {
		t.Fatalf("grant outside caller's level-2 groups error = %v, want forbidden", err)
	}
	if repository.createdWith != 0 {
		t.Fatalf("repository was called for a denied grant: %d calls", repository.createdWith)
	}

	repository.assignments[0].Level = access.LevelWrite
	id, secret, err := service.CreatePrincipalWithAccess(context.Background(), actor, "Service", nil, false, map[string]access.Level{"content": access.LevelRead})
	if err != nil || id != "principal-id" || secret != "one-time-secret" || repository.createdWith != 1 {
		t.Fatalf("authorized account creation = (%q, %q, %v), calls=%d", id, secret, err, repository.createdWith)
	}
}

func TestAdministratorManagementRequiresActiveManagingPrincipal(t *testing.T) {
	service, err := NewService(&identityRepositoryStub{})
	if err != nil {
		t.Fatal(err)
	}
	cases := []access.Principal{
		{ID: "", Active: true, CanManageAdministrators: true},
		{ID: "user-id", Active: false, CanManageAdministrators: true},
		{ID: "user-id", Active: true, CanManageAdministrators: false},
	}
	for _, actor := range cases {
		if err := service.RequireAdministratorManagement(context.Background(), actor); !errors.Is(err, access.ErrUnauthorized) && !errors.Is(err, access.ErrForbidden) {
			t.Fatalf("management check for %+v returned %v", actor, err)
		}
	}
	if err := service.RequireAdministratorManagement(context.Background(), access.Principal{ID: "admin-id", Active: true, CanManageAdministrators: true}); err != nil {
		t.Fatalf("valid manager rejected: %v", err)
	}
}
