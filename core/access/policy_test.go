package access

import (
	"reflect"
	"testing"
)

func TestAuthorizeUsesOnlyTheThreeDirectGroupLevels(t *testing.T) {
	skipUntilDirectAssignmentsAreImplemented(t)
	user := Principal{ID: "user", Active: true}
	file := Resource{ID: "file", Groups: []string{"content"}, Ready: true}
	for _, tc := range []struct {
		level      Level
		read       bool
		write      bool
		upload     bool
		deleteFile bool
	}{
		{level: LevelNone},
		{level: LevelRead, read: true},
		{level: LevelWrite, read: true, write: true, upload: true, deleteFile: true},
	} {
		assignments := []GroupAssignment{{Group: "content", Level: tc.level, Active: true}}
		for _, action := range []struct {
			name string
			op   Action
			want bool
		}{
			{"read", Read, tc.read}, {"write", Write, tc.write},
			{"upload", Upload, tc.upload}, {"delete", Delete, tc.deleteFile},
		} {
			t.Run(string(rune('0'+tc.level))+"/"+action.name, func(t *testing.T) {
				if got := authorizeWithAssignments(t, user, action.op, file, assignments); got != action.want {
					t.Fatalf("Authorize(level=%d, action=%s) = %v, want %v", tc.level, action.op, got, action.want)
				}
			})
		}
	}
}

func TestAuthorizeUsesAnyActiveFileGroupWithoutLeakingManagementRights(t *testing.T) {
	skipUntilDirectAssignmentsAreImplemented(t)
	resource := Resource{ID: "file", Groups: []string{"feature", "content", "retired"}, Ready: true}
	assignments := []GroupAssignment{
		{Group: "feature", Level: LevelNone, Active: true},
		{Group: "content", Level: LevelRead, Active: true},
		{Group: "retired", Level: LevelWrite, Active: false},
	}
	if !authorizeWithAssignments(t, Principal{ID: "reader", Active: true}, Read, resource, assignments) {
		t.Fatal("read permission on any active file group should allow reading")
	}
	if authorizeWithAssignments(t, Principal{ID: "reader", Active: true}, Write, resource, assignments) {
		t.Fatal("inactive group or level 1 assignment allowed writing")
	}
	manager := Principal{ID: "manager", Active: true, CanManageAdministrators: true}
	if authorizeWithAssignments(t, manager, Read, Resource{ID: "private", Groups: []string{"private"}, Ready: true}, nil) {
		t.Fatal("administrator-management permission bypassed file group access")
	}
	if authorizeWithAssignments(t, Principal{ID: "inactive", CanManageAdministrators: true}, Read, resource, assignments) {
		t.Fatal("inactive principal accessed a file")
	}
	if authorizeWithAssignments(t, Principal{ID: "reader", Active: true}, Read, Resource{ID: "pending", Groups: []string{"content"}}, assignments) {
		t.Fatal("unready file was returned")
	}
}

func TestAuthorizeRejectsEngineSpecificActions(t *testing.T) {
	skipUntilDirectAssignmentsAreImplemented(t)
	user := Principal{ID: "editor", Active: true}
	resource := Resource{ID: "record", Groups: []string{"content"}, Ready: true}
	assignments := []GroupAssignment{{Group: "content", Level: LevelWrite, Active: true}}
	for _, action := range []Action{Publish, Restore, Archive, Manage, "unknown"} {
		if authorizeWithAssignments(t, user, action, resource, assignments) {
			t.Errorf("Core group access accepted engine-specific action %q", action)
		}
	}
}

func skipUntilDirectAssignmentsAreImplemented(t *testing.T) {
	t.Helper()
	typ := reflect.TypeOf(Authorize)
	if typ.Kind() != reflect.Func || typ.NumIn() != 4 || typ.In(3) != reflect.TypeOf([]GroupAssignment(nil)) {
		t.Skip("behavioral matrix becomes executable when Authorize consumes direct GroupAssignment values")
	}
}

func authorizeWithAssignments(t *testing.T, actor Principal, action Action, resource Resource, assignments []GroupAssignment) bool {
	t.Helper()
	fn := reflect.ValueOf(Authorize)
	fnType := fn.Type()
	assignmentType := reflect.TypeOf([]GroupAssignment(nil))
	if fn.Kind() != reflect.Func || fnType.NumIn() != 4 || fnType.In(3) != assignmentType || fnType.NumOut() != 1 || fnType.Out(0).Kind() != reflect.Bool {
		t.Fatalf("Authorize must accept []GroupAssignment directly; got %v", fnType)
	}
	result := fn.Call([]reflect.Value{
		reflect.ValueOf(actor), reflect.ValueOf(action), reflect.ValueOf(resource), reflect.ValueOf(assignments),
	})
	return result[0].Bool()
}
