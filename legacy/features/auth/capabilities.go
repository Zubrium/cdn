package auth

import (
	"websiteengine/pkg/principal"
)

// Capabilities are the server-side role checks shared by HTTP handlers. They
// intentionally mirror the role matrix documented in README.md.

// Site configuration is restricted to administrators and moderators. The
// legacy staff role remains a content-only role.
func IsSiteManager(role any) bool {
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
	return r == principal.RoleAdmin || r == RoleModerator
}

func CanManageFeatures(role string) bool { return IsSiteManager(role) }

func CanPublishContent(role string) bool { return ValidRole(role) }

func CanPromoteContent(role string) bool { return IsSiteManager(role) }

func CanManageMedia(role string) bool { return ValidRole(role) }

func CanDeleteMedia(role string) bool { return IsAdmin(role) }

func CanViewSubmissions(role string) bool { return ValidRole(role) }

func CanManageSubmissions(role string) bool { return IsSiteManager(role) }
