//go:build full && !devfixtures && !router

package composition

import (
	"context"
	"errors"
	"strconv"

	core "github.com/Zubrium/cdn/core/presentation"
	websiteworkflow "github.com/Zubrium/cdn/engines/website/workflow"
	appconfig "github.com/Zubrium/cdn/main/internal/config"
	mediaprofile "github.com/Zubrium/cdn/pkg/contracts/media"
	"github.com/Zubrium/cdn/workers/archive"
	"github.com/Zubrium/cdn/workers/media"
	"github.com/Zubrium/cdn/workers/prerender"
)

func registerWorkerHandlers(registry *core.RuntimeRegistry, selected map[string]struct{}, config appconfig.Resolved, component *coreComponent) error {
	workers := []string{"worker.archive", "worker.media", "worker.prerender"}
	for _, componentID := range workers {
		if _, enabled := selected[componentID]; !enabled {
			continue
		}
		handlers := map[string]core.WorkerHandler{}
		switch componentID {
		case archive.DataModuleID:
			handlers[archive.OperationID+"/v1"] = func(ctx context.Context, task core.Task) (core.TaskOutcome, error) {
				if !config.FeatureImport.Enabled {
					return core.TaskOutcome{}, errors.New("archive worker is not configured")
				}
				configured := component.archiveHandle.Load()
				if configured == nil {
					return core.TaskOutcome{}, errors.New("archive worker is not configured")
				}
				return configured.WorkerHandler(ctx, task)
			}
		case media.DataModuleID:
			handlers[media.OperationID+"/v1"] = failClosedWorker(componentID)
			handlers[media.OperationID+"/v"+strconv.Itoa(mediaprofile.WorkContractVersion)] = func(ctx context.Context, task core.Task) (core.TaskOutcome, error) {
				configured := component.mediaHandle.Load()
				if configured == nil {
					return core.TaskOutcome{}, errors.New("media worker is not configured")
				}
				return configured.WorkerHandler(ctx, task)
			}
		case prerender.DataModuleID:
			handlers[prerender.OperationID+"/v1"] = func(ctx context.Context, task core.Task) (core.TaskOutcome, error) {
				configured := component.prerenderHandle.Load()
				if configured == nil {
					return core.TaskOutcome{}, errors.New("prerender worker is not configured")
				}
				return configured.WorkerHandler(ctx, task)
			}
		}
		if err := registry.RegisterWorkerHandlers(componentID, handlers); err != nil {
			return err
		}
	}
	if _, enabled := selected[websiteworkflow.ComponentID]; enabled {
		handler, err := websiteworkflow.NewHandler(websiteworkflow.Config{MaxAttempts: config.Coordinator.MaxAttempts})
		if err != nil {
			return err
		}
		if err := registry.RegisterWorkerHandlers(websiteworkflow.ComponentID, map[string]core.WorkerHandler{
			websiteworkflow.OperationID + "/v1":      handler.WorkerHandler,
			websiteworkflow.MediaOperationID + "/v1": handler.MediaWorkerHandler,
		}); err != nil {
			return err
		}
	}
	return nil
}
