package composition

import (
	"errors"
	"testing"

	platform "github.com/Zubrium/cdn/core/presentation"
	"github.com/Zubrium/cdn/pkg/contracts"
)

func TestCoreFileAdapterMapsForbiddenToNeutralContractError(t *testing.T) {
	err := mapCoreFileError(platform.ErrForbidden)
	if !errors.Is(err, contracts.ErrFileForbidden) {
		t.Fatalf("mapped error = %v, want contracts.ErrFileForbidden", err)
	}
}
