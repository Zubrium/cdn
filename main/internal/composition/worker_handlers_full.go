//go:build full && !devfixtures && !router

package composition

import (
	"context"
	"errors"
	"fmt"
	"time"

	websiteworkflow "github.com/Zubrium/cdn/engines/website/workflow"
	"github.com/Zubrium/cdn/main/internal/bus"
	appconfig "github.com/Zubrium/cdn/main/internal/config"
	"github.com/Zubrium/cdn/pkg/contracts"
	mediaprofile "github.com/Zubrium/cdn/pkg/contracts/media"
	"github.com/Zubrium/cdn/pkg/observability"
	"github.com/Zubrium/cdn/workers/archive"
	"github.com/Zubrium/cdn/workers/media"
	"github.com/Zubrium/cdn/workers/prerender"
)

func registerWorkerHandlers(selected map[string]struct{}, config appconfig.Resolved, component *coreComponent, observer *observability.Observer) (map[contracts.Key]TaskOperationBinding, error) {
	operations := make(map[contracts.Key]TaskOperationBinding)
	register := func(classID, operationID string, version uint32, handler bus.TaskOperationHandler) error {
		key := contracts.Key{ID: operationID, Version: version}
		if err := key.Validate(); err != nil || classID == "" || handler == nil {
			return contracts.ErrInvalidOperation
		}
		if _, exists := operations[key]; exists {
			return fmt.Errorf("duplicate operation registration: %s/v%d", key.ID, key.Version)
		}
		operations[key] = TaskOperationBinding{TaskClass: classID, Handler: observedTaskOperation(observer, classID, key, handler)}
		return nil
	}

	if _, enabled := selected[archive.DataModuleID]; enabled {
		if err := register(archive.DataModuleID, archive.OperationID, 1, func(ctx context.Context, task contracts.OperationTask) (contracts.OperationOutcome, error) {
			if !config.FeatureImport.Enabled {
				return contracts.OperationOutcome{}, errors.New("archive worker is not configured")
			}
			configured := component.archiveHandle.Load()
			if configured == nil {
				return contracts.OperationOutcome{}, errors.New("archive worker is not configured")
			}
			return configured.WorkerHandler(ctx, task)
		}); err != nil {
			return nil, err
		}
	}
	if _, enabled := selected[media.DataModuleID]; enabled {
		if err := register(media.DataModuleID, media.OperationID, 1, failClosedOperation(media.DataModuleID)); err != nil {
			return nil, err
		}
		if err := register(media.DataModuleID, media.OperationID, uint32(mediaprofile.WorkContractVersion), func(ctx context.Context, task contracts.OperationTask) (contracts.OperationOutcome, error) {
			configured := component.mediaHandle.Load()
			if configured == nil {
				return contracts.OperationOutcome{}, errors.New("media worker is not configured")
			}
			return configured.WorkerHandler(ctx, task)
		}); err != nil {
			return nil, err
		}
	}
	if _, enabled := selected[prerender.DataModuleID]; enabled {
		if err := register(prerender.DataModuleID, prerender.OperationID, 1, func(ctx context.Context, task contracts.OperationTask) (contracts.OperationOutcome, error) {
			configured := component.prerenderHandle.Load()
			if configured == nil {
				return contracts.OperationOutcome{}, errors.New("prerender worker is not configured")
			}
			return configured.WorkerHandler(ctx, task)
		}); err != nil {
			return nil, err
		}
	}
	if _, enabled := selected[websiteworkflow.ComponentID]; enabled {
		handler, err := websiteworkflow.NewHandler(websiteworkflow.Config{MaxAttempts: config.Coordinator.MaxAttempts})
		if err != nil {
			return nil, err
		}
		if err := register(websiteworkflow.ComponentID, websiteworkflow.OperationID, 1, handler.WorkerHandler); err != nil {
			return nil, err
		}
		if err := register(websiteworkflow.ComponentID, websiteworkflow.MediaOperationID, 1, handler.MediaWorkerHandler); err != nil {
			return nil, err
		}
		forkHandler, err := websiteworkflow.NewForkHandler(snapshotForkContentCopierHandle{component: component}, snapshotForkWebsiteFinalizerHandle{component: component}, config.Coordinator.MaxAttempts)
		if err != nil {
			return nil, err
		}
		if err := register(websiteworkflow.ComponentID, websiteworkflow.ForkOperationID, 1, forkHandler.WorkerHandler); err != nil {
			return nil, err
		}
	}
	return operations, nil
}

func failClosedOperation(classID string) bus.TaskOperationHandler {
	return func(ctx context.Context, _ contracts.OperationTask) (contracts.OperationOutcome, error) {
		if err := ctx.Err(); err != nil {
			return contracts.OperationOutcome{}, err
		}
		return contracts.OperationOutcome{}, fmt.Errorf("%s is compiled but has no server-side execution configuration", classID)
	}
}

func observedTaskOperation(observer *observability.Observer, classID string, key contracts.Key, handler bus.TaskOperationHandler) bus.TaskOperationHandler {
	if observer == nil {
		return handler
	}
	return func(ctx context.Context, task contracts.OperationTask) (contracts.OperationOutcome, error) {
		started := time.Now()
		attrs := []observability.Attribute{observability.String("worker.task.id", task.TaskID)}
		if task.WorkflowID != "" {
			attrs = append(attrs, observability.String("workflow.id", task.WorkflowID))
		}
		identity := observability.Identity{
			ModuleKind: observability.ModuleKindWorker, ModuleID: classID,
			ComponentID: classID, OperationID: key.ID,
		}
		var result contracts.OperationOutcome
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
			observability.String("outcome", taskOperationOutcome(err)),
		}
		observer.IncCounter(spanCtx, "zubrium_worker_task_total", 1, labels...)
		observer.ObserveHistogram(spanCtx, "zubrium_worker_task_duration_seconds", time.Since(started).Seconds(), labels...)
		return result, err
	}
}

func taskOperationOutcome(err error) string {
	if err != nil {
		return "error"
	}
	return "ok"
}
