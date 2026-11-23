package coordinator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestWorkflowStartRejectsInvalidPayloadsBeforePersistence(t *testing.T) {
	store := &serviceStore{}
	config := serviceTestConfig(store)
	client, err := NewWorkflowClient(WorkflowClientConfig{Store: store, TaskClass: "engine.example", TaskClasses: config.TaskClasses,
		InstanceID: uuid.New(), ConfigEpoch: 1,
		MaxAttempts: 3, Retention: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	invalid := []struct {
		name    string
		payload json.RawMessage
	}{
		{name: "empty"},
		{name: "malformed", payload: json.RawMessage(`{"field":`)},
		{name: "array", payload: json.RawMessage(`[]`)},
		{name: "null", payload: json.RawMessage(`null`)},
		{name: "scalar", payload: json.RawMessage(`"value"`)},
		{name: "invalid UTF-8", payload: json.RawMessage([]byte{0xff})},
		{name: "oversized", payload: append(json.RawMessage(`{"data":"`), append(bytes.Repeat([]byte{'a'}, maxWorkflowPayloadBytes), []byte(`"}`)...)...)},
	}
	for _, input := range invalid {
		t.Run(input.name, func(t *testing.T) {
			_, err := client.Start(context.Background(), WorkflowStartRequest{OperationID: "process", ContractVersion: 1,
				IdempotencyKey: "request-1", Payload: input.payload})
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("invalid workflow payload was accepted: %v", err)
			}
		})
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.startCalls != 0 {
		t.Fatalf("invalid payloads reached persistence %d times", store.startCalls)
	}
}

func TestWorkflowStartRejectsInvalidIdempotencyKeys(t *testing.T) {
	for _, key := range []string{"", " leading", "trailing ", "line\nbreak", string([]byte{0xff}), string(bytes.Repeat([]byte{'x'}, 257))} {
		if validCoordinatorIdempotencyKey(key) {
			t.Errorf("invalid idempotency key accepted: %q", key)
		}
	}
}
