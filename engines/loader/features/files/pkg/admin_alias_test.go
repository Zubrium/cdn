package pkg

import (
	"errors"
	"testing"

	"github.com/Zubrium/cdn/pkg/contracts"
)

func TestAdminFilesAliasesPreserveErrorsIs(t *testing.T) {
	pairs := []struct {
		engine  error
		neutral error
	}{
		{ErrUnauthorized, contracts.ErrAdminFilesUnauthorized},
		{ErrForbidden, contracts.ErrAdminFilesForbidden},
		{ErrNotFound, contracts.ErrAdminFilesNotFound},
		{ErrConflict, contracts.ErrAdminFilesConflict},
		{ErrInvalid, contracts.ErrAdminFilesInvalid},
		{ErrTooLarge, contracts.ErrAdminFilesTooLarge},
		{ErrUnavailable, contracts.ErrAdminFilesUnavailable},
	}
	for _, pair := range pairs {
		if !errors.Is(pair.engine, pair.neutral) {
			t.Fatalf("engine error %v is not neutral error %v", pair.engine, pair.neutral)
		}
	}
}
