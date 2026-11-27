//go:build full && !devfixtures && !router

package composition

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	platform "github.com/Zubrium/cdn/core/presentation"
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

// isWorkflowOnlyOperation reports operations whose task keys belong to the
// dedicated workflow dispatcher. HTTP ingress keys stay on the component bus.
func isWorkflowOnlyOperation(id string) bool {
	switch id {
	case contracts.PublicationStartOperationID, contracts.PublicationBuildOperationID,
		contracts.PublicationActivateOperationID, contracts.PublicationFinalizeOperationID,
		contracts.RetirementStartOperationID, contracts.RetirementRouteRemoveOperationID,
		contracts.RetirementPageFinalizeOperationID:
		return true
	default:
		return false
	}
}

func legacyTaskOperations(operations map[contracts.Key]TaskOperationBinding) map[contracts.Key]TaskOperationBinding {
	legacy := make(map[contracts.Key]TaskOperationBinding, len(operations))
	for key, binding := range operations {
		if !isWorkflowOnlyOperation(key.ID) {
			legacy[key] = binding
		}
	}
	return legacy
}

// Durable workflow tasks are dispatched through a dedicated, sealed bus whose
// allowlist is derived from the compiled task registry. The public component
// dispatcher keeps its exact static allowlist and remains the ingress for
// workflow-control and component operations.
var (
	workflowTaskDispatcher *bus.Dispatcher
	workflowTaskOperations map[contracts.Key]TaskOperationBinding
)

func registerWorkerHandlers(selected map[string]struct{}, config appconfig.Resolved, component *coreComponent, observer *observability.Observer) (map[contracts.Key]TaskOperationBinding, error) {
	operations := make(map[contracts.Key]TaskOperationBinding)
	register := func(classID, operationID string, version uint32, enabled bool, handler bus.TaskOperationHandler) error {
		key := contracts.Key{ID: operationID, Version: version}
		if err := key.Validate(); err != nil || classID == "" || handler == nil {
			return contracts.ErrInvalidOperation
		}
		if _, exists := operations[key]; exists {
			return fmt.Errorf("duplicate operation registration: %s/v%d", key.ID, key.Version)
		}
		operations[key] = TaskOperationBinding{TaskClass: classID,
			Handler: observedTaskOperation(observer, classID, key, configuredOperation(classID, enabled, handler))}
		return nil
	}

	if _, compiled := selected[archive.DataModuleID]; compiled {
		if err := register(archive.DataModuleID, archive.OperationID, 1, config.Scales.WorkerPools.Archive.Enabled, func(ctx context.Context, task contracts.OperationTask) (contracts.OperationOutcome, error) {
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
	if _, compiled := selected[media.DataModuleID]; compiled {
		if err := register(media.DataModuleID, media.OperationID, 1, true, failClosedOperation(media.DataModuleID)); err != nil {
			return nil, err
		}
		if err := register(media.DataModuleID, media.OperationID, uint32(mediaprofile.WorkContractVersion), config.Scales.WorkerPools.Media.Enabled, func(ctx context.Context, task contracts.OperationTask) (contracts.OperationOutcome, error) {
			configured := component.mediaHandle.Load()
			if configured == nil {
				return contracts.OperationOutcome{}, errors.New("media worker is not configured")
			}
			return configured.WorkerHandler(ctx, task)
		}); err != nil {
			return nil, err
		}
	}
	if _, compiled := selected[prerender.DataModuleID]; compiled {
		if err := register(prerender.DataModuleID, prerender.OperationID, 1, config.Scales.WorkerPools.Prerender.Enabled, func(ctx context.Context, task contracts.OperationTask) (contracts.OperationOutcome, error) {
			configured := component.prerenderHandle.Load()
			if configured == nil {
				return contracts.OperationOutcome{}, errors.New("prerender worker is not configured")
			}
			return configured.WorkerHandler(ctx, task)
		}); err != nil {
			return nil, err
		}
	}
	if _, compiled := selected[websiteworkflow.ComponentID]; compiled {
		handler, err := websiteworkflow.NewHandler(websiteworkflow.Config{MaxAttempts: config.Coordinator.MaxAttempts})
		if err != nil {
			return nil, err
		}
		if err := register(websiteworkflow.ComponentID, websiteworkflow.OperationID, 1, config.Scales.WorkerPools.Publication.Enabled, handler.WorkerHandler); err != nil {
			return nil, err
		}
		if err := register(websiteworkflow.ComponentID, websiteworkflow.MediaOperationID, 1, config.Scales.WorkerPools.Publication.Enabled, handler.MediaWorkerHandler); err != nil {
			return nil, err
		}
		forkHandler, err := websiteworkflow.NewForkHandler(config.Coordinator.MaxAttempts)
		if err != nil {
			return nil, err
		}
		if err := register(websiteworkflow.ComponentID, websiteworkflow.ForkOperationID, 1, config.Scales.WorkerPools.Publication.Enabled, forkHandler.WorkerHandler); err != nil {
			return nil, err
		}
		if _, compiled := selected["engine.content"]; compiled {
			if err := register("engine.content", websiteworkflow.ForkCopyOperationID, websiteworkflow.ForkChildContractVersion,
				config.Scales.WorkerPools.Publication.Enabled, snapshotForkContentCopierHandle{component: component}.WorkerHandler); err != nil {
				return nil, err
			}
		}
		if err := register(websiteworkflow.ComponentID, websiteworkflow.ForkFinalizeOperationID, websiteworkflow.ForkChildContractVersion,
			config.Scales.WorkerPools.Publication.Enabled, snapshotForkWebsiteFinalizerHandle{component: component}.WorkerHandler); err != nil {
			return nil, err
		}
	}
	if err := registerContentBridgeWorkerHandlers(register, selected, config, component); err != nil {
		return nil, err
	}
	if err := registerPublicationWorkflowWorkerHandlers(register, selected, config, component); err != nil {
		return nil, err
	}
	if err := registerRetirementWorkflowWorkerHandlers(register, selected, config, component); err != nil {
		return nil, err
	}
	workflowTaskOperations = make(map[contracts.Key]TaskOperationBinding, len(operations))
	for key, binding := range operations {
		if isWorkflowOnlyOperation(key.ID) {
			workflowTaskOperations[key] = binding
		}
	}
	if config.Scales.Bus.MaxInFlight <= 0 || len(operations) == 0 {
		return legacyTaskOperations(operations), nil
	}
	keys := make([]contracts.Key, 0, len(operations))
	handlers := make(map[contracts.Key]bus.TaskOperationHandler, len(operations))
	for key, binding := range operations {
		keys = append(keys, key)
		handlers[key] = binding.Handler
	}
	dispatcher, err := bus.NewAllowlisted(bus.ConcurrencyLimits{MaxInFlight: config.Scales.Bus.MaxInFlight}, keys...)
	if err != nil {
		return nil, err
	}
	if err := bus.RegisterTaskOperations(dispatcher, handlers); err != nil {
		return nil, err
	}
	if err := dispatcher.Seal(); err != nil {
		return nil, err
	}
	workflowTaskDispatcher = dispatcher
	return legacyTaskOperations(operations), nil
}

// compiledWorkerClasses is the union of task classes present in this binary,
// including classes whose pool is disabled. It exists so engines can still
// construct scoped workflow clients whose tasks wait in the durable store
// until the corresponding pool is explicitly enabled.
func compiledWorkerClasses(selected map[string]struct{}) []string {
	classes := make([]string, 0, 8)
	for _, id := range []string{
		archive.DataModuleID, media.DataModuleID, prerender.DataModuleID, websiteworkflow.ComponentID,
		"engine.content", "engine.publication", "engine.router", "composition.retirement",
	} {
		if _, compiled := selected[id]; compiled {
			classes = append(classes, id)
		}
	}
	sort.Strings(classes)
	return classes
}

// workerTaskPools builds one isolated Core pool per enabled worker class.
// Disabled classes keep fail-closed registry bindings so cross-class children
// remain resolvable, but they produce no pool and cannot claim work or borrow
// another pool's limits.
func workerTaskPools(config appconfig.Resolved, dispatcher *bus.Dispatcher, operations map[contracts.Key]TaskOperationBinding) ([]platform.TaskPoolConfig, error) {
	if workflowTaskDispatcher != nil {
		dispatcher = workflowTaskDispatcher
		all := make(map[contracts.Key]TaskOperationBinding, len(operations)+len(workflowTaskOperations))
		workflowTaskDispatcher = nil
		for key, binding := range operations {
			all[key] = binding
		}
		for key, binding := range workflowTaskOperations {
			all[key] = binding
		}
		operations = all
	}
	specs := make([]TaskPoolSpec, 0, 5)
	add := func(classID string, pool appconfig.ResolvedWorkerPool) {
		if pool.Enabled {
			specs = append(specs, TaskPoolSpec{ClassID: classID, Concurrency: pool.Limit,
				LeaseDuration: pool.LeaseDuration, TaskTimeout: pool.TaskTimeout, PollInterval: pool.PollInterval})
		}
	}
	add(archive.DataModuleID, config.Scales.WorkerPools.Archive)
	add(media.DataModuleID, config.Scales.WorkerPools.Media)
	add(prerender.DataModuleID, config.Scales.WorkerPools.Prerender)
	add(websiteworkflow.ComponentID, config.Scales.WorkerPools.Publication)
	if _, registered := operations[contracts.Key{ID: websiteworkflow.ForkCopyOperationID, Version: websiteworkflow.ForkChildContractVersion}]; registered {
		add("engine.content", config.Scales.WorkerPools.Publication)
	}
	add("engine.publication", config.Scales.WorkerPools.Publication)
	add("engine.router", config.Scales.WorkerPools.Publication)
	add("composition.retirement", config.Scales.WorkerPools.Publication)
	return NewDataBusTaskPools(dispatcher, operations, specs)
}

func registerPublicationWorkflowWorkerHandlers(register func(classID, operationID string, version uint32, enabled bool, handler bus.TaskOperationHandler) error, selected map[string]struct{}, config appconfig.Resolved, component *coreComponent) error {
	if _, compiled := selected["engine.publication"]; !compiled {
		return nil
	}
	enabled := config.Scales.WorkerPools.Publication.Enabled
	root, err := newPublicationRootHandler(config.Coordinator.MaxAttempts)
	if err != nil {
		return err
	}
	if err := register("engine.publication", contracts.PublicationStartOperationID, contracts.PublicationStartContractVersion,
		enabled, root); err != nil {
		return err
	}
	if _, compiled := selected[websiteworkflow.ComponentID]; compiled {
		if err := register(websiteworkflow.ComponentID, contracts.PublicationBuildOperationID, contracts.PublicationChildContractVersion,
			enabled, bindWorkflowCore(component, func(ctx context.Context, task contracts.OperationTask, core *coreComponent) (contracts.OperationOutcome, error) {
				return (publicationBuildHandler{component: core}).WorkerHandler(ctx, task)
			})); err != nil {
			return err
		}
		if err := register(websiteworkflow.ComponentID, contracts.PublicationFinalizeOperationID, contracts.PublicationChildContractVersion,
			enabled, publicationFinalizeHandler{}.WorkerHandler); err != nil {
			return err
		}
	}
	if _, compiled := selected["engine.router"]; compiled {
		if err := register("engine.router", contracts.PublicationActivateOperationID, contracts.PublicationChildContractVersion,
			enabled, bindWorkflowCore(component, func(ctx context.Context, task contracts.OperationTask, core *coreComponent) (contracts.OperationOutcome, error) {
				return (publicationActivateHandler{component: core}).WorkerHandler(ctx, task)
			})); err != nil {
			return err
		}
	}
	return nil
}

func registerRetirementWorkflowWorkerHandlers(register func(classID, operationID string, version uint32, enabled bool, handler bus.TaskOperationHandler) error, selected map[string]struct{}, config appconfig.Resolved, component *coreComponent) error {
	if _, compiled := selected["composition.retirement"]; !compiled {
		return nil
	}
	enabled := config.Scales.WorkerPools.Publication.Enabled
	root, err := newRetirementRootHandler(config.Coordinator.MaxAttempts)
	if err != nil {
		return err
	}
	if err := register("composition.retirement", contracts.RetirementStartOperationID, contracts.RetirementStartContractVersion,
		enabled, root); err != nil {
		return err
	}
	if _, compiled := selected["engine.router"]; compiled {
		if err := register("engine.router", contracts.RetirementRouteRemoveOperationID, contracts.RetirementChildContractVersion,
			enabled, bindWorkflowCore(component, func(ctx context.Context, task contracts.OperationTask, core *coreComponent) (contracts.OperationOutcome, error) {
				return (retirementRouteRemoveHandler{component: core}).WorkerHandler(ctx, task)
			})); err != nil {
			return err
		}
	}
	if _, compiled := selected[websiteworkflow.ComponentID]; compiled {
		if err := register(websiteworkflow.ComponentID, contracts.RetirementPageFinalizeOperationID, contracts.RetirementChildContractVersion,
			enabled, bindWorkflowCore(component, func(ctx context.Context, task contracts.OperationTask, core *coreComponent) (contracts.OperationOutcome, error) {
				return (retirementFinalizeHandler{component: core}).WorkerHandler(ctx, task)
			})); err != nil {
			return err
		}
	}
	return nil
}

func bindWorkflowCore(component *coreComponent, handler func(context.Context, contracts.OperationTask, *coreComponent) (contracts.OperationOutcome, error)) bus.TaskOperationHandler {
	return func(ctx context.Context, task contracts.OperationTask) (contracts.OperationOutcome, error) {
		return handler(ctx, task, component)
	}
}

func configuredOperation(classID string, enabled bool, handler bus.TaskOperationHandler) bus.TaskOperationHandler {
	if !enabled || handler == nil {
		return failClosedOperation(classID)
	}
	return handler
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
