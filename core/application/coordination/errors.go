package coordinator

import "errors"

var (
	ErrInvalid               = errors.New("invalid Coordinator input or configuration")
	ErrDuplicate             = errors.New("Coordinator component is already registered")
	ErrComponentPanic        = errors.New("Coordinator component panicked")
	ErrWorkerHandlerConflict = errors.New("multiple workers register the same operation type")
	ErrTaskExecutorNotReady  = errors.New("task executor is not ready")
)
