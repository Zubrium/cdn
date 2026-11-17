package platform

import (
	"context"
	"errors"
	"testing"
)

var _ IdentityAdminCapability = (*identityAdminCapability)(nil)

func TestAdminCapabilityRejectsUnavailableRuntime(t *testing.T) {
	if _, err := (&identityAdminCapability{}).ListPrincipals(context.Background(), Session{}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("ListPrincipals error = %v, want %v", err, ErrUnavailable)
	}
}
