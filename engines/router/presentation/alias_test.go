package presentation

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/Zubrium/cdn/pkg/contracts"
)

func TestRouterFacadeAliasesShareNeutralContracts(t *testing.T) {
	for name, scenario := range map[string]struct {
		facade, neutral error
	}{
		"invalid target": {ErrInvalidTarget, contracts.ErrInvalidTarget},
		"invalid":        {ErrInvalid, contracts.ErrPublicInvalid},
		"not found":      {ErrNotFound, contracts.ErrPublicNotFound},
		"unavailable":    {ErrUnavailable, contracts.ErrPublicUnavailable},
		"too large":      {ErrTooLarge, contracts.ErrPublicTooLarge},
	} {
		t.Run(name, func(t *testing.T) {
			if scenario.facade != scenario.neutral {
				t.Fatal("facade sentinel is no longer an alias of the neutral error")
			}
			if !errors.Is(fmt.Errorf("wrapped: %w", scenario.facade), scenario.neutral) {
				t.Fatal("errors.Is did not follow the facade sentinel alias")
			}
		})
	}
	if reflect.TypeOf(CreateRouteRequest{}) != reflect.TypeOf(contracts.CreateRouteRequest{}) ||
		reflect.TypeOf(DeliveryRequest{}) != reflect.TypeOf(contracts.DeliveryRequest{}) ||
		reflect.TypeOf(DeliveryResponse{}) != reflect.TypeOf(contracts.DeliveryResponse{}) {
		t.Fatal("facade DTOs are no longer aliases of the neutral contracts")
	}
	if got := (Descriptor{}).ComponentDescriptor(); got.ID != "engine.router" || got.Kind != "engine" || got.Version != 1 {
		t.Fatalf("facade descriptor diverged from neutral helper: %#v", got)
	}
}
