package bus

import (
	"context"
	"errors"

	"github.com/Zubrium/cdn/pkg/contracts"
)

func RegisterCoreReadiness(dispatcher *Dispatcher, check func(context.Context) error) error {
	if check == nil {
		return errors.New("Core readiness check is required")
	}
	return Register(dispatcher, contracts.CoreReadinessOperation(), func(ctx context.Context, _ contracts.CoreReadinessRequest) (contracts.CoreReadinessResponse, error) {
		if err := check(ctx); err != nil {
			return contracts.CoreReadinessResponse{}, err
		}
		return contracts.CoreReadinessResponse{Ready: true}, nil
	})
}
