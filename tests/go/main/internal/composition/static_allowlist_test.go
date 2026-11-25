package composition

import (
	"sort"
	"testing"

	platform "github.com/Zubrium/cdn/core/presentation"
	"github.com/Zubrium/cdn/main/internal/bus"
	appconfig "github.com/Zubrium/cdn/main/internal/config"
	"github.com/Zubrium/cdn/pkg/contracts"
)

func baselineStaticOperationKeys() []contracts.Key {
	keys := []contracts.Key{
		contracts.CoreReadinessOperation().Key,
		contracts.WorkflowListOperation().Key,
		contracts.WorkflowGetOperation().Key,
		contracts.WorkflowRetryOperation().Key,
		contracts.WorkflowStartOperation().Key,
		contracts.WorkflowStateOperation().Key,
		contracts.WorkflowTaskResultOperation().Key,
		contracts.WorkflowTaskIDResolveOperation().Key,
		contracts.RecordGetOperation().Key,
		contracts.RecordGetExpiredOperation().Key,
		contracts.RecordPutOperation().Key,
		contracts.RecordDeleteOperation().Key,
		contracts.RecordBatchOperation().Key,
		contracts.RecordListOperation().Key,
		contracts.RecordListExpiredOperation().Key,
		contracts.FileCreateVerifiedOperation().Key,
		contracts.FileReadOperation().Key,
		contracts.FileStatOperation().Key,
		contracts.FileDeleteOperation().Key,
		contracts.FileSetGetOperation().Key,
		contracts.FileSetListOperation().Key,
		contracts.FileSetMembersOperation().Key,
		contracts.FileSetAddOperation().Key,
		contracts.FileSetRemoveOperation().Key,
		contracts.AccessAuthenticateOperation().Key,
		contracts.AccessAuthenticateSessionOperation().Key,
		contracts.AccessRequireGroupOperation().Key,
	}
	return append(keys, bus.AdminIdentityOperationKeys()...)
}

func assertCompiledDispatcherAllowlist(t *testing.T, plan platform.BuildPlan, extra ...contracts.Key) {
	t.Helper()
	resolved := appconfig.Resolved{}
	resolved.Scales.Bus.MaxInFlight = 8
	resolved.Coordinator.MaxAttempts = 3
	registry, core, _, _, err := registerComponents(resolved, plan, nil)
	if err != nil {
		t.Fatalf("register compiled profile: %v", err)
	}
	if registry == nil {
		t.Fatal("profile registry is nil")
	}
	if err != nil {
		t.Fatalf("re-register compiled profile: %v", err)
	}
	actual := core.dispatcher.AllowedKeys()
	want := append(baselineStaticOperationKeys(), extra...)
	sort.Slice(actual, func(i, j int) bool {
		if actual[i].ID == actual[j].ID {
			return actual[i].Version < actual[j].Version
		}
		return actual[i].ID < actual[j].ID
	})
	sort.Slice(want, func(i, j int) bool {
		if want[i].ID == want[j].ID {
			return want[i].Version < want[j].Version
		}
		return want[i].ID < want[j].ID
	})
	if len(actual) != len(want) {
		t.Fatalf("static allowlist has %d operations, want %d: actual=%v want=%v", len(actual), len(want), actual, want)
	}
	for index := range want {
		if actual[index] != want[index] {
			t.Fatalf("static allowlist[%d] = %v, want %v", index, actual[index], want[index])
		}
	}
}

func TestMinimalProfileStaticAllowlistIsExact(t *testing.T) {
	assertCompiledDispatcherAllowlist(t, platform.BuildPlan{Components: []string{
		"core", "engine.loader", "admin.files", "admin.coordinator", "admin.identity",
	}})
}
