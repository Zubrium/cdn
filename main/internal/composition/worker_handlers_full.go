//go:build full && !devfixtures && !router

package composition

import (
	"context"
	"errors"

	core "github.com/Zubrium/cdn/core/presentation"
	websiteworkflow "github.com/Zubrium/cdn/engines/website/workflow"
	appconfig "github.com/Zubrium/cdn/main/internal/config"
	"github.com/Zubrium/cdn/workers/archive"
	"github.com/Zubrium/cdn/workers/media"
	"github.com/Zubrium/cdn/workers/prerender"
)

func registerWorkerHandlers(registry *core.RuntimeRegistry, selected map[string]struct{}, config appconfig.Resolved, component *coreComponent) error {
	handlers := []struct {
		componentID string
		operationID string
	}{
		{componentID: "worker.archive", operationID: archive.OperationID},
		{componentID: "worker.media", operationID: media.OperationID},
		{componentID: "worker.prerender", operationID: prerender.OperationID},
	}
	for _, worker := range handlers {
		if _, enabled := selected[worker.componentID]; !enabled {
			continue
		}
		handler := failClosedWorker(worker.componentID)
		if worker.componentID == archive.DataModuleID && config.FeatureImport.Enabled {
			handler = func(ctx context.Context, task core.Task) (core.TaskOutcome, error) {
				configured := component.archiveHandle.Load()
				if configured == nil {
					return core.TaskOutcome{}, errors.New("archive worker is not configured")
				}
				return configured.WorkerHandler(ctx, task)
			}
		}
		err := registry.RegisterWorkerHandlers(worker.componentID, map[string]core.WorkerHandler{
			worker.operationID + "/v1": handler,
		})
		if err != nil {
			return err
		}
	}
	if _, enabled := selected[websiteworkflow.ComponentID]; enabled {
		handler, err := websiteworkflow.NewHandler(websiteworkflow.Config{MaxAttempts: config.Coordinator.MaxAttempts})
		if err != nil {
			return err
		}
		if err := registry.RegisterWorkerHandlers(websiteworkflow.ComponentID, map[string]core.WorkerHandler{
			websiteworkflow.OperationID + "/v1": handler.WorkerHandler,
		}); err != nil {
			return err
		}
	}
	return nil
}
