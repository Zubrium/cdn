//go:build !full || devfixtures || router

package composition

import "testing"

func TestAdminUIHandlerIsNotCompiledIntoOtherProfiles(t *testing.T) {
	handler, err := newAdminUIHandler()
	if err != nil {
		t.Fatalf("disabled Admin UI handler returned an error: %v", err)
	}
	if handler != nil {
		t.Fatal("Admin UI handler must not be compiled into non-full profiles")
	}
}
