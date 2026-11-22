//go:build full && !devfixtures && !router

package composition

import (
	platform "github.com/Zubrium/cdn/core/presentation"
	websiteworkflow "github.com/Zubrium/cdn/engines/website/workflow"
	appconfig "github.com/Zubrium/cdn/main/internal/config"
	"github.com/Zubrium/cdn/workers/archive"
	"github.com/Zubrium/cdn/workers/media"
	"github.com/Zubrium/cdn/workers/prerender"
)

func registerWorkerHandlers(registry *platform.RuntimeRegistry, selected map[string]struct{}, config appconfig.Resolved) error {
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
		err := registry.RegisterWorkerHandlers(worker.componentID, map[string]platform.WorkerHandler{
			worker.operationID + "/v1": failClosedWorker(worker.componentID),
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
		if err := registry.RegisterWorkerHandlers(websiteworkflow.ComponentID, map[string]platform.WorkerHandler{
			websiteworkflow.OperationID + "/v1": handler.WorkerHandler,
		}); err != nil {
			return err
		}
	}
	return nil
}
