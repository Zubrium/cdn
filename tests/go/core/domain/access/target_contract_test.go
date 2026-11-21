package access

import (
	"errors"
	"reflect"
	"testing"
)

func TestIdentityAndResourceContractsContainNoRoleOwnerOrVisibility(t *testing.T) {
	for typ, retired := range map[reflect.Type][]string{
		reflect.TypeOf(Principal{}):       {"Kind", "Type", "Role", "Roles", "Owner"},
		reflect.TypeOf(Resource{}):        {"Group", "Type", "Owner", "Public"},
		reflect.TypeOf(GroupAssignment{}): {"Role", "Action", "Type", "Object"},
	} {
		for _, field := range retired {
			if _, ok := typ.FieldByName(field); ok {
				t.Errorf("%s contains retired field %s", typ.Name(), field)
			}
		}
	}
	if _, ok := reflect.TypeOf(Resource{}).FieldByName("Groups"); !ok {
		t.Fatal("file access resource must support multiple groups")
	}
}

func TestNewGroupAssignmentRejectsInvalidValues(t *testing.T) {
	for _, input := range []struct {
		group string
		level Level
	}{
		{group: " ", level: LevelRead},
		{group: "content", level: Level(3)},
	} {
		if _, err := NewGroupAssignment(input.group, input.level, false); !errors.Is(err, ErrInvalidGroupAssignment) {
			t.Errorf("NewGroupAssignment(%q, %d) error = %v, want ErrInvalidGroupAssignment", input.group, input.level, err)
		}
	}
}

func TestNewGroupAssignmentPreservesInactiveAssignment(t *testing.T) {
	assignment, err := NewGroupAssignment("content", LevelWrite, false)
	if err != nil {
		t.Fatal(err)
	}
	if assignment.Group != "content" || assignment.Level != LevelWrite || assignment.Active {
		t.Fatalf("unexpected assignment: %#v", assignment)
	}
}
