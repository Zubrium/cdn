package access

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
