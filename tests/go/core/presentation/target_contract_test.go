package presentation

import (
	"reflect"
	"testing"
)

func TestUserAndFileContractsMatchCoreV2(t *testing.T) {
	for typ, retired := range map[reflect.Type][]string{
		reflect.TypeOf(Principal{}): {"Kind", "Type", "Role", "Roles"},
		reflect.TypeOf(File{}):      {"Owner", "OwnerID", "Public", "Visibility", "Version", "Group", "Type", "Name"},
		reflect.TypeOf(Group{}):     {"Owner", "OwnerID", "Name"},
	} {
		for _, field := range retired {
			if _, ok := typ.FieldByName(field); ok {
				t.Errorf("%s contains retired field %s", typ.Name(), field)
			}
		}
	}
	if _, ok := reflect.TypeOf(File{}).FieldByName("Groups"); !ok {
		t.Fatal("file contract must expose multiple access groups")
	}
	metadata, ok := reflect.TypeOf(File{}).FieldByName("Metadata")
	if !ok || metadata.Type != reflect.TypeOf(map[string]string{}) {
		t.Fatal("file contract must expose generic string metadata without product-specific fields")
	}
	management, ok := reflect.TypeOf(Principal{}).FieldByName("CanManageAdministrators")
	if !ok || management.Type.Kind() != reflect.Bool {
		t.Fatal("principal contract must expose the individual administrator-management permission")
	}
}

func TestRuntimeExposesNoRetiredCoreOperations(t *testing.T) {
	runtime := reflect.TypeOf((*Runtime)(nil))
	for _, method := range []string{
		"ReplaceFile", "FileVersions", "ReadFileVersion", "RestoreFileVersion", "ReferencesFor",
		"CreateRole", "ListRoles", "AssignRole", "RemoveRole", "DeleteRole", "RenameRole",
		"AddGrant", "ListGrants", "RemoveGrant", "CreateGroup", "RenameGroup", "DeleteGroup",
	} {
		if _, ok := runtime.MethodByName(method); ok {
			t.Errorf("runtime exposes retired operation %s", method)
		}
	}
	for _, method := range []string{"CreateFile", "UpdateFile"} {
		m, ok := runtime.MethodByName(method)
		if !ok {
			continue
		}
		for i := 0; i < m.Type.NumIn(); i++ {
			if m.Type.In(i).Kind() == reflect.Bool {
				t.Errorf("%s exposes a boolean visibility switch", method)
			}
		}
	}
}
