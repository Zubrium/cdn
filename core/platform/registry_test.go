package platform

import (
	"errors"
	"testing"
)

type testModule struct{ value int }

func (m testModule) Operations() []Operation { return nil }

func TestRegistryRejectsDuplicateAndReportsUnavailable(t *testing.T) {
	r := NewRegistry()
	if err := r.Register("website", testModule{42}); err != nil {
		t.Fatal(err)
	}
	if err := r.Register("website", testModule{43}); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate: %v", err)
	}
	value, err := r.Lookup("website")
	if err != nil || value.(testModule).value != 42 {
		t.Fatalf("lookup: %v %v", value, err)
	}
	if _, err := r.Lookup("forms"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("missing: %v", err)
	}
	if err := r.Register("", testModule{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty name: %v", err)
	}
	if err := r.Register("content", nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("nil service: %v", err)
	}
}
