package features

import (
	"context"
	"testing"
)

func TestFeatureListingRejectsRetiredContentModes(t *testing.T) {
	service := NewService(nil)
	for _, retired := range []string{"single", "collection"} {
		t.Run(retired, func(t *testing.T) {
			_, _, err := service.List(context.Background(), "", retired, "", "", 10)
			if err == nil {
				t.Fatalf("retired CMS content mode %q was accepted as a feature type", retired)
			}
			validation, ok := err.(*ValidationError)
			if !ok || validation.Message != "type must be omitted or form" {
				t.Fatalf("unexpected error for retired mode %q: %v", retired, err)
			}
		})
	}
}
