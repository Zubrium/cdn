package access

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

// Level is the directly assigned access level for one configured group.
type Level uint8

const (
	LevelNone Level = iota
	LevelRead
	LevelWrite
)

type GroupAssignment struct {
	Group  string
	Level  Level
	Active bool
}

// EffectiveLevel returns the maximum valid direct assignment over the file's
// currently configured groups. Missing or invalid assignments grant nothing.
func EffectiveLevel(assignments []GroupAssignment, groups []string) Level {
	var result Level
	for _, assignment := range assignments {
		if !assignment.Active || assignment.Group == "" || assignment.Level > LevelWrite {
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
