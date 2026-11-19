package contracts

import (
	"context"
	"errors"
	"fmt"
)

var (
	ErrInvalidOperation      = errors.New("invalid operation contract")
	ErrRequestTypeMismatch   = errors.New("operation request type mismatch")
	ErrResponseTypeMismatch  = errors.New("operation response type mismatch")
	ErrDispatcherUnavailable = errors.New("operation dispatcher unavailable")
)

type Key struct {
	ID      string
	Version uint32
}

func (key Key) Validate() error {
	if key.ID == "" || key.Version == 0 {
		return ErrInvalidOperation
	}
	return nil
}

type Operation[Request, Response any] struct {
	Key      Key
	Validate func(Request) error
}

type CoreReadinessRequest struct{}

type CoreReadinessResponse struct {
	Ready bool
}

func CoreReadinessOperation() Operation[CoreReadinessRequest, CoreReadinessResponse] {
	return Operation[CoreReadinessRequest, CoreReadinessResponse]{Key: Key{ID: "core.readiness", Version: 1}}
}

type Handler[Request, Response any] func(context.Context, Request) (Response, error)

type Dispatcher interface {
	Dispatch(context.Context, Key, any) (any, error)
}

func Bind[Request, Response any](operation Operation[Request, Response], handler Handler[Request, Response]) (func(context.Context, any) (any, error), error) {
	if err := operation.Key.Validate(); err != nil || handler == nil {
		return nil, ErrInvalidOperation
	}
	return func(ctx context.Context, input any) (any, error) {
		request, ok := input.(Request)
		if !ok {
			return nil, fmt.Errorf("%w: %T", ErrRequestTypeMismatch, input)
		}
		if operation.Validate != nil {
			if err := operation.Validate(request); err != nil {
				return nil, err
			}
		}
		return handler(ctx, request)
	}, nil
}

func Call[Request, Response any](ctx context.Context, dispatcher Dispatcher, operation Operation[Request, Response], request Request) (Response, error) {
	var zero Response
	if dispatcher == nil {
		return zero, ErrDispatcherUnavailable
	}
	if err := operation.Key.Validate(); err != nil {
		return zero, err
	}
	if operation.Validate != nil {
		if err := operation.Validate(request); err != nil {
			return zero, err
		}
	}
	response, err := dispatcher.Dispatch(ctx, operation.Key, request)
	if err != nil {
		return zero, err
	}
	result, ok := response.(Response)
	if !ok {
		return zero, fmt.Errorf("%w: %T", ErrResponseTypeMismatch, response)
	}
	return result, nil
}
