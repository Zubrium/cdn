package composition

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Zubrium/cdn/main/internal/bus"
	"github.com/Zubrium/cdn/pkg/contracts"
)

func TestPublicationIntentOperationsRequirePublicationCaller(t *testing.T) {
	list := contracts.ListContentPublicationIntentsOperation()
	ack := contracts.AcknowledgeContentPublicationIntentOperation()
	dispatcher, err := bus.NewAllowlisted(bus.ConcurrencyLimits{MaxInFlight: 1}, list.Key, ack.Key)
	if err != nil {
		t.Fatal(err)
	}
	if err := registerPublicationIntentOperations(dispatcher, &contentEngineComponent{}); err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Seal(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	admin := bus.NewModuleDispatcher(dispatcher, "admin.api")
	if _, err := contracts.Call(ctx, admin, list, contracts.ContentPublicationIntentList{Limit: 1}); !errors.Is(err, bus.ErrOperationNotAllowedForModule) {
		t.Fatalf("admin list: %v", err)
	}
	if _, err := contracts.Call(ctx, dispatcher, ack, contracts.ContentPublicationIntentAck{}); !errors.Is(err, bus.ErrInvalidModuleIdentity) {
		t.Fatalf("unscoped acknowledgement: %v", err)
	}
	publisher := bus.NewModuleDispatcher(dispatcher, "main.publication")
	if _, err := contracts.Call(ctx, publisher, list, contracts.ContentPublicationIntentList{Limit: 1}); !errors.Is(err, contracts.ErrAdminContentUnavailable) {
		t.Fatalf("publisher should reach Content readiness check: %v", err)
	}
}

type deliveryDispatcher func(context.Context, contracts.Envelope) (contracts.Envelope, error)

func (f deliveryDispatcher) Dispatch(ctx context.Context, request contracts.Envelope) (contracts.Envelope, error) {
	return f(ctx, request)
}

func TestPublicationDeliveryReplaysAfterLostAcknowledgement(t *testing.T) {
	const runID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	intent := contracts.PublicationPublishRequest{Locale: "en-US", ContentPublicationRevision: 9, SystemModelRevision: 2, IdempotencyKey: "publication-test-key"}
	delivered := false
	starts, acknowledgements := 0, 0
	outage := errors.New("acknowledgement unavailable")
	dispatch := deliveryDispatcher(func(_ context.Context, req contracts.Envelope) (contracts.Envelope, error) {
		var result any
		switch req.Key() {
		case contracts.ListContentPublicationIntentsOperation().Key:
			page := contracts.ContentPublicationIntentPage{}
			if !delivered {
				page.Items = []contracts.ContentPublicationIntentItem{{Publication: intent, StoreRevision: 17}}
			}
			result = page
		case contracts.PublicationPublishOperation().Key:
			var trigger contracts.PublicationPublishRequest
			if err := json.Unmarshal(req.Payload, &trigger); err != nil || trigger != intent {
				t.Fatalf("trigger=%+v, %v", trigger, err)
			}
			starts++
			result = contracts.PublicationPublishResponse{RunID: runID, WorkflowStarted: true, State: "running"}
		case contracts.AcknowledgeContentPublicationIntentOperation().Key:
			var ack contracts.ContentPublicationIntentAck
			if err := json.Unmarshal(req.Payload, &ack); err != nil || ack.RunID != runID || ack.StoreRevision != 17 || ack.ContentRevision != 9 {
				t.Fatalf("ack=%+v, %v", ack, err)
			}
			acknowledgements++
			if acknowledgements == 1 {
				return contracts.Envelope{}, outage
			}
			delivered = true
			result = struct{}{}
		default:
			t.Fatalf("unexpected operation %v", req.Key())
		}
		raw, err := json.Marshal(result)
		return contracts.Envelope{OperationID: req.OperationID, Version: req.Version, Payload: raw}, err
	})
	for pass := 0; pass < 3; pass++ {
		_, err := deliverPublicationPage(context.Background(), dispatch, "", 1)
		if (pass == 0 && !errors.Is(err, outage)) || (pass != 0 && err != nil) {
			t.Fatalf("pass %d: %v", pass, err)
		}
	}
	if !delivered || starts != 2 || acknowledgements != 2 {
		t.Fatalf("delivered=%v starts=%d acknowledgements=%d", delivered, starts, acknowledgements)
	}
}

func TestPublicationDeliveryContinuesPastUnconfirmedStart(t *testing.T) {
	const runID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	acknowledged := int64(0)
	dispatch := deliveryDispatcher(func(_ context.Context, req contracts.Envelope) (contracts.Envelope, error) {
		var result any
		switch req.Key() {
		case contracts.ListContentPublicationIntentsOperation().Key:
			result = contracts.ContentPublicationIntentPage{
				Items: []contracts.ContentPublicationIntentItem{
					{Publication: contracts.PublicationPublishRequest{Locale: "en-US", ContentPublicationRevision: 1, IdempotencyKey: "start-failed-or-pending"}},
					{Publication: contracts.PublicationPublishRequest{Locale: "en-US", ContentPublicationRevision: 2, IdempotencyKey: "started-with-failed-target"}},
				},
				NextCursor: "next",
			}
		case contracts.PublicationPublishOperation().Key:
			var trigger contracts.PublicationPublishRequest
			if err := json.Unmarshal(req.Payload, &trigger); err != nil {
				return contracts.Envelope{}, err
			}
			result = contracts.PublicationPublishResponse{RunID: runID, WorkflowStarted: trigger.ContentPublicationRevision == 2, State: "failed"}
		case contracts.AcknowledgeContentPublicationIntentOperation().Key:
			var ack contracts.ContentPublicationIntentAck
			if err := json.Unmarshal(req.Payload, &ack); err != nil {
				return contracts.Envelope{}, err
			}
			if acknowledged != 0 {
				t.Fatal("more than one intent acknowledged")
			}
			acknowledged = ack.ContentRevision
			result = struct{}{}
		default:
			t.Fatalf("unexpected operation %v", req.Key())
		}
		raw, err := json.Marshal(result)
		return contracts.Envelope{OperationID: req.OperationID, Version: req.Version, Payload: raw}, err
	})
	cursor, err := deliverPublicationPage(context.Background(), dispatch, "", 2)
	if err == nil || cursor != "next" || acknowledged != 2 {
		t.Fatalf("cursor=%q, acknowledgement=%d, error=%v", cursor, acknowledged, err)
	}
}
