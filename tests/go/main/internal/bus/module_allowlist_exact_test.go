package bus_test

import (
	"context"
	"errors"
	"testing"

	websiteworkflow "github.com/Zubrium/cdn/engines/website/workflow"
	"github.com/Zubrium/cdn/main/internal/bus"
	"github.com/Zubrium/cdn/pkg/contracts"
	mediaprofile "github.com/Zubrium/cdn/pkg/contracts/media"
	"github.com/Zubrium/cdn/workers/archive"
	"github.com/Zubrium/cdn/workers/media"
	"github.com/Zubrium/cdn/workers/prerender"
)

type recordingDispatcher struct {
	keys    []contracts.Key
	modules []string
}

func (recorder *recordingDispatcher) Dispatch(ctx context.Context, envelope contracts.Envelope) (contracts.Envelope, error) {
	recorder.keys = append(recorder.keys, envelope.Key())
	recorder.modules = append(recorder.modules, bus.ModuleIDFromContext(ctx))
	return envelope, nil
}

func allBusOperationKeys() []contracts.Key {
	keys := []contracts.Key{
		contracts.CoreReadinessOperation().Key,
		contracts.AccessAuthenticateOperation().Key,
		contracts.AccessAuthenticateSessionOperation().Key,
		contracts.AccessRequireGroupOperation().Key,
		contracts.FileCreateVerifiedOperation().Key,
		contracts.FileReadOperation().Key,
		contracts.FileStatOperation().Key,
		contracts.FileDeleteOperation().Key,
		contracts.FileSetGetOperation().Key,
		contracts.FileSetListOperation().Key,
		contracts.FileSetMembersOperation().Key,
		contracts.FileSetAddOperation().Key,
		contracts.FileSetRemoveOperation().Key,
		contracts.RecordGetOperation().Key,
		contracts.RecordGetExpiredOperation().Key,
		contracts.RecordPutOperation().Key,
		contracts.RecordDeleteOperation().Key,
		contracts.RecordBatchOperation().Key,
		contracts.RecordListOperation().Key,
		contracts.RecordListExpiredOperation().Key,
		contracts.WorkflowStartOperation().Key,
		contracts.WorkflowStateOperation().Key,
		contracts.WorkflowTaskResultOperation().Key,
		contracts.WorkflowTaskIDResolveOperation().Key,
		contracts.WorkflowListOperation().Key,
		contracts.WorkflowGetOperation().Key,
		contracts.WorkflowRetryOperation().Key,
		contracts.PublicationPublishOperation().Key,
		contracts.RetirePageOperation().Key,
		contracts.GetPageRetirementOperation().Key,
		contracts.PublishedContentReadOperation().Key,
	}
	keys = append(keys, bus.AdminIdentityOperationKeys()...)
	return append(keys, []contracts.Key{
		{ID: archive.OperationID, Version: 1},
		{ID: media.OperationID, Version: 1},
		{ID: media.OperationID, Version: mediaprofile.WorkContractVersion},
		{ID: prerender.OperationID, Version: 1},
		{ID: websiteworkflow.OperationID, Version: 1},
		{ID: websiteworkflow.MediaOperationID, Version: 1},
		{ID: websiteworkflow.ForkOperationID, Version: 1},
		{ID: websiteworkflow.ForkCopyOperationID, Version: websiteworkflow.ForkChildContractVersion},
		{ID: websiteworkflow.ForkFinalizeOperationID, Version: websiteworkflow.ForkChildContractVersion},
	}...)
}

func keySet(keys ...contracts.Key) map[contracts.Key]struct{} {
	result := make(map[contracts.Key]struct{}, len(keys))
	for _, key := range keys {
		result[key] = struct{}{}
	}
	return result
}

func unionKeySets(sets ...map[contracts.Key]struct{}) map[contracts.Key]struct{} {
	result := make(map[contracts.Key]struct{})
	for _, set := range sets {
		for key := range set {
			result[key] = struct{}{}
		}
	}
	return result
}

func expectedKeysForModule(t *testing.T, moduleID string) map[contracts.Key]struct{} {
	t.Helper()
	core := keySet(contracts.CoreReadinessOperation().Key)
	access := keySet(
		contracts.AccessAuthenticateOperation().Key,
		contracts.AccessAuthenticateSessionOperation().Key,
		contracts.AccessRequireGroupOperation().Key,
	)
	files := keySet(
		contracts.FileCreateVerifiedOperation().Key,
		contracts.FileReadOperation().Key,
		contracts.FileStatOperation().Key,
		contracts.FileDeleteOperation().Key,
		contracts.FileSetGetOperation().Key,
		contracts.FileSetListOperation().Key,
		contracts.FileSetMembersOperation().Key,
		contracts.FileSetAddOperation().Key,
		contracts.FileSetRemoveOperation().Key,
	)
	records := keySet(
		contracts.RecordGetOperation().Key,
		contracts.RecordGetExpiredOperation().Key,
		contracts.RecordPutOperation().Key,
		contracts.RecordDeleteOperation().Key,
		contracts.RecordBatchOperation().Key,
		contracts.RecordListOperation().Key,
		contracts.RecordListExpiredOperation().Key,
	)
	workflow := keySet(
		contracts.WorkflowStartOperation().Key,
		contracts.WorkflowStateOperation().Key,
		contracts.WorkflowTaskResultOperation().Key,
		contracts.WorkflowTaskIDResolveOperation().Key,
	)
	adminWorkflow := keySet(
		contracts.WorkflowListOperation().Key,
		contracts.WorkflowGetOperation().Key,
		contracts.WorkflowRetryOperation().Key,
	)
	adminIdentity := keySet(bus.AdminIdentityOperationKeys()...)
	publication := keySet(contracts.PublicationPublishOperation().Key)
	retirement := keySet(contracts.RetirePageOperation().Key, contracts.GetPageRetirementOperation().Key)
	publishedContent := keySet(contracts.PublishedContentReadOperation().Key)

	switch moduleID {
	case "engine.website", "engine.website.import":
		return unionKeySets(core, access, files, records, workflow)
	case "engine.content", "engine.forms", "engine.loader":
		return unionKeySets(core, access, files, records)
	case "engine.router":
		return unionKeySets(core, access, files, publishedContent)
	case "worker.archive", "worker.media", "worker.prerender":
		return unionKeySets(core, access, files, records, workflow)
	case "main.publication":
		return unionKeySets(core, access, files, records, workflow, publication)
	case "composition.retirement":
		return unionKeySets(core, access, files, records, workflow, retirement)
	case "admin.identity":
		return unionKeySets(core, access, adminIdentity)
	case "admin.coordinator":
		return unionKeySets(core, access, workflow, adminWorkflow)
	case "admin.api":
		return unionKeySets(core, access, files, records, workflow, adminWorkflow, adminIdentity, publication, retirement, publishedContent)
	default:
		t.Fatalf("test does not declare expected keys for module %q", moduleID)
		return nil
	}
}

func TestModuleAllowlistsAreExactForEveryCompiledIdentity(t *testing.T) {
	modules := []string{
		"engine.website", "engine.website.import", "engine.content", "engine.forms",
		"engine.loader", "engine.router", "worker.archive", "worker.media", "worker.prerender",
		"main.publication", "composition.retirement", "admin.identity", "admin.coordinator", "admin.api",
	}
	for _, moduleID := range modules {
		t.Run(moduleID, func(t *testing.T) {
			expected := expectedKeysForModule(t, moduleID)
			recorder := &recordingDispatcher{}
			client := bus.NewModuleDispatcher(recorder, moduleID)
			if client == nil {
				t.Fatal("module dispatcher was not constructed")
			}
			for _, key := range allBusOperationKeys() {
				_, err := client.Dispatch(context.Background(), contracts.Envelope{
					OperationID: key.ID, Version: key.Version, Payload: []byte(`{}`),
				})
				_, allowed := expected[key]
				if allowed {
					if err != nil {
						t.Errorf("operation %s/v%d: unexpected rejection: %v", key.ID, key.Version, err)
					}
					continue
				}
				if !errors.Is(err, bus.ErrOperationNotAllowedForModule) {
					t.Errorf("operation %s/v%d: error = %v, want ErrOperationNotAllowedForModule", key.ID, key.Version, err)
				}
			}
			if len(recorder.keys) != len(expected) {
				t.Errorf("underlying dispatcher received %d operations, want exactly %d", len(recorder.keys), len(expected))
			}
			for index, key := range recorder.keys {
				if _, allowed := expected[key]; !allowed {
					t.Errorf("underlying dispatcher received disallowed operation %s/v%d", key.ID, key.Version)
				}
				if recorder.modules[index] != moduleID {
					t.Errorf("dispatch source = %q, want %q", recorder.modules[index], moduleID)
				}
			}
		})
	}
}

func TestCoordinatorBypassesModuleNarrowingButKeepsBoundIdentity(t *testing.T) {
	recorder := &recordingDispatcher{}
	client := bus.NewModuleDispatcher(recorder, "core.coordinator")
	if client == nil {
		t.Fatal("Coordinator module dispatcher was not constructed")
	}
	key := contracts.Key{ID: "core.unlisted-but-dispatched", Version: 1}
	if _, err := client.Dispatch(context.Background(), contracts.Envelope{OperationID: key.ID, Version: key.Version, Payload: []byte(`{}`)}); err != nil {
		t.Fatalf("Coordinator dispatch was narrowed: %v", err)
	}
	if len(recorder.keys) != 1 || recorder.keys[0] != key {
		t.Fatalf("dispatch record = %#v, want %v", recorder.keys, key)
	}
	if recorder.modules[0] != "core.coordinator" {
		t.Fatalf("dispatch identity = %q, want core.coordinator", recorder.modules[0])
	}
}

func TestUnknownModuleCannotDispatchAnyOperation(t *testing.T) {
	recorder := &recordingDispatcher{}
	client := bus.NewModuleDispatcher(recorder, "engine.unknown")
	if client == nil {
		t.Fatal("unknown module dispatcher was constructed")
	}
	for _, key := range allBusOperationKeys() {
		_, err := client.Dispatch(context.Background(), contracts.Envelope{OperationID: key.ID, Version: key.Version, Payload: []byte(`{}`)})
		if !errors.Is(err, bus.ErrOperationNotAllowedForModule) {
			t.Errorf("operation %s/v%d: error = %v, want ErrOperationNotAllowedForModule", key.ID, key.Version, err)
		}
	}
	if len(recorder.keys) != 0 {
		t.Fatalf("unknown module reached underlying dispatcher %d times", len(recorder.keys))
	}
}
