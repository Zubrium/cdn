package bus

import (
	"context"

	"github.com/Zubrium/cdn/pkg/contracts"
)

type TaskOperationHandler func(context.Context, contracts.OperationTask) (contracts.OperationOutcome, error)

// RegisterTaskOperations binds statically compiled operation handlers to the
// process-local DataBus. Core never receives handlers or a module catalog.
func RegisterTaskOperations(dispatcher *Dispatcher, handlers map[contracts.Key]TaskOperationHandler) error {
	if dispatcher == nil || len(handlers) == 0 {
		return contracts.ErrInvalidOperation
	}
	for key, handler := range handlers {
		if err := key.Validate(); err != nil || handler == nil {
			return contracts.ErrInvalidOperation
		}
		operation := contracts.Operation[contracts.OperationTask, contracts.OperationOutcome]{Key: key}
		if err := Register(dispatcher, operation, func(ctx context.Context, request contracts.OperationTask) (contracts.OperationOutcome, error) {
			return handler(ctx, request)
		}); err != nil {
			return err
		}
	}
	return nil
}
