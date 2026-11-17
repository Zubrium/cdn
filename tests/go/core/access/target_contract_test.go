package access

import (
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
	authorize := reflect.TypeOf(Authorize)
	if authorize.Kind() != reflect.Func || authorize.NumIn() != 4 || authorize.In(3) != reflect.TypeOf([]GroupAssignment(nil)) {
		t.Fatalf("Authorize must consume direct group assignments, got %v", authorize)
	}
	for _, action := range []Action{Publish, Restore, Archive, Manage} {
		if ValidAction(action) {
			t.Errorf("Core accepted engine-specific action %q", action)
		}
	}
}
