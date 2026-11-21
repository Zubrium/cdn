package access

import (
	"errors"
	"strings"
)

var ErrInvalidGroupAssignment = errors.New("invalid group assignment")

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

func NewGroupAssignment(group string, level Level, active bool) (GroupAssignment, error) {
	if strings.TrimSpace(group) == "" || level > LevelWrite {
		return GroupAssignment{}, ErrInvalidGroupAssignment
	}
	return GroupAssignment{Group: group, Level: level, Active: active}, nil
}
