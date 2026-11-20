package coordinator

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"
)

var ErrComponentUnavailable = errors.New("component is not compiled into this application")

// ManagedComponent is the uniform start/readiness/stop boundary used by the
// composition root. Engines communicate through adapters registered here.
type ManagedComponent interface {
	Start(context.Context) error
	Ready(context.Context) error
	Stop(context.Context) error
}

type CurrentReadiness interface{ IsReady() bool }
type FailureSource interface{ Errors() <-chan error }
type ActivatableComponent interface{ Activate(context.Context) error }
type FinalActivator interface{ ActivateAfterComponents(context.Context) error }
type PreStopComponent interface{ QuiesceBeforeComponents(context.Context) error }

type ComponentFactory func(context.Context) (ManagedComponent, error)

type RuntimeComponent struct {
	ID       string
	Requires []string
	New      ComponentFactory
}

type RuntimeRegistry struct {
	mu             sync.RWMutex
	components     map[string]RuntimeComponent
	workerHandlers map[string]map[string]WorkerHandler
}

func NewRuntimeRegistry() *RuntimeRegistry {
	return &RuntimeRegistry{components: make(map[string]RuntimeComponent)}
}

func (r *RuntimeRegistry) Register(component RuntimeComponent) error {
	if r == nil || component.ID == "" || component.New == nil {
		return ErrInvalid
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.components == nil {
		r.components = make(map[string]RuntimeComponent)
	}
	if _, exists := r.components[component.ID]; exists {
		return ErrDuplicate
	}
	component.Requires = append([]string(nil), component.Requires...)
	r.components[component.ID] = component
	return nil
}

// RegisterWorkerHandlers installs statically compiled operation handlers for a
// generic component. The selected composition is validated before startup.
func (r *RuntimeRegistry) RegisterWorkerHandlers(componentID string, handlers map[string]WorkerHandler) error {
	if r == nil || !componentIDPattern.MatchString(componentID) || len(handlers) == 0 {
		return ErrInvalid
	}
	copyHandlers := make(map[string]WorkerHandler, len(handlers))
	for operationType, handler := range handlers {
		if _, _, valid := parseRegisteredWorkerType(operationType); !valid || handler == nil {
			return ErrInvalid
		}
		copyHandlers[operationType] = handler
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.workerHandlers == nil {
		r.workerHandlers = make(map[string]map[string]WorkerHandler)
	}
	if _, exists := r.workerHandlers[componentID]; exists {
		return ErrDuplicate
	}
	r.workerHandlers[componentID] = copyHandlers
	return nil
}

// CompileWorkerCatalog exposes a defensive immutable view of the handlers that
// match a selected profile. Composition uses the same method before startup so
// missing handlers fail before Core or any listener has side effects.
func (r *RuntimeRegistry) CompileWorkerCatalog(plan BuildPlan) (*WorkerCatalog, error) {
	if r == nil {
		return nil, ErrInvalid
	}
	r.mu.RLock()
	providers := make([]ComponentWorkerSet, 0, len(r.workerHandlers))
	for componentID, handlers := range r.workerHandlers {
		providers = append(providers, ComponentWorkerSet{ComponentID: componentID, Handlers: handlers})
	}
	r.mu.RUnlock()
	return BuildWorkerCatalog(plan, providers)
}

func (r *RuntimeRegistry) lookup(id string) (RuntimeComponent, bool) {
	if r == nil {
		return RuntimeComponent{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	component, ok := r.components[id]
	return component, ok
}

type startedComponent struct {
	id        string
	component ManagedComponent
}

// Runtime owns initialized components and always shuts them down in reverse
// dependency order. It does not expose one engine instance to another.
type Runtime struct {
	mu              sync.RWMutex
	started         []startedComponent
	ready           bool
	failed          bool
	closing         bool
	closeErr        error
	closeDone       chan struct{}
	shutdownTimeout time.Duration
	monitorCancel   context.CancelFunc
	monitorWG       sync.WaitGroup
	failures        chan error
	catalog         *WorkerCatalog
}

func Compose(ctx context.Context, plan BuildPlan, registry *RuntimeRegistry, shutdownTimeout time.Duration) (*Runtime, error) {
	if ctx == nil || registry == nil || len(plan.Components) == 0 || shutdownTimeout <= 0 {
		return nil, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(plan.Components) == 0 {
		return nil, ErrInvalid
	}
	selected := make(map[string]bool, len(plan.Components))
	seen := make(map[string]bool, len(plan.Components))
	for _, id := range plan.Components {
		if !componentIDPattern.MatchString(id) || selected[id] {
			return nil, ErrInvalid
		}
		selected[id] = true
	}
	for _, id := range plan.Components {
		definition, ok := registry.lookup(id)
		if !ok {
			return nil, fmt.Errorf("%w: %s", ErrComponentUnavailable, id)
		}
		if definition.ID != id {
			return nil, ErrInvalid
		}
		for _, dependency := range definition.Requires {
			if !selected[dependency] || !seen[dependency] {
				return nil, fmt.Errorf("component %q dependency %q is unavailable or ordered later", id, dependency)
			}
		}
		seen[id] = true
	}
	catalog, err := registry.CompileWorkerCatalog(plan)
	if err != nil {
		return nil, err
	}
	runtime := &Runtime{closeDone: make(chan struct{}), failures: make(chan error, len(plan.Components)), shutdownTimeout: shutdownTimeout, catalog: catalog}
	cleanup := func() error {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
		defer cancel()
		return runtime.stopStarted(cleanupCtx)
	}
	active := make(map[string]bool, len(plan.Components))
	for _, id := range plan.Components {
		if err := ctx.Err(); err != nil {
			return nil, errors.Join(err, cleanup())
		}
		definition, ok := registry.lookup(id)
		if !ok {
			return nil, errors.Join(fmt.Errorf("%w: %s", ErrComponentUnavailable, id), cleanup())
		}
		if definition.ID != id {
			return nil, errors.Join(ErrInvalid, cleanup())
		}
		for _, dependency := range definition.Requires {
			if !active[dependency] {
				return nil, errors.Join(fmt.Errorf("component %q dependency %q is unavailable or ordered later", id, dependency), cleanup())
			}
		}
		component, err := invokeFactory(id, definition.New, ctx)
		if err != nil {
			return nil, errors.Join(fmt.Errorf("initialize component %q: %w", id, err), cleanup())
		}
		if isNilManagedComponent(component) {
			return nil, errors.Join(fmt.Errorf("%w: component %q factory returned nil", ErrInvalid, id), cleanup())
		}
		runtime.started = append(runtime.started, startedComponent{id: id, component: component})
		if err := invokeLifecycle(id, "start", func() error { return component.Start(ctx) }); err != nil {
			cleanupErr := cleanup()
			return nil, errors.Join(fmt.Errorf("start component %q: %w", id, err), cleanupErr)
		}
		active[id] = true
	}
	for _, item := range runtime.started {
		if err := ctx.Err(); err != nil {
			cleanupErr := cleanup()
			return nil, errors.Join(err, cleanupErr)
		}
		if err := invokeLifecycle(item.id, "readiness", func() error { return item.component.Ready(ctx) }); err != nil {
			cleanupErr := cleanup()
			return nil, errors.Join(fmt.Errorf("component %q is not ready: %w", item.id, err), cleanupErr)
		}
	}
	for _, item := range runtime.started {
		activator, ok := item.component.(ActivatableComponent)
		if !ok {
			continue
		}
		if err := ctx.Err(); err != nil {
			cleanupErr := cleanup()
			return nil, errors.Join(err, cleanupErr)
		}
		if err := invokeLifecycle(item.id, "activation", func() error { return activator.Activate(ctx) }); err != nil {
			cleanupErr := cleanup()
			return nil, errors.Join(fmt.Errorf("activate component %q: %w", item.id, err), cleanupErr)
		}
	}
	// Core starts durable workers only after every selected engine and
	// presentation component has passed readiness and ordinary activation.
	for _, item := range runtime.started {
		activator, ok := item.component.(FinalActivator)
		if !ok {
			continue
		}
		if err := ctx.Err(); err != nil {
			cleanupErr := cleanup()
			return nil, errors.Join(err, cleanupErr)
		}
		if err := invokeLifecycle(item.id, "final activation", func() error {
			return activator.ActivateAfterComponents(ctx)
		}); err != nil {
			cleanupErr := cleanup()
			return nil, errors.Join(fmt.Errorf("activate component %q: %w", item.id, err), cleanupErr)
		}
	}
	if err := ctx.Err(); err != nil {
		cleanupErr := cleanup()
		return nil, errors.Join(err, cleanupErr)
	}
	failureSources := make([]struct {
		id     string
		errors <-chan error
	}, 0, len(runtime.started))
	for _, item := range runtime.started {
		source, ok := item.component.(FailureSource)
		if !ok {
			continue
		}
		failures, err := invokeFailureSource(item.id, source)
		if err != nil {
			cleanupErr := cleanup()
			return nil, errors.Join(err, cleanupErr)
		}
		if failures == nil {
			cleanupErr := cleanup()
			return nil, errors.Join(fmt.Errorf("component %q returned a nil failure stream: %w", item.id, ErrInvalid), cleanupErr)
		}
		failureSources = append(failureSources, struct {
			id     string
			errors <-chan error
		}{id: item.id, errors: failures})
	}
	runtime.mu.Lock()
	runtime.ready = true
	runtime.mu.Unlock()
	monitorCtx, monitorCancel := context.WithCancel(context.Background())
	runtime.monitorCancel = monitorCancel
	for _, source := range failureSources {
		runtime.monitorWG.Add(1)
		go runtime.monitorFailures(monitorCtx, source.id, source.errors)
	}
	return runtime, nil
}

// WorkerCatalog returns the immutable handler set compiled into this runtime.
// A composition without task handlers has no task catalog.
func (r *Runtime) WorkerCatalog() *WorkerCatalog {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.catalog
}

func (r *Runtime) Ready() bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	if !r.ready || r.closing || r.failed {
		r.mu.RUnlock()
		return false
	}
	items := append([]startedComponent(nil), r.started...)
	r.mu.RUnlock()
	for _, item := range items {
		probe, ok := item.component.(CurrentReadiness)
		if !ok {
			continue
		}
		ready, panicked := currentReadiness(item.id, probe)
		if panicked {
			r.failComponent(item.id)
			return false
		}
		if !ready {
			return false
		}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.ready && !r.closing && !r.failed
}

func (r *Runtime) Failures() <-chan error {
	if r == nil {
		ch := make(chan error)
		close(ch)
		return ch
	}
	return r.failures
}

func (r *Runtime) monitorFailures(ctx context.Context, id string, source <-chan error) {
	defer r.monitorWG.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case err, ok := <-source:
			if !ok {
				r.failComponent(id)
				return
			}
			if err == nil {
				continue
			}
			r.failComponent(id)
			return
		}
	}
}

func (r *Runtime) Close(ctx context.Context) error {
	if r == nil {
		return nil
	}
	if ctx == nil {
		return ErrInvalid
	}
	r.mu.Lock()
	if r.closing {
		done := r.closeDone
		r.mu.Unlock()
		select {
		case <-done:
			r.mu.RLock()
			defer r.mu.RUnlock()
			return r.closeErr
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	r.closing = true
	r.ready = false
	items := append([]startedComponent(nil), r.started...)
	r.started = nil
	r.mu.Unlock()
	shutdownCtx, shutdownCancel := context.WithTimeout(ctx, r.shutdownTimeout)
	defer shutdownCancel()
	if r.monitorCancel != nil {
		r.monitorCancel()
	}
	var closeErr error
	closeErr = errors.Join(closeErr, stopComponents(items, shutdownCtx))
	r.monitorWG.Wait()
	r.mu.Lock()
	close(r.failures)
	r.closeErr = closeErr
	close(r.closeDone)
	r.mu.Unlock()
	return closeErr
}

func (r *Runtime) stopStarted(ctx context.Context) error {
	stopErr := stopComponents(r.started, ctx)
	r.started = nil
	return stopErr
}

func stopComponents(items []startedComponent, ctx context.Context) error {
	var stopErr error
	// Core quiesces durable workers while their engine dependencies and database
	// are still alive. Network components are already unready once close begins.
	for _, item := range items {
		quiescer, ok := item.component.(PreStopComponent)
		if !ok {
			continue
		}
		if err := invokeLifecycle(item.id, "quiesce", func() error { return quiescer.QuiesceBeforeComponents(ctx) }); err != nil {
			stopErr = errors.Join(stopErr, fmt.Errorf("quiesce component %q: %w", item.id, err))
		}
	}
	for i := len(items) - 1; i >= 0; i-- {
		item := items[i]
		if err := invokeLifecycle(item.id, "stop", func() error { return item.component.Stop(ctx) }); err != nil {
			stopErr = errors.Join(stopErr, fmt.Errorf("stop component %q: %w", item.id, err))
		}
	}
	return stopErr
}

func (r *Runtime) failComponent(id string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closing || r.failed {
		return
	}
	r.failed = true
	r.ready = false
	select {
	case r.failures <- fmt.Errorf("component %q reported a fatal failure", id):
	default:
	}
}

func invokeFactory(id string, factory ComponentFactory, ctx context.Context) (component ManagedComponent, err error) {
	defer func() {
		if recover() != nil {
			component = nil
			err = fmt.Errorf("%w in component %q factory", ErrComponentPanic, id)
		}
	}()
	return factory(ctx)
}

func invokeLifecycle(id, phase string, call func() error) (err error) {
	defer func() {
		if recover() != nil {
			err = fmt.Errorf("%w during component %q %s", ErrComponentPanic, id, phase)
		}
	}()
	return call()
}

func invokeFailureSource(id string, source FailureSource) (failures <-chan error, err error) {
	defer func() {
		if recover() != nil {
			failures = nil
			err = fmt.Errorf("%w in component %q failure source", ErrComponentPanic, id)
		}
	}()
	return source.Errors(), nil
}

func currentReadiness(id string, probe CurrentReadiness) (ready, panicked bool) {
	defer func() {
		if recover() != nil {
			ready = false
			panicked = true
		}
	}()
	return probe.IsReady(), false
}

func isNilManagedComponent(component ManagedComponent) bool {
	return isNilInterface(component)
}

func isNilInterface(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
