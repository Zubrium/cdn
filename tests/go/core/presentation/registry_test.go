package presentation

import (
	"errors"
	"testing"
)

func TestRegistryRejectsDuplicateAndReportsUnavailable(t *testing.T) {
	r := NewRegistry()
	if err := r.Register("owner.example"); err != nil {
		t.Fatal(err)
	}
	if err := r.Register("owner.example"); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate: %v", err)
	}
	if err := r.Lookup("owner.example"); err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if err := r.Lookup("forms"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("missing: %v", err)
	}
	if err := r.Register(""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty name: %v", err)
	}
}
