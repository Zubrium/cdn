package contracts

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

type testDispatcher struct {
	response Envelope
	err      error
}

func (dispatcher testDispatcher) Dispatch(context.Context, Envelope) (Envelope, error) {
	return dispatcher.response, dispatcher.err
}

func TestCallValidatesAndPreservesTypedResponse(t *testing.T) {
	operation := Operation[int, string]{Key: Key{ID: "core.ping", Version: 1}, Validate: func(request int) error {
		if request < 0 {
			return errors.New("negative request")
		}
		return nil
	}}
	response, err := Call(context.Background(), testDispatcher{response: Envelope{OperationID: operation.Key.ID, Version: operation.Key.Version, Payload: []byte(`"pong"`)}}, operation, 1)
	if err != nil || response != "pong" {
		t.Fatalf("Call() = %q, %v; want pong", response, err)
	}
	if _, err := Call(context.Background(), testDispatcher{response: Envelope{OperationID: operation.Key.ID, Version: operation.Key.Version, Payload: []byte(`"pong"`)}}, operation, -1); err == nil {
		t.Fatal("invalid request was dispatched")
	}
}

func TestCallRejectsMissingDispatcherAndWrongResponseType(t *testing.T) {
	operation := Operation[int, string]{Key: Key{ID: "core.ping", Version: 1}}
	if _, err := Call(context.Background(), nil, operation, 1); !errors.Is(err, ErrDispatcherUnavailable) {
		t.Fatalf("Call() without dispatcher = %v", err)
	}
	if _, err := Call(context.Background(), testDispatcher{response: Envelope{OperationID: operation.Key.ID, Version: operation.Key.Version, Payload: []byte(`1`)}}, operation, 1); !errors.Is(err, ErrResponseTypeMismatch) {
		t.Fatalf("Call() with wrong response type = %v", err)
	}
	if _, err := Call(context.Background(), testDispatcher{response: Envelope{OperationID: "core.other", Version: 1, Payload: []byte(`"pong"`)}}, operation, 1); !errors.Is(err, ErrResponseTypeMismatch) {
		t.Fatalf("Call() with different operation response = %v", err)
	}
}

func TestBindRejectsInvalidContractAndInputType(t *testing.T) {
	if _, err := Bind(Operation[int, string]{}, func(context.Context, int) (string, error) { return "", nil }); !errors.Is(err, ErrInvalidOperation) {
		t.Fatalf("Bind() with invalid contract = %v", err)
	}
	operation := Operation[int, string]{Key: Key{ID: "core.ping", Version: 1}}
	bound, err := Bind(operation, func(context.Context, int) (string, error) { return "pong", nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bound(context.Background(), "wrong"); !errors.Is(err, ErrRequestTypeMismatch) {
		t.Fatalf("bound handler accepted wrong request type: %v", err)
	}
}

func TestDecodePayloadRejectsDuplicateObjectKeys(t *testing.T) {
	if _, err := DecodePayload[struct{ Value string }](json.RawMessage(`{"Value":"first","Value":"second"}`)); !errors.Is(err, ErrMalformedEnvelope) {
		t.Fatalf("duplicate payload key = %v", err)
	}
}
