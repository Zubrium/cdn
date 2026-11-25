package domain

import (
	"errors"
	"testing"

	"github.com/Zubrium/cdn/pkg/contracts"
)

func TestAdminContentDomainAliasesPreserveErrorsIs(t *testing.T) {
	pairs := []struct {
		engine  error
		neutral error
	}{
		{ErrInvalid, contracts.ErrAdminContentInvalid},
		{ErrNotFound, contracts.ErrAdminContentNotFound},
		{ErrConflict, contracts.ErrAdminContentConflict},
		{ErrUnavailable, contracts.ErrAdminContentUnavailable},
		{ErrTooLarge, contracts.ErrAdminContentTooLarge},
	}
	for _, pair := range pairs {
		if !errors.Is(pair.engine, pair.neutral) {
			t.Fatalf("domain error %v is not neutral error %v", pair.engine, pair.neutral)
		}
	}
}
