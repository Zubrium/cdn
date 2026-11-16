package integration

import (
	"context"
	"testing"
)

func TestSnapshotFileRemovalGuardKeepsUnrelatedStructure(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	fileID := "a708456b-d234-409e-b324-c45eafce77a6"
	original := `{"config":{"items":[{"media":"a708456b-d234-409e-b324-c45eafce77a6","description":"keep"},"a708456b-d234-409e-b324-c45eafce77a6"],"number":7},"builds":[]}`
	for _, tc := range []struct {
		name, updated string
		allowed       bool
	}{
		{"unchanged", original, true},
		{"clear one field", `{"config":{"items":[{"media":"","description":"keep"},"a708456b-d234-409e-b324-c45eafce77a6"],"number":7},"builds":[]}`, true},
		{"clear both", `{"config":{"items":[{"media":"","description":"keep"},""],"number":7},"builds":[]}`, true},
		{"remove field", `{"config":{"items":[{"description":"keep"},"a708456b-d234-409e-b324-c45eafce77a6"],"number":7},"builds":[]}`, false},
		{"change text", `{"config":{"items":[{"media":"","description":"lost"},"a708456b-d234-409e-b324-c45eafce77a6"],"number":7},"builds":[]}`, false},
		{"remove array entry", `{"config":{"items":[{"media":"","description":"keep"}],"number":7},"builds":[]}`, false},
		{"different ID", `{"config":{"items":[{"media":"wrong","description":"keep"},""],"number":7},"builds":[]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got bool
			if err := e.pool.QueryRow(ctx, `SELECT allow_file_reference_clear($1::jsonb,$2::jsonb,$3)`, original, tc.updated, fileID).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if got != tc.allowed {
				t.Fatalf("allowed=%v want %v", got, tc.allowed)
			}
		})
	}
}
