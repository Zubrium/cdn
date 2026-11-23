//go:build full && !devfixtures && !router

package composition

import (
	"context"
	"errors"
	"strconv"
	"time"

	core "github.com/Zubrium/cdn/core/presentation"
	websiteworkflow "github.com/Zubrium/cdn/engines/website/workflow"
	appconfig "github.com/Zubrium/cdn/main/internal/config"
	mediaprofile "github.com/Zubrium/cdn/pkg/contracts/media"
	"github.com/Zubrium/cdn/pkg/observability"
	"github.com/Zubrium/cdn/workers/archive"
	"github.com/Zubrium/cdn/workers/media"
	"github.com/Zubrium/cdn/workers/prerender"
	"github.com/google/uuid"
)

func registerWorkerHandlers(registry *core.RuntimeRegistry, selected map[string]struct{}, config appconfig.Resolved, component *coreComponent, observer *observability.Observer) error {
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
		if err := registry.RegisterWorkerHandlers(componentID, observedWorkerHandlers(observer, componentID, handlers)); err != nil {
			return err
		}
	}
	if _, enabled := selected[websiteworkflow.ComponentID]; enabled {
		handler, err := websiteworkflow.NewHandler(websiteworkflow.Config{MaxAttempts: config.Coordinator.MaxAttempts})
		if err != nil {
			return err
		}
		workflowHandlers := map[string]core.WorkerHandler{
			websiteworkflow.OperationID + "/v1":      handler.WorkerHandler,
			websiteworkflow.MediaOperationID + "/v1": handler.MediaWorkerHandler,
		}
		forkHandler, err := websiteworkflow.NewForkHandler(snapshotForkContentCopierHandle{component: component}, snapshotForkWebsiteFinalizerHandle{component: component}, config.Coordinator.MaxAttempts)
		if err != nil {
			return err
		}
		workflowHandlers[websiteworkflow.ForkOperationID+"/v1"] = forkHandler.WorkerHandler
		if err := registry.RegisterWorkerHandlers(websiteworkflow.ComponentID, observedWorkerHandlers(observer, websiteworkflow.ComponentID, workflowHandlers)); err != nil {
			return err
		}
	}
	return nil
}

func workerOutcome(err error) string {
	if err != nil {
		return "error"
	}
	return "ok"
}

func observedWorkerHandlers(observer *observability.Observer, componentID string, handlers map[string]core.WorkerHandler) map[string]core.WorkerHandler {
	if observer == nil {
		return handlers
	}
	observed := make(map[string]core.WorkerHandler, len(handlers))
	for operationID, handler := range handlers {
		operationID, handler := operationID, handler
		observed[operationID] = func(ctx context.Context, task core.Task) (core.TaskOutcome, error) {
			var result core.TaskOutcome
			started := time.Now()
			attrs := []observability.Attribute{observability.Int("worker.task.attempt", int64(task.Attempts))}
			if task.ID != uuid.Nil {
				attrs = append(attrs, observability.String("worker.task.id", task.ID.String()))
			}
			if task.WorkflowID != uuid.Nil {
				attrs = append(attrs, observability.String("workflow.id", task.WorkflowID.String()))
			}
			identity := observability.Identity{
				ModuleKind: observability.ModuleKindWorker, ModuleID: componentID,
				ComponentID: componentID, OperationID: operationID,
			}
			var spanCtx context.Context
			err := observer.Operation(ctx, identity, "worker.task", attrs, func(operationCtx context.Context) error {
				spanCtx = operationCtx
				var callErr error
				result, callErr = handler(operationCtx, task)
				return callErr
			})
			labels := []observability.Attribute{
				observability.String("module.kind", string(identity.ModuleKind)),
				observability.String("module.id", identity.ModuleID),
				observability.String("component.id", identity.ComponentID),
				observability.String("operation.id", identity.OperationID),
				observability.String("outcome", workerOutcome(err)),
			}
			observer.IncCounter(spanCtx, "zubrium_worker_task_total", 1, labels...)
			observer.ObserveHistogram(spanCtx, "zubrium_worker_task_duration_seconds", time.Since(started).Seconds(), labels...)
			return result, err
		}
	}
	return observed
}
