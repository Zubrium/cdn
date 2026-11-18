package coordinator

import "errors"

var (
	ErrInvalid                 = errors.New("invalid Coordinator input or configuration")
	ErrDuplicate               = errors.New("Coordinator component is already registered")
	ErrComponentPanic          = errors.New("Coordinator component panicked")
	ErrTaskProviderUnavailable = errors.New("selected Coordinator worker has no task provider")
	ErrTaskHandlerConflict     = errors.New("multiple Coordinator workers register the same task type")
)
