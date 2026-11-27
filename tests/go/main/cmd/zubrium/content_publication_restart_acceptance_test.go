//go:build publication_delivery_acceptance

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"testing"
	"time"

	core "github.com/Zubrium/cdn/core/presentation"
	content "github.com/Zubrium/cdn/engines/content"
	"github.com/Zubrium/cdn/engines/content/domain"
	"github.com/Zubrium/cdn/engines/content/repository"
	"github.com/Zubrium/cdn/main/internal/publication"
	"github.com/Zubrium/cdn/pkg/contracts"
	"github.com/google/uuid"
)

var errPublicationStartReplyLost = errors.New("injected loss of workflow start reply")

type restartWorkflowStarter struct {
	client    *core.WorkflowClient
	receipt   core.WorkflowReceipt
	loseReply bool
}

func (starter *restartWorkflowStarter) Start(ctx context.Context, request contracts.WorkflowStartInput) (contracts.WorkflowStartReceipt, error) {
	receipt, err := starter.client.Start(ctx, core.WorkflowStartRequest{
		OperationID: request.OperationID, ContractVersion: request.ContractVersion,
		IdempotencyKey: request.IdempotencyKey, Payload: request.Payload,
	})
	if err != nil {
		return contracts.WorkflowStartReceipt{}, err
	}
	starter.receipt = receipt
	if starter.loseReply {
		return contracts.WorkflowStartReceipt{}, errPublicationStartReplyLost
	}
	return contracts.WorkflowStartReceipt{WorkflowID: receipt.WorkflowID, RootTaskID: receipt.RootTaskID, State: receipt.State, Created: receipt.Created}, nil
}

// This scenario restarts the Core runtime and recreates the Content repository
// and publication service over the same disposable PostgreSQL database.
func TestContentPublicationDeliveryRestart(t *testing.T) {
	env := websitePersistenceEnvironment(t)
	db := assertDisposableDatabase(t, "WEBSITE_TEST_POSTGRES_DSN", env.runtimeDSN)
	if db.Hostname() != "127.0.0.1" || db.Port() == "" || db.Port() != os.Getenv("QA_PG_PORT") ||
		db.Path != "/zubrium_core_qa_platform" || env.s3Bucket != "zubrium-core-qa-"+env.runID ||
		os.Getenv("WEBSITE_TEST_S3_MOCK") == "1" {
		t.Fatal("requires the run-specific Docker QA stack on explicit loopback ports")
	}
	websiteProxyPrivateS3Checks(t, &env)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	config := websiteRuntimeConfig(env, websiteRunBootstrapSecret(env.runID))
	const locale = "en-US"
	fingerprint := sha256.Sum256([]byte("publication-delivery-" + env.runID))
	routeID, snapshotID := uuid.NewString(), uuid.NewString()

	start := func(loseReply bool) (*core.Runtime, *repository.Repository, *publication.Service, *restartWorkflowStarter) {
		t.Helper()
		runtime, _, err := core.Start(ctx, config)
		if err != nil {
			t.Fatalf("start disposable Core: %v", err)
		}
		t.Cleanup(func() {
			closeCtx, finish := context.WithTimeout(context.Background(), 15*time.Second)
			defer finish()
			if err := runtime.CloseContext(closeCtx); err != nil {
				t.Errorf("close disposable Core: %v", err)
			}
		})
		for _, name := range []string{"coordinator", content.RecordDescriptor().OwnerID, publication.ModuleDataDescriptor().OwnerID} {
			if err := runtime.Registry.Register(name); err != nil {
				t.Fatal(err)
			}
		}
		contentStore, err := websiteAcceptanceRecordStore(t, runtime, content.RecordDescriptor())
		if err != nil {
			t.Fatal(err)
		}
		queue, err := repository.New(contentStore)
		if err != nil {
			t.Fatal(err)
		}
		publicationStore, err := websiteAcceptanceRecordStore(t, runtime, publication.ModuleDataDescriptor())
		if err != nil {
			t.Fatal(err)
		}
		if err := runtime.StartCoordinator(ctx, core.CoordinatorRuntimeConfig{
			Fingerprint: hex.EncodeToString(fingerprint[:]), InstanceLease: 30 * time.Second, HeartbeatInterval: 5 * time.Second,
			RetryBase: time.Second, RetryMaximum: 2 * time.Second, MaxAttempts: 3, MaxWorkflowTasks: 32, MaxChildrenPerTask: 8,
			WorkflowRetention: time.Hour, ShutdownTimeout: 15 * time.Second, TaskClasses: []string{"engine.publication"},
		}); err != nil {
			t.Fatalf("start isolated Coordinator: %v", err)
		}
		client, err := runtime.NewWorkflowClient("engine.publication")
		if err != nil {
			t.Fatal(err)
		}
		starter := &restartWorkflowStarter{client: client, loseReply: loseReply}
		publisher, err := publication.NewService(publication.Config{
			BranchID: env.runID, Store: publicationStore, Starter: starter, MaxAttempts: 3,
			Targets: []publication.Target{{Locale: locale, RouteID: routeID, PageSnapshotID: snapshotID}},
		})
		if err != nil {
			t.Fatal(err)
		}
		return runtime, queue, publisher, starter
	}

	first, queue, publisher, lost := start(true)
	initial, err := queue.Create(ctx, domain.StoredWorkspace{Aggregate: domain.Aggregate{
		SnapshotID: domain.GlobalScopeID, Locale: locale, Records: []domain.Record{},
	}})
	if err != nil {
		t.Fatalf("create fixture Workspace on fresh database: %v", err)
	}
	history := domain.Publication{ID: uuid.NewString(), Locale: locale, ContentRevision: 1, CreatedAt: time.Now().UTC(),
		Records: []domain.Record{}, Selections: []domain.Selection{}}
	initial.Aggregate.Revision, initial.Aggregate.PublishedRevision = 1, 1
	intent := domain.PublicationIntent{Locale: locale, ContentRevision: 1, SystemModelRevision: 2,
		WorkflowKey: "publication-delivery-" + env.runID, State: "pending"}
	initial.PublicationIntent = &intent
	if _, err := queue.Commit(ctx, initial.StoreRevision, initial, &history); err != nil {
		t.Fatalf("commit Content and outbox: %v", err)
	}
	trigger := publication.Trigger{Locale: intent.Locale, ContentPublicationRevision: intent.ContentRevision,
		SystemModelRevision: intent.SystemModelRevision, IdempotencyKey: intent.WorkflowKey}
	if _, err := publisher.Publish(ctx, trigger); !errors.Is(err, errPublicationStartReplyLost) || !lost.receipt.Created {
		t.Fatalf("expected persisted workflow and lost reply, got %v (created=%v)", err, lost.receipt.Created)
	}
	if err := first.CloseContext(ctx); err != nil {
		t.Fatal(err)
	}

	_, queue, publisher, recovered := start(false)
	items, _, err := queue.ListPendingPublicationIntents(ctx, "", 8)
	if err != nil || len(items) != 1 || items[0].Intent != intent {
		t.Fatalf("pending after restart: items=%d err=%v", len(items), err)
	}
	item := items[0]
	result, err := publisher.Publish(ctx, publication.Trigger{Locale: item.Intent.Locale, ContentPublicationRevision: item.Intent.ContentRevision,
		SystemModelRevision: item.Intent.SystemModelRevision, IdempotencyKey: item.Intent.WorkflowKey})
	if err != nil || !result.WorkflowStarted || recovered.receipt.Created ||
		recovered.receipt.WorkflowID != lost.receipt.WorkflowID || recovered.receipt.RootTaskID != lost.receipt.RootTaskID {
		t.Fatalf("expected reuse of original durable workflow and root: started=%v created=%v err=%v", result.WorkflowStarted, recovered.receipt.Created, err)
	}
	for i := 0; i < 2; i++ {
		if err := queue.MarkPublicationIntentDelivered(ctx, locale, intent.ContentRevision, item.StoreRevision, result.RunID); err != nil {
			t.Fatalf("acknowledge attempt %d: %v", i, err)
		}
	}
	items, _, err = queue.ListPendingPublicationIntents(ctx, "", 8)
	if err != nil || len(items) != 0 {
		t.Fatalf("pending after acknowledgement: items=%d err=%v", len(items), err)
	}
	persisted, err := queue.GetHistoryByContentRevision(ctx, locale, intent.ContentRevision)
	if err != nil || persisted.ID != history.ID {
		t.Fatalf("publication history changed: %v", err)
	}
}
