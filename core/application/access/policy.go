// Package access contains Core use-case policy over domain access values.
package access

import domainaccess "github.com/Zubrium/cdn/core/domain/access"

func ValidGroupKey(value string) bool {
	if value == "" || len(value) > 64 || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for i := 1; i < len(value); i++ {
		c := value[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_' && c != '-' {
			return false
		}
	}
	return true
}

func EffectiveLevel(assignments []domainaccess.GroupAssignment, groups []string) domainaccess.Level {
	var result domainaccess.Level
	for _, assignment := range assignments {
		if !assignment.Active || assignment.Group == "" || assignment.Level > domainaccess.LevelWrite {
			continue
		}
		for _, group := range groups {
			if group == assignment.Group && assignment.Level > result {
				result = assignment.Level
			}
		}
	}
	return result
}

func ValidAction(action domainaccess.Action) bool {
	switch action {
	case domainaccess.Read, domainaccess.Write, domainaccess.Delete, domainaccess.Upload:
		return true
	default:
		return false
	}
}

// Authorize grants read at level 1 and read/write/delete/upload at level 2.
// Administrator management never bypasses file-group permissions.
func Authorize(principal domainaccess.Principal, action domainaccess.Action, resource domainaccess.Resource, assignments []domainaccess.GroupAssignment) bool {
	if !principal.Active || principal.ID == "" || !resource.Ready || resource.ID == "" || !ValidAction(action) {
		return false
	}
	level := EffectiveLevel(assignments, resource.Groups)
	if action == domainaccess.Read {
		return level >= domainaccess.LevelRead
	}
	return level >= domainaccess.LevelWrite
}

func AuthorizeGroup(principal domainaccess.Principal, action domainaccess.Action, group string, assignments []domainaccess.GroupAssignment) bool {
	if !principal.Active || principal.ID == "" || !ValidGroupKey(group) || !ValidAction(action) {
		return false
	}
	level := EffectiveLevel(assignments, []string{group})
	if action == domainaccess.Read {
		return level >= domainaccess.LevelRead
	}
	return level >= domainaccess.LevelWrite
}

func CanDelegate(active, managesAdministrators bool, own, requested domainaccess.Level) bool {
	return active && managesAdministrators && own == domainaccess.LevelWrite && requested <= domainaccess.LevelWrite
}
