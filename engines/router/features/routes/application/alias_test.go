package application

import (
	"errors"
	"fmt"
	"testing"

	"github.com/Zubrium/cdn/engines/router/features/routes/model"
	"github.com/Zubrium/cdn/pkg/contracts"
)

func TestRouterApplicationSentinelAliasesKeepErrorsIs(t *testing.T) {
	for name, scenario := range map[string]struct {
		application, neutral error
	}{
		"forbidden":   {ErrForbidden, contracts.ErrPublicForbidden},
		"invalid":     {ErrInvalid, contracts.ErrPublicInvalid},
		"not found":   {ErrNotFound, contracts.ErrPublicNotFound},
		"unavailable": {ErrUnavailable, contracts.ErrPublicUnavailable},
	} {
		t.Run(name, func(t *testing.T) {
			if scenario.application != scenario.neutral {
				t.Fatal("application sentinel is no longer an alias of the neutral error")
			}
			if !errors.Is(fmt.Errorf("wrapped: %w", scenario.application), scenario.neutral) {
				t.Fatal("errors.Is did not follow the neutral sentinel alias")
			}
		})
	}
	if !errors.Is(model.ErrInvalidRoute, contracts.ErrInvalidRoute) {
		t.Fatal("route model sentinel is no longer an alias of the neutral error")
	}
}
