package access

import (
	"testing"

	domainaccess "github.com/Zubrium/cdn/core/domain/access"
)

func TestEffectiveLevelUsesTheHighestActiveAssignmentAcrossGroups(t *testing.T) {
	assignments := []domainaccess.GroupAssignment{
		{Group: "features", Level: domainaccess.LevelRead, Active: true},
		{Group: "content", Level: domainaccess.LevelWrite, Active: true},
		{Group: "disabled", Level: domainaccess.LevelWrite, Active: false},
		{Group: "invalid", Level: domainaccess.Level(3), Active: true},
	}
	for _, tc := range []struct {
		name   string
		groups []string
		want   domainaccess.Level
	}{
		{"read on one group", []string{"features"}, domainaccess.LevelRead},
		{"write on any active group", []string{"features", "content"}, domainaccess.LevelWrite},
		{"disabled group grants nothing", []string{"disabled"}, domainaccess.LevelNone},
		{"invalid level grants nothing", []string{"invalid"}, domainaccess.LevelNone},
		{"unknown group grants nothing", []string{"unknown"}, domainaccess.LevelNone},
		{"empty group set grants nothing", nil, domainaccess.LevelNone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := EffectiveLevel(assignments, tc.groups); got != tc.want {
				t.Fatalf("EffectiveLevel(%v) = %d, want %d", tc.groups, got, tc.want)
			}
		})
	}
}

func TestAuthorizeAppliesDirectLevelsWithoutAdministratorBypass(t *testing.T) {
	principal := domainaccess.Principal{ID: "admin", Active: true, CanManageAdministrators: true}
	resource := domainaccess.Resource{ID: "file", Ready: true, Groups: []string{"feature", "content", "disabled"}}
	assignments := []domainaccess.GroupAssignment{
		{Group: "feature", Level: domainaccess.LevelNone, Active: true},
		{Group: "content", Level: domainaccess.LevelRead, Active: true},
		{Group: "disabled", Level: domainaccess.LevelWrite, Active: false},
	}
	if !Authorize(principal, domainaccess.Read, resource, assignments) {
		t.Fatal("read assignment on any active file group should allow reading")
	}
	if Authorize(principal, domainaccess.Write, resource, assignments) {
		t.Fatal("level 1 or inactive group allowed writing")
	}
	if Authorize(principal, domainaccess.Read, domainaccess.Resource{ID: "private", Groups: []string{"private"}, Ready: true}, nil) {
		t.Fatal("administrator-management permission bypassed file access")
	}
	if Authorize(domainaccess.Principal{ID: "disabled", CanManageAdministrators: true}, domainaccess.Read, resource, assignments) {
		t.Fatal("inactive principal accessed a file")
	}
	if Authorize(principal, domainaccess.Read, domainaccess.Resource{ID: "pending", Groups: []string{"content"}}, assignments) {
		t.Fatal("unready file was returned")
	}
}

func TestAuthorizeAllowsOnlyCoreFileActions(t *testing.T) {
	principal := domainaccess.Principal{ID: "editor", Active: true}
	resource := domainaccess.Resource{ID: "file", Ready: true, Groups: []string{"content"}}
	assignments := []domainaccess.GroupAssignment{{Group: "content", Level: domainaccess.LevelWrite, Active: true}}
	for _, action := range []domainaccess.Action{"domain.publish", "domain.restore", "domain.archive", domainaccess.Manage, "unknown"} {
		if Authorize(principal, action, resource, assignments) {
			t.Errorf("Core access accepted product action %q", action)
		}
	}
}

func TestCanDelegateRequiresAdministratorManagementAndLevelTwo(t *testing.T) {
	for _, tc := range []struct {
		name            string
		active, manages bool
		own, requested  domainaccess.Level
		want            bool
	}{
		{"grant level 0", true, true, domainaccess.LevelWrite, domainaccess.LevelNone, true},
		{"grant level 1", true, true, domainaccess.LevelWrite, domainaccess.LevelRead, true},
		{"grant level 2", true, true, domainaccess.LevelWrite, domainaccess.LevelWrite, true},
		{"cannot exceed level 2", true, true, domainaccess.LevelWrite, domainaccess.Level(3), false},
		{"read access cannot delegate", true, true, domainaccess.LevelRead, domainaccess.LevelRead, false},
		{"management right alone cannot delegate", true, false, domainaccess.LevelWrite, domainaccess.LevelRead, false},
		{"inactive principal cannot delegate", false, true, domainaccess.LevelWrite, domainaccess.LevelRead, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := CanDelegate(tc.active, tc.manages, tc.own, tc.requested); got != tc.want {
				t.Fatalf("CanDelegate() = %v, want %v", got, tc.want)
			}
		})
	}
}
