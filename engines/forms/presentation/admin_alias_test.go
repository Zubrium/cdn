package presentation

import (
	"errors"
	"testing"

	"github.com/Zubrium/cdn/pkg/contracts"
)

func TestAdminFormsAliasesPreserveErrorsIs(t *testing.T) {
	pairs := []struct {
		engine  error
		neutral error
	}{
		{ErrUnauthorized, contracts.ErrAdminFormsUnauthorized},
		{ErrForbidden, contracts.ErrAdminFormsForbidden},
		{ErrNotFound, contracts.ErrAdminFormsNotFound},
		{ErrConflict, contracts.ErrAdminFormsConflict},
		{ErrInvalid, contracts.ErrAdminFormsInvalid},
		{ErrTooLarge, contracts.ErrAdminFormsTooLarge},
		{ErrUnavailable, contracts.ErrAdminFormsUnavailable},
		{ErrExpired, contracts.ErrAdminFormsExpired},
	}
	for _, pair := range pairs {
		if !errors.Is(pair.engine, pair.neutral) {
			t.Fatalf("engine error %v is not neutral error %v", pair.engine, pair.neutral)
		}
	}
}
