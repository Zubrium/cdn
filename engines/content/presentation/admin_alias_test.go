package presentation

import (
	"errors"
	"testing"

	"github.com/Zubrium/cdn/pkg/contracts"
)

func TestAdminContentAliasesPreserveErrorsIs(t *testing.T) {
	pairs := []struct {
		engine  error
		neutral error
	}{
		{ErrUnauthorized, contracts.ErrAdminContentUnauthorized},
		{ErrForbidden, contracts.ErrAdminContentForbidden},
		{ErrNotFound, contracts.ErrAdminContentNotFound},
		{ErrConflict, contracts.ErrAdminContentConflict},
		{ErrInvalid, contracts.ErrAdminContentInvalid},
		{ErrTooLarge, contracts.ErrAdminContentTooLarge},
		{ErrUnavailable, contracts.ErrAdminContentUnavailable},
	}
	for _, pair := range pairs {
		if !errors.Is(pair.engine, pair.neutral) {
			t.Fatalf("engine error %v is not neutral error %v", pair.engine, pair.neutral)
		}
	}
}
