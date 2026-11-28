//go:build full && website_orchestration_acceptance

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	core "github.com/Zubrium/cdn/core/presentation"
	"github.com/Zubrium/cdn/main/internal/composition"
	"github.com/Zubrium/cdn/pkg/contracts"
)

func websiteStartOrchestration(t *testing.T, ctx context.Context, runtime *core.Runtime, scenarioID string,
	bindings map[contracts.Key]composition.TaskOperationBinding, rootClass string,
) {
	t.Helper()
	if err := runtime.Registry.Register("coordinator"); err != nil {
		t.Fatalf("register Coordinator: %v", err)
	}
	dispatcher := websiteAcceptanceExecutor(t, bindings)
	classes := []string{rootClass, "engine.website", "engine.router"}
	specs := make([]composition.TaskPoolSpec, 0, len(classes))
	for _, class := range classes {
		specs = append(specs, composition.TaskPoolSpec{ClassID: class, Concurrency: 2,
			LeaseDuration: 20 * time.Second, TaskTimeout: 90 * time.Second, PollInterval: 25 * time.Millisecond})
	}
	fingerprint := sha256.Sum256([]byte(scenarioID))
	if err := runtime.StartCoordinator(ctx, core.CoordinatorRuntimeConfig{
		Fingerprint: hex.EncodeToString(fingerprint[:]), InstanceLease: 30 * time.Second, HeartbeatInterval: 5 * time.Second,
		RetryBase: 50 * time.Millisecond, RetryMaximum: 250 * time.Millisecond,
		MaxAttempts: 3, MaxWorkflowTasks: 32, MaxChildrenPerTask: 8,
		WorkflowRetention: time.Hour, ShutdownTimeout: 15 * time.Second,
		TaskClasses: classes, Pools: websiteAcceptancePools(t, dispatcher, bindings, specs...),
	}); err != nil {
		t.Fatalf("start disposable Coordinator: %v", err)
	}
	t.Cleanup(func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := runtime.StopCoordinator(shutdown); err != nil {
			t.Errorf("stop disposable Coordinator: %v", err)
		}
	})
	if err := runtime.ActivateCoordinator(ctx); err != nil {
		t.Fatalf("activate disposable Coordinator: %v", err)
	}
}
