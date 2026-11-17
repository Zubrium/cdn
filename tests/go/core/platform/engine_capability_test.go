package platform

import (
	"context"
	"errors"
	"testing"
)

var _ EngineIdentityCapability = (*engineIdentityCapability)(nil)

func TestEngineIdentityCapabilityRejectsUnavailableRuntime(t *testing.T) {
	_, _, err := (&engineIdentityCapability{}).Authenticate(context.Background(), "secret")
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Authenticate error = %v, want %v", err, ErrUnavailable)
	}
}

func TestEngineFilesCapabilityRejectsUnavailableRuntime(t *testing.T) {
	_, _, err := (&engineFilesCapability{}).ReadFile(context.Background(), Session{}, "00000000-0000-4000-8000-000000000001")
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("ReadFile error = %v, want %v", err, ErrUnavailable)
	}
}
