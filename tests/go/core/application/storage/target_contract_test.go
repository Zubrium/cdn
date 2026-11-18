package storage

import (
	"context"
	"io"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/Zubrium/cdn/core/domain/access"
)

type coreV2StorageContract interface {
	Create(context.Context, access.Principal, string, []string, map[string]string, string, int64, io.Reader) (string, error)
	Read(context.Context, access.Principal, string) (io.ReadCloser, Object, error)
	ReadRange(context.Context, access.Principal, string, int64, int64) (io.ReadCloser, Object, error)
	Stat(context.Context, access.Principal, string) (Object, error)
	Update(context.Context, access.Principal, string, int64, map[string]string) error
	AddToGroup(context.Context, access.Principal, string, string, int64) error
	RemoveFromGroup(context.Context, access.Principal, string, string, int64) error
	Delete(context.Context, access.Principal, string, int64) error
}

func TestStorageModelAndServiceExcludeRetiredFileSemantics(t *testing.T) {
	for typ, retired := range map[reflect.Type][]string{
		reflect.TypeOf(Object{}): {"Owner", "Group", "Type", "Public", "Version", "DeletedAt", "Name"},
	} {
		for _, field := range retired {
			if _, ok := typ.FieldByName(field); ok {
				t.Errorf("%s contains retired field %s", typ.Name(), field)
			}
		}
	}
	if _, ok := reflect.TypeOf(Object{}).FieldByName("Groups"); !ok {
		t.Fatal("storage object must carry all static access groups")
	}
	metadata, ok := reflect.TypeOf(Object{}).FieldByName("Metadata")
	if !ok || metadata.Type != reflect.TypeOf(map[string]string{}) {
		t.Fatal("storage object must expose only generic metadata")
	}
	for typ, methods := range map[reflect.Type][]string{
		reflect.TypeOf(Service{}):                     {"Versions", "ReadVersion", "Replace", "Restore"},
		reflect.TypeOf((*MetadataReader)(nil)).Elem(): {"ListVersions", "FindVersion"},
	} {
		for _, name := range methods {
			if _, ok := typ.MethodByName(name); ok {
				t.Errorf("%s exposes retired operation %s", typ.Name(), name)
			}
		}
	}
}

func TestStorageServiceAndRepositoryUseDirectAssignments(t *testing.T) {
	want := reflect.TypeOf((*coreV2StorageContract)(nil)).Elem()
	got := reflect.TypeOf((*Service)(nil))
	var missing []string
	for i := 0; i < want.NumMethod(); i++ {
		method := want.Method(i)
		implementation, ok := got.MethodByName(method.Name)
		if !ok || !sameStorageMethodSignature(implementation.Type, method.Type) {
			missing = append(missing, method.Name)
		}
	}
	if len(missing) != 0 {
		sort.Strings(missing)
		t.Fatalf("storage Service does not implement Core v2 contract: %s", strings.Join(missing, ", "))
	}
	assignments := reflect.TypeOf([]access.GroupAssignment(nil))
	reader := reflect.TypeOf((*MetadataReader)(nil)).Elem()
	method, ok := reader.MethodByName("Assignments")
	if !ok || method.Type.NumIn() != 2 || method.Type.In(0) != reflect.TypeOf((*context.Context)(nil)).Elem() || method.Type.In(1).Kind() != reflect.String || method.Type.NumOut() != 2 || method.Type.Out(0) != assignments || method.Type.Out(1) != reflect.TypeOf((*error)(nil)).Elem() {
		t.Fatalf("MetadataReader must return current direct group assignments; got Assignments=%v", method)
	}
	for _, retired := range []string{"Grants", "ListVersions", "FindVersion"} {
		if _, ok := reader.MethodByName(retired); ok {
			t.Errorf("MetadataReader exposes retired method %s", retired)
		}
	}
}

func sameStorageMethodSignature(implementation, contract reflect.Type) bool {
	if implementation.NumIn() != contract.NumIn()+1 || implementation.NumOut() != contract.NumOut() || implementation.IsVariadic() != contract.IsVariadic() {
		return false
	}
	if implementation.In(0) != reflect.TypeOf((*Service)(nil)) {
		return false
	}
	for i := 0; i < contract.NumIn(); i++ {
		if implementation.In(i+1) != contract.In(i) {
			return false
		}
	}
	for i := 0; i < contract.NumOut(); i++ {
		if implementation.Out(i) != contract.Out(i) {
			return false
		}
	}
	return true
}
