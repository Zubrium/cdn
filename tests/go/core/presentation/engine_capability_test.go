package presentation

import (
	"context"
	"errors"
	"testing"
)

var _ IdentityCapability = (*identityCapability)(nil)

func TestIdentityCapabilityRejectsUnavailableRuntime(t *testing.T) {
	_, _, err := (&identityCapability{}).Authenticate(context.Background(), "secret")
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Authenticate error = %v, want %v", err, ErrUnavailable)
	}
}

func TestFilesCapabilityRejectsUnavailableRuntime(t *testing.T) {
	_, _, err := (&filesCapability{}).ReadFile(context.Background(), Session{}, "00000000-0000-4000-8000-000000000001")
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("ReadFile error = %v, want %v", err, ErrUnavailable)
	}
}
