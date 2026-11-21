package bus

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Zubrium/cdn/pkg/contracts"
)

type publishedContentReaderStub struct {
	pageSnapshotID string
	locale         string
	payload        json.RawMessage
	err            error
}

func (stub *publishedContentReaderStub) Read(_ context.Context, pageSnapshotID, locale string) (json.RawMessage, error) {
	stub.pageSnapshotID, stub.locale = pageSnapshotID, locale
	return stub.payload, stub.err
}

func TestPublishedContentOperationIsStaticAndOwned(t *testing.T) {
	dispatcher, err := NewAllowlisted(contracts.PublishedContentReadOperation().Key)
	if err != nil {
		t.Fatal(err)
	}
	stub := &publishedContentReaderStub{payload: json.RawMessage(`{"snapshot_id":"11111111-1111-4111-8111-111111111111","locale":"en-US","content_revision":7,"records":[],"selections":[]}`)}
	if err := RegisterPublishedContent(dispatcher, stub.Read); err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Seal(); err != nil {
		t.Fatal(err)
	}
	response, err := contracts.Call(context.Background(), dispatcher, contracts.PublishedContentReadOperation(), contracts.PublishedContentReadRequest{
		PageSnapshotID: "11111111-1111-4111-8111-111111111111", Locale: "en-US",
	})
	if err != nil {
		t.Fatal(err)
	}
	if stub.pageSnapshotID != "11111111-1111-4111-8111-111111111111" || stub.locale != "en-US" {
		t.Fatalf("reader request = %q, %q", stub.pageSnapshotID, stub.locale)
	}
	if string(response.Payload) != string(stub.payload) {
		t.Fatalf("published content payload = %s", response.Payload)
	}
}

func TestPublishedContentOperationFailsClosed(t *testing.T) {
	dispatcher, err := NewAllowlisted(contracts.PublishedContentReadOperation().Key)
	if err != nil {
		t.Fatal(err)
	}
	if err := RegisterPublishedContent(dispatcher, nil); !errors.Is(err, contracts.ErrInvalidOperation) {
		t.Fatalf("nil reader = %v", err)
	}
	stub := &publishedContentReaderStub{payload: json.RawMessage(`not-json`)}
	if err := RegisterPublishedContent(dispatcher, stub.Read); err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Seal(); err != nil {
		t.Fatal(err)
	}
	_, err = contracts.Call(context.Background(), dispatcher, contracts.PublishedContentReadOperation(), contracts.PublishedContentReadRequest{
		PageSnapshotID: "snapshot", Locale: "en US",
	})
	if !errors.Is(err, contracts.ErrInvalidPublishedContentRequest) {
		t.Fatalf("invalid request = %v", err)
	}
	_, err = contracts.Call(context.Background(), dispatcher, contracts.PublishedContentReadOperation(), contracts.PublishedContentReadRequest{
		PageSnapshotID: "snapshot", Locale: "en-US",
	})
	if !errors.Is(err, ErrInvalidPublishedContentPayload) {
		t.Fatalf("invalid payload = %v", err)
	}
}
