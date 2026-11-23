package composition

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	contentapi "github.com/Zubrium/cdn/engines/content/presentation"
	"github.com/Zubrium/cdn/pkg/contracts"
)

type publicationTriggerContent struct {
	contentapi.API
	workspace contentapi.Workspace
	err       error
	calls     int
}

func (fake *publicationTriggerContent) PublishWorkspace(context.Context, contentapi.Projections,
	contentapi.PublishWorkspaceRequest) (contentapi.Workspace, error) {
	fake.calls++
	return fake.workspace, fake.err
}

type publicationTriggerBus struct {
	request contracts.PublicationPublishRequest
	calls   int
	err     error
}

func (bus *publicationTriggerBus) Dispatch(_ context.Context, envelope contracts.Envelope) (contracts.Envelope, error) {
	bus.calls++
	if envelope.Key() != contracts.PublicationPublishOperation().Key {
		return contracts.Envelope{}, contracts.ErrInvalidOperation
	}
	if err := json.Unmarshal(envelope.Payload, &bus.request); err != nil {
		return contracts.Envelope{}, err
	}
	if bus.err != nil {
		return contracts.Envelope{}, bus.err
	}
	payload, err := json.Marshal(contracts.PublicationPublishResponse{State: "running"})
	if err != nil {
		return contracts.Envelope{}, err
	}
	return contracts.Envelope{OperationID: envelope.OperationID, Version: envelope.Version, Payload: payload}, nil
}

func TestContentPublicationStartsTrustedFanout(t *testing.T) {
	content := &publicationTriggerContent{workspace: contentapi.Workspace{
		Locale: "en-US", PublishedRevision: 7, ModelRevision: 4,
	}}
	bus := &publicationTriggerBus{}
	api := contentPublicationAPI{API: content, client: publicationClient{dispatcher: bus}}

	workspace, err := api.PublishWorkspace(context.Background(), contentapi.Projections{},
		contentapi.PublishWorkspaceRequest{IdempotencyKey: "e2e-publication-trigger-key"})
	if err != nil {
		t.Fatalf("publish workspace: %v", err)
	}
	if workspace.PublishedRevision != content.workspace.PublishedRevision || content.calls != 1 || bus.calls != 1 {
		t.Fatalf("unexpected forwarding: workspace=%+v content calls=%d bus calls=%d", workspace, content.calls, bus.calls)
	}
	want := contracts.PublicationPublishRequest{
		Locale: "en-US", ContentPublicationRevision: 7, SystemModelRevision: 4,
		IdempotencyKey: "e2e-publication-trigger-key",
	}
	if bus.request != want {
		t.Fatalf("fan-out request = %+v, want %+v", bus.request, want)
	}
}

func TestContentPublicationDoesNotStartFanoutWithoutCommit(t *testing.T) {
	bus := &publicationTriggerBus{}
	api := contentPublicationAPI{API: &publicationTriggerContent{err: errors.New("publish rejected")}, client: publicationClient{dispatcher: bus}}
	if _, err := api.PublishWorkspace(context.Background(), contentapi.Projections{},
		contentapi.PublishWorkspaceRequest{IdempotencyKey: "e2e-publication-trigger-key"}); err == nil {
		t.Fatal("expected Content publication error")
	}
	if bus.calls != 0 {
		t.Fatalf("fan-out called %d times after a failed publication", bus.calls)
	}
}
