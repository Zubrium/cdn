package coordinator

import (
	"errors"

	corecoord "github.com/Zubrium/cdn/core/domain/coordination"
)

var (
	ErrInvalid              = errors.New("invalid Coordinator input or configuration")
	ErrDuplicate            = errors.New("Coordinator component is already registered")
	ErrComponentPanic       = errors.New("Coordinator component panicked")
	ErrTaskExecutorNotReady = errors.New("task executor is not ready")
	ErrTaskClassUnavailable = errors.New("task class is not enabled")
	ErrLimit                = corecoord.ErrLimit
)
