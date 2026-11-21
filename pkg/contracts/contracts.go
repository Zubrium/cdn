package contracts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

var (
	ErrInvalidOperation      = errors.New("invalid operation contract")
	ErrRequestTypeMismatch   = errors.New("operation request type mismatch")
	ErrResponseTypeMismatch  = errors.New("operation response type mismatch")
	ErrDispatcherUnavailable = errors.New("operation dispatcher unavailable")
	ErrMalformedEnvelope     = errors.New("malformed operation envelope")
	ErrStreamUnavailable     = errors.New("stream capability unavailable")
)

type Envelope struct {
	OperationID string          `json:"operation_id"`
	Version     uint32          `json:"version"`
	Payload     json.RawMessage `json:"payload"`
}

func (envelope Envelope) Key() Key {
	return Key{ID: envelope.OperationID, Version: envelope.Version}
}

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
	Dispatch(context.Context, Envelope) (Envelope, error)
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

func DecodePayload[Value any](payload json.RawMessage) (Value, error) {
	var value Value
	if len(payload) == 0 || !json.Valid(payload) {
		return value, ErrMalformedEnvelope
	}
	if err := rejectDuplicateKeys(payload); err != nil {
		return value, err
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return value, fmt.Errorf("%w: %v", ErrRequestTypeMismatch, err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return value, ErrMalformedEnvelope
	}
	return value, nil
}

func rejectDuplicateKeys(payload []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	var visit func(json.Token) error
	visit = func(token json.Token) error {
		delimiter, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delimiter {
		case '{':
			seen := make(map[string]struct{})
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return ErrMalformedEnvelope
				}
				key, ok := keyToken.(string)
				if !ok {
					return ErrMalformedEnvelope
				}
				if _, exists := seen[key]; exists {
					return ErrMalformedEnvelope
				}
				seen[key] = struct{}{}
				value, err := decoder.Token()
				if err != nil {
					return ErrMalformedEnvelope
				}
				if err := visit(value); err != nil {
					return err
				}
			}
			if _, err := decoder.Token(); err != nil {
				return ErrMalformedEnvelope
			}
		case '[':
			for decoder.More() {
				value, err := decoder.Token()
				if err != nil {
					return ErrMalformedEnvelope
				}
				if err := visit(value); err != nil {
					return err
				}
			}
			if _, err := decoder.Token(); err != nil {
				return ErrMalformedEnvelope
			}
		default:
			return ErrMalformedEnvelope
		}
		return nil
	}
	root, err := decoder.Token()
	if err != nil {
		return ErrMalformedEnvelope
	}
	if err := visit(root); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return ErrMalformedEnvelope
	}
	return nil
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
	payload, err := json.Marshal(request)
	if err != nil {
		return zero, fmt.Errorf("%w: %v", ErrRequestTypeMismatch, err)
	}
	response, err := dispatcher.Dispatch(ctx, Envelope{OperationID: operation.Key.ID, Version: operation.Key.Version, Payload: payload})
	if err != nil {
		return zero, err
	}
	if response.Key() != operation.Key {
		return zero, ErrResponseTypeMismatch
	}
	result, err := DecodePayload[Response](response.Payload)
	if err != nil {
		return zero, fmt.Errorf("%w: %v", ErrResponseTypeMismatch, err)
	}
	return result, nil
}
