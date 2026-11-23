package composition

import (
	"errors"
	"strings"
	"testing"
)

func TestSnapshotForkStageErrorAddsStageAndPreservesCause(t *testing.T) {
	want := errors.New("not found")
	got := snapshotForkStageError("source projection", want)
	if !errors.Is(got, want) || !strings.Contains(got.Error(), "source projection") {
		t.Fatalf("snapshotForkStageError() = %v, want wrapped source projection error", got)
	}
}
