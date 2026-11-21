//go:build full && !devfixtures && !router

package composition

import (
	platform "github.com/Zubrium/cdn/core/presentation"
	"github.com/Zubrium/cdn/workers/archive"
	"github.com/Zubrium/cdn/workers/media"
	"github.com/Zubrium/cdn/workers/prerender"
)

func registerWorkerHandlers(registry *platform.RuntimeRegistry, selected map[string]struct{}) error {
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
	return nil
}
