package presentation

import (
	"errors"
	"testing"

	"github.com/Zubrium/cdn/pkg/contracts"
)

func TestAdminWebsiteAliasesPreserveErrorsIs(t *testing.T) {
	pairs := []struct {
		engine  error
		neutral error
	}{
		{ErrUnauthorized, contracts.ErrAdminWebsiteUnauthorized},
		{ErrForbidden, contracts.ErrAdminWebsiteForbidden},
		{ErrNotFound, contracts.ErrAdminWebsiteNotFound},
		{ErrConflict, contracts.ErrAdminWebsiteConflict},
		{ErrInvalid, contracts.ErrAdminWebsiteInvalid},
		{ErrTooLarge, contracts.ErrAdminWebsiteTooLarge},
		{ErrUnavailable, contracts.ErrAdminWebsiteUnavailable},
	}
	for _, pair := range pairs {
		if !errors.Is(pair.engine, pair.neutral) {
			t.Fatalf("engine error %v is not neutral error %v", pair.engine, pair.neutral)
		}
	}
}
