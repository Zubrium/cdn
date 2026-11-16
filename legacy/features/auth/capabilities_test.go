package auth

import (
	"testing"
)

func TestContentAndSiteManagementRoleMatrix(t *testing.T) {
	for _, role := range []string{"admin", "moderator", "staff", "user"} {
		if !CanPublishContent(role) || !CanManageMedia(role) || !CanViewSubmissions(role) {
			t.Errorf("role %q should publish content, manage media and view submissions", role)
		}
	}
	for _, role := range []string{"admin", "moderator"} {
		if !CanManageFeatures(role) || !CanPromoteContent(role) || !IsSiteManager(role) || !CanManageSubmissions(role) {
			t.Errorf("role %q should manage site settings, submissions and promote content", role)
		}
	}
	for _, role := range []string{"staff", "user"} {
		if CanManageFeatures(role) || CanPromoteContent(role) || IsSiteManager(role) || CanManageSubmissions(role) {
			t.Errorf("role %q must not manage site settings or promote content", role)
		}
		if CanDeleteMedia(role) {
			t.Errorf("role %q must not permanently delete shared media", role)
		}
	}
	if !CanDeleteMedia("admin") || CanDeleteMedia("moderator") {
		t.Fatal("only admin can permanently delete shared media")
	}
}
