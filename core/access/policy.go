// Package access contains the Core's direct, group-based file policy.
package access

import "errors"

var ErrForbidden = errors.New("access forbidden")
var ErrUnauthorized = errors.New("unauthorized")

type Action string

const (
	Read   Action = "read"
	Write  Action = "write"
	Delete Action = "delete"
	Upload Action = "upload"

	// Product actions are intentionally not authorized by Core. Engines own
	// publication, restoration, workflow, and other product-specific rules.
	Publish Action = "publish"
	Restore Action = "restore"
	Archive Action = "archive"
	Manage  Action = "manage"
)

func ValidAction(action Action) bool {
	switch action {
	case Read, Write, Delete, Upload:
		return true
	default:
		return false
	}
}

type Principal struct {
	ID                      string
	Active                  bool
	CanManageAdministrators bool
}

type Resource struct {
	ID     string
	Ready  bool
	Groups []string
}

// Authorize grants read at level 1 and read/write/delete/upload at level 2.
// It never treats administrator management as a file-access bypass.
func Authorize(principal Principal, action Action, resource Resource, assignments []GroupAssignment) bool {
	if !principal.Active || principal.ID == "" || !resource.Ready || resource.ID == "" || !ValidAction(action) {
		return false
	}
	level := EffectiveLevel(assignments, resource.Groups)
	if action == Read {
		return level >= LevelRead
	}
	return level >= LevelWrite
}

func AuthorizeGroup(principal Principal, action Action, group string, assignments []GroupAssignment) bool {
	if !principal.Active || principal.ID == "" || group == "" || !ValidAction(action) {
		return false
	}
	level := EffectiveLevel(assignments, []string{group})
	if action == Read {
		return level >= LevelRead
	}
	return level >= LevelWrite
}

// CanDelegate allows an administrator manager to set any valid direct level
// only in groups where they themselves have level 2.
func CanDelegate(active, managesAdministrators bool, own, requested Level) bool {
	return active && managesAdministrators && own == LevelWrite && requested <= LevelWrite
}
