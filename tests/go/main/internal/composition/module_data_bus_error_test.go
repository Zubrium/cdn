package composition

import (
	"errors"
	"testing"

	platform "github.com/Zubrium/cdn/core/presentation"
	"github.com/Zubrium/cdn/pkg/contracts"
)

func TestCoreRecordErrorsMapToNeutralRecordContracts(t *testing.T) {
	for _, test := range []struct {
		name string
		core error
		want error
	}{
		{name: "not found", core: platform.ErrModuleDataNotFound, want: contracts.ErrRecordNotFound},
		{name: "conflict", core: platform.ErrModuleDataConflict, want: contracts.ErrRecordConflict},
		{name: "invalid", core: platform.ErrModuleDataInvalid, want: contracts.ErrRecordInvalid},
		{name: "limit", core: platform.ErrModuleDataLimitExceeded, want: contracts.ErrRecordLimit},
		{name: "unavailable", core: platform.ErrModuleDataUnavailable, want: contracts.ErrRecordUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := mapCoreRecordError(test.core); !errors.Is(got, test.want) {
				t.Fatalf("mapCoreRecordError(%v) = %v, want %v", test.core, got, test.want)
			}
		})
	}
}
