package media

import (
	"errors"
	"testing"
)

func TestNewV2HandlerRequiresNeutralCoreFilesContract(t *testing.T) {
	if _, err := NewV2Handler(nil, nil, nil, 1, 1); !errors.Is(err, ErrNotReady) {
		t.Fatalf("handler with unavailable DataBus files = %v, want ErrNotReady", err)
	}
}
