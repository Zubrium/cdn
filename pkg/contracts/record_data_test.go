package contracts

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

type recordDispatcher struct {
	request  Envelope
	response any
}

func (dispatcher *recordDispatcher) Dispatch(_ context.Context, request Envelope) (Envelope, error) {
	dispatcher.request = request
	payload, err := json.Marshal(dispatcher.response)
	if err != nil {
		return Envelope{}, err
	}
	return Envelope{OperationID: request.OperationID, Version: request.Version, Payload: payload}, nil
}

func TestRecordDataStoreGetUsesScopedDataBusOperation(t *testing.T) {
	want := Record{Payload: json.RawMessage(`{"state":"ready"}`), PayloadVersion: 1, Revision: 3,
		CreatedAt: time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)}
	dispatcher := &recordDispatcher{response: RecordGetResponse{Record: want}}
	store, err := NewRecordDataStore(dispatcher, RecordLimits{MaxPageSize: 25, MaxBatchOperations: 10,
		MaxPayloadBytes: 1024, MaxIndexBytes: 512, MaxBatchBytes: 4096, MaxPageBytes: 4096, MaxSelectors: 8, MaxSelectorValues: 10})
	if err != nil {
		t.Fatal(err)
	}
	key := RecordKey{Namespace: "website/pages", ScopeType: "global", ScopeID: "global", RecordKey: "page/1"}
	got, err := store.Get(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	if got.Revision != want.Revision || string(got.Payload) != string(want.Payload) {
		t.Fatalf("record = %#v, want %#v", got, want)
	}
	if dispatcher.request.OperationID != "core.records.get" || dispatcher.request.Version != 1 {
		t.Fatalf("operation = %s/v%d, want core.records.get/v1", dispatcher.request.OperationID, dispatcher.request.Version)
	}
	var request map[string]any
	if err := json.Unmarshal(dispatcher.request.Payload, &request); err != nil {
		t.Fatal(err)
	}
	if _, ok := request["owner_id"]; ok {
		t.Fatal("record owner identity must be bound by the DataBus scope, not sent in the request")
	}
}

func TestNewRecordDataStoreRejectsIncompleteLimits(t *testing.T) {
	if _, err := NewRecordDataStore(&recordDispatcher{}, RecordLimits{MaxPayloadBytes: 1024, MaxIndexBytes: 512,
		MaxBatchBytes: 4096, MaxPageBytes: 4096, MaxSelectorValues: 10,
		MaxPageSize: 25, MaxBatchOperations: 10}); err == nil {
		t.Fatal("constructor accepted limits without selector bounds")
	}
}
