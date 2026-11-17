package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"

	appconfig "github.com/Zubrium/cdn/cmd/zubrium/config"
	"github.com/Zubrium/cdn/coordinator"
	"github.com/Zubrium/cdn/core/access"
	"github.com/Zubrium/cdn/core/platform"
	loaderapp "github.com/Zubrium/cdn/engines/loader/features/files/application"
	coordinatoradmin "github.com/Zubrium/cdn/presentation/admin/coordinator"
	"github.com/Zubrium/cdn/presentation/fileapi"
	"github.com/google/uuid"
)

type coreComponent struct {
	config  appconfig.Resolved
	runtime *platform.Runtime
	secret  string
}

func (c *coreComponent) Start(ctx context.Context) error {
	runtime, secret, err := platform.Start(ctx, c.config.Core)
	if err != nil {
		return err
	}
	c.runtime, c.secret = runtime, secret
	if err := registerProfile(runtime.Registry); err != nil {
		_ = runtime.Close()
		return fmt.Errorf("register compiled profile: %w", err)
	}
	if err := runtime.Registry.Register("coordinator", coordinator.Descriptor{}); err != nil {
		_ = runtime.Close()
		return fmt.Errorf("register Coordinator capability: %w", err)
	}
	return nil
}

func (c *coreComponent) Ready(context.Context) error {
	if c == nil || c.runtime == nil {
		return errors.New("Core is not initialized")
	}
	return nil
}

func (c *coreComponent) Stop(context.Context) error {
	if c == nil || c.runtime == nil {
		return nil
	}
	err := c.runtime.Close()
	c.runtime = nil
	return err
}

func (c *coreComponent) IsReady() bool { return c != nil && c.runtime != nil }

type coordinatorComponent struct {
	mu       sync.RWMutex
	core     *coreComponent
	settings appconfig.CoordinatorSettings
	fp       string
	catalog  *coordinator.TaskCatalog
	service  *coordinator.Service
}

func (c *coordinatorComponent) Start(ctx context.Context) error {
	if c.core == nil || c.core.runtime == nil {
		return errors.New("Core is not initialized")
	}
	store, err := c.core.runtime.CoordinatorStore()
	if err != nil {
		return err
	}
	service, err := coordinator.NewService(coordinator.ServiceConfig{
		Store: store, SystemID: c.settings.SystemID, Fingerprint: c.fp,
		InstanceLease: c.settings.InstanceLease, HeartbeatInterval: c.settings.HeartbeatInterval,
		WorkerConcurrency: c.settings.WorkerConcurrency, JobLease: c.settings.JobLease,
		TaskTimeout: c.settings.TaskTimeout, PollInterval: c.settings.PollInterval,
		RetryBase: c.settings.RetryBase, RetryMaximum: c.settings.RetryMaximum,
		MaxAttempts: c.settings.MaxAttempts, Bindings: c.settings.Bindings, Catalog: c.catalog,
	})
	if err != nil {
		return fmt.Errorf("configure Coordinator: %w", err)
	}
	c.mu.Lock()
	if c.service != nil {
		c.mu.Unlock()
		return coordinator.ErrWorkerAlreadyStarted
	}
	c.service = service
	c.mu.Unlock()
	if err := service.Start(ctx); err != nil {
		return fmt.Errorf("start Coordinator service: %w", err)
	}
	return nil
}

// ScheduleAutomaticPublication is the adapter entry point for a publish event
// from the configured Website/Content pair. The route policy and related IDs
// come from the immutable startup binding snapshot.
func (c *coordinatorComponent) ScheduleAutomaticPublication(ctx context.Context, routeID uuid.UUID, input coordinator.PublicationInput) (int64, platform.CoordinatorJob, bool, error) {
	if c == nil {
		return 0, platform.CoordinatorJob{}, false, coordinator.ErrInstanceNotReady
	}
	service := c.currentService()
	if service == nil || !service.Ready() {
		return 0, platform.CoordinatorJob{}, false, coordinator.ErrInstanceNotReady
	}
	return service.ScheduleAutomaticPublication(ctx, routeID, input)
}

// ScheduleConfiguredRebuild is for Coordinator-owned flows that explicitly
// rebuild a configured route even when automatic content publication is off.
// An HTTP operator entry point must authorize before calling this method.
func (c *coordinatorComponent) ScheduleConfiguredRebuild(ctx context.Context, routeID uuid.UUID, input coordinator.PublicationInput) (int64, platform.CoordinatorJob, bool, error) {
	if c == nil {
		return 0, platform.CoordinatorJob{}, false, coordinator.ErrInstanceNotReady
	}
	service := c.currentService()
	if service == nil || !service.Ready() {
		return 0, platform.CoordinatorJob{}, false, coordinator.ErrInstanceNotReady
	}
	return service.ScheduleConfiguredRebuild(ctx, routeID, input)
}

func (c *coordinatorComponent) Activate(ctx context.Context) error {
	service := c.currentService()
	if service == nil {
		return coordinator.ErrInstanceNotReady
	}
	return service.Activate(ctx)
}

// NewJobScheduler issues an explicit task-type subset to one engine adapter.
func (c *coordinatorComponent) NewJobScheduler(jobTypes []string) (*coordinator.JobScheduler, error) {
	service := c.currentService()
	if service == nil {
		return nil, coordinator.ErrInstanceNotReady
	}
	return service.NewJobScheduler(jobTypes)
}

// NewJobControl issues only job list/retry operations to an Admin API adapter.
// That adapter must authenticate and authorize each request before use.
func (c *coordinatorComponent) NewJobControl() (*coordinator.JobControl, error) {
	service := c.currentService()
	if service == nil {
		return nil, coordinator.ErrInstanceNotReady
	}
	return service.NewJobControl()
}

func (c *coordinatorComponent) Ready(ctx context.Context) error {
	if c == nil {
		return coordinator.ErrInstanceNotReady
	}
	service := c.currentService()
	if service == nil {
		return coordinator.ErrInstanceNotReady
	}
	return service.CheckStartup(ctx)
}

func (c *coordinatorComponent) Stop(ctx context.Context) error {
	if c == nil {
		return nil
	}
	service := c.currentService()
	if service == nil {
		return nil
	}
	err := service.Close(ctx)
	c.mu.Lock()
	if c.service == service {
		c.service = nil
	}
	c.mu.Unlock()
	return err
}

func (c *coordinatorComponent) IsReady() bool {
	if c == nil {
		return false
	}
	service := c.currentService()
	return service != nil && service.Ready()
}
func (c *coordinatorComponent) Errors() <-chan error {
	if c == nil {
		ch := make(chan error)
		close(ch)
		return ch
	}
	service := c.currentService()
	if service == nil {
		ch := make(chan error)
		close(ch)
		return ch
	}
	return service.Errors()
}

func (c *coordinatorComponent) currentService() *coordinator.Service {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.service
}

type loaderComponent struct {
	core    *coreComponent
	config  appconfig.Resolved
	service *loaderapp.Service
	cancel  context.CancelFunc
	cleanup <-chan error
}

func (c *loaderComponent) Start(ctx context.Context) error {
	if c.core == nil || c.core.runtime == nil {
		return errors.New("Core is not initialized")
	}
	moduleData, err := c.core.runtime.RegisterDataModule(loaderapp.ModuleDataDescriptor())
	if err != nil {
		return err
	}
	service, err := loaderapp.New(c.core.runtime.LoaderIdentity(), c.core.runtime.LoaderFiles(), moduleData, c.config.Loader)
	if err != nil {
		return err
	}
	cleanupCtx, cancel := context.WithCancel(ctx)
	c.service, c.cancel, c.cleanup = service, cancel, service.RunCleanup(cleanupCtx)
	return nil
}

func (c *loaderComponent) Ready(context.Context) error {
	if c.service == nil {
		return errors.New("Loader is not initialized")
	}
	return nil
}

func (c *loaderComponent) Stop(context.Context) error {
	if c == nil || c.cancel == nil {
		return nil
	}
	c.cancel()
	for range c.cleanup {
	}
	c.service, c.cleanup = nil, nil
	c.cancel = nil
	return nil
}

func (c *loaderComponent) IsReady() bool { return c != nil && c.service != nil }

type fileAPIComponent struct {
	loader               *loaderComponent
	coordinator          *coordinatorComponent
	coordinatorAdmin     *coordinatorAdminComponent
	config               appconfig.Resolved
	server               *http.Server
	listener             net.Listener
	done                 chan struct{}
	failures             chan error
	mu                   sync.RWMutex
	ready                bool
	applicationReadiness interface{ Ready() bool }
}

func (c *fileAPIComponent) Start(context.Context) error {
	if c.loader == nil || c.loader.service == nil {
		return errors.New("Loader is not initialized")
	}
	handler, err := fileapi.NewHandler(c.loader.service, c.config.FileAPI)
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.Handle("/api/v2/files", c.requireCoordinator(handler))
	mux.Handle("/api/v2/files/", c.requireCoordinator(handler))
	if c.coordinatorAdmin != nil {
		mux.Handle("/api/v2/coordinator/jobs", c.requireCoordinator(c.coordinatorAdmin))
		mux.Handle("/api/v2/coordinator/jobs/", c.requireCoordinator(c.coordinatorAdmin))
	}
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		if !c.IsReady() || !c.applicationReady() || c.coordinator == nil || !c.coordinator.IsReady() {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Retry-After", "1")
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNoContent)
	})
	c.server = &http.Server{Handler: mux, ReadHeaderTimeout: c.config.HTTP.ReadHeaderTimeout, ReadTimeout: c.config.HTTP.ReadTimeout,
		WriteTimeout: c.config.HTTP.WriteTimeout, IdleTimeout: c.config.HTTP.IdleTimeout, MaxHeaderBytes: c.config.HTTP.MaxHeaderBytes}
	listener, err := net.Listen("tcp", c.config.HTTP.ListenAddress)
	if err != nil {
		return err
	}
	c.listener = listener
	c.done = make(chan struct{})
	c.failures = make(chan error, 1)
	server := c.server
	c.mu.Lock()
	c.ready = true
	c.mu.Unlock()
	go func() {
		defer close(c.done)
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			c.mu.Lock()
			c.ready = false
			c.mu.Unlock()
			select {
			case c.failures <- errors.New("admin HTTP server stopped unexpectedly"):
			default:
			}
		}
	}()
	return nil
}

type coordinatorAdminComponent struct {
	mu      sync.RWMutex
	ready   bool
	handler http.Handler
}

func (c *coordinatorAdminComponent) Start(context.Context) error {
	if c == nil || c.handler == nil {
		return coordinator.ErrInvalid
	}
	c.mu.Lock()
	c.ready = true
	c.mu.Unlock()
	return nil
}

func (c *coordinatorAdminComponent) Ready(context.Context) error {
	if c == nil || !c.IsReady() {
		return errors.New("Coordinator Admin API is not initialized")
	}
	return nil
}

func (c *coordinatorAdminComponent) Stop(context.Context) error {
	if c != nil {
		c.mu.Lock()
		c.ready = false
		c.mu.Unlock()
	}
	return nil
}

func (c *coordinatorAdminComponent) IsReady() bool {
	if c == nil {
		return false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.ready && c.handler != nil
}

func (c *coordinatorAdminComponent) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !c.IsReady() {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Retry-After", "1")
		http.Error(w, "service temporarily unavailable", http.StatusServiceUnavailable)
		return
	}
	c.handler.ServeHTTP(w, r)
}

type coreCoordinatorGroupAuthorization struct{ core *coreComponent }

func (a coreCoordinatorGroupAuthorization) capability() (platform.CoordinatorAuthorizationCapability, error) {
	if a.core == nil || a.core.runtime == nil {
		return nil, platform.ErrUnavailable
	}
	return a.core.runtime.CoordinatorAuthorization(), nil
}

func (a coreCoordinatorGroupAuthorization) Authenticate(ctx context.Context, credential string) (platform.Session, error) {
	capability, err := a.capability()
	if err != nil {
		return platform.Session{}, err
	}
	return capability.Authenticate(ctx, credential)
}

func (a coreCoordinatorGroupAuthorization) RequireGroupLevel(ctx context.Context, session platform.Session, group string, level access.Level) error {
	capability, err := a.capability()
	if err != nil {
		return err
	}
	return capability.RequireGroupLevel(ctx, session, group, level)
}

type liveCoordinatorJobControl struct{ coordinator *coordinatorComponent }

func (c liveCoordinatorJobControl) List(ctx context.Context, filter coordinator.JobListFilter) (platform.CoordinatorJobPage, error) {
	if c.coordinator == nil {
		return platform.CoordinatorJobPage{}, coordinator.ErrInstanceNotReady
	}
	control, err := c.coordinator.NewJobControl()
	if err != nil {
		return platform.CoordinatorJobPage{}, err
	}
	return control.List(ctx, filter)
}

func (c liveCoordinatorJobControl) Retry(ctx context.Context, id uuid.UUID) error {
	if c.coordinator == nil {
		return coordinator.ErrInstanceNotReady
	}
	control, err := c.coordinator.NewJobControl()
	if err != nil {
		return err
	}
	return control.Retry(ctx, id)
}

func (c *fileAPIComponent) Ready(context.Context) error {
	if !c.IsReady() {
		return errors.New("File API listener is not initialized")
	}
	return nil
}

func (c *fileAPIComponent) Stop(ctx context.Context) error {
	if c == nil || c.server == nil {
		return nil
	}
	c.mu.Lock()
	c.ready = false
	c.mu.Unlock()
	err := c.server.Shutdown(ctx)
	if err != nil {
		_ = c.server.Close()
	}
	select {
	case <-c.done:
	case <-ctx.Done():
		err = errors.Join(err, ctx.Err())
	}
	if closeErr := c.server.Close(); closeErr != nil && !errors.Is(closeErr, http.ErrServerClosed) {
		err = errors.Join(err, closeErr)
	}
	c.server, c.listener = nil, nil
	return err
}

func (c *fileAPIComponent) IsReady() bool {
	if c == nil {
		return false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.ready
}

func (c *fileAPIComponent) requireCoordinator(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c == nil || !c.IsReady() || !c.applicationReady() || c.coordinator == nil || !c.coordinator.IsReady() {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Retry-After", "1")
			http.Error(w, "service temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (c *fileAPIComponent) setApplicationReadiness(readiness interface{ Ready() bool }) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.applicationReadiness = readiness
	c.mu.Unlock()
}

func (c *fileAPIComponent) applicationReady() bool {
	if c == nil {
		return false
	}
	c.mu.RLock()
	readiness := c.applicationReadiness
	c.mu.RUnlock()
	return readiness != nil && readiness.Ready()
}

func (c *fileAPIComponent) Errors() <-chan error {
	if c == nil || c.failures == nil {
		ch := make(chan error)
		close(ch)
		return ch
	}
	return c.failures
}

func registerComponents(resolved appconfig.Resolved, plan coordinator.BuildPlan) (*coordinator.RuntimeRegistry, *coreComponent, *coordinatorComponent, *loaderComponent, *fileAPIComponent, error) {
	registry := coordinator.NewRuntimeRegistry()
	catalog, err := registry.CompileTaskCatalog(plan)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	core := &coreComponent{config: resolved}
	coordinatorPart := &coordinatorComponent{core: core, settings: resolved.Coordinator, fp: resolved.CoordinatorFingerprint, catalog: catalog}
	loader := &loaderComponent{core: core, config: resolved}
	fileAPI := &fileAPIComponent{loader: loader, coordinator: coordinatorPart, config: resolved}
	selected := make(map[string]struct{}, len(plan.Components))
	for _, id := range plan.Components {
		selected[id] = struct{}{}
	}
	if _, enabled := selected["admin.coordinator"]; enabled {
		authorize, authErr := coordinatoradmin.NewGroupAuthorizer(coreCoordinatorGroupAuthorization{core: core}, resolved.Coordinator.JobAccessGroup)
		if authErr != nil {
			return nil, nil, nil, nil, nil, authErr
		}
		handler, handlerErr := coordinatoradmin.NewHandler(liveCoordinatorJobControl{coordinator: coordinatorPart}, authorize, coordinatoradmin.Config{
			DefaultPageSize: resolved.Coordinator.JobsPageDefault,
			MaxPageSize:     resolved.Coordinator.JobsPageMaximum,
		})
		if handlerErr != nil {
			return nil, nil, nil, nil, nil, handlerErr
		}
		fileAPI.coordinatorAdmin = &coordinatorAdminComponent{handler: handler}
	}
	if err := registry.Register(coordinator.RuntimeComponent{ID: "core", New: func(context.Context) (coordinator.ManagedComponent, error) { return core, nil }}); err != nil {
		return nil, nil, nil, nil, nil, err
	}
	if err := registry.Register(coordinator.RuntimeComponent{ID: "coordinator", Requires: []string{"core"}, New: func(context.Context) (coordinator.ManagedComponent, error) { return coordinatorPart, nil }}); err != nil {
		return nil, nil, nil, nil, nil, err
	}
	if err := registry.Register(coordinator.RuntimeComponent{ID: "engine.loader", Requires: []string{"core", "coordinator"}, New: func(context.Context) (coordinator.ManagedComponent, error) { return loader, nil }}); err != nil {
		return nil, nil, nil, nil, nil, err
	}
	if err := registry.Register(coordinator.RuntimeComponent{ID: "admin.files", Requires: []string{"engine.loader", "coordinator"}, New: func(context.Context) (coordinator.ManagedComponent, error) { return fileAPI, nil }}); err != nil {
		return nil, nil, nil, nil, nil, err
	}
	if adminAPI := fileAPI.coordinatorAdmin; adminAPI != nil {
		if err := registry.Register(coordinator.RuntimeComponent{ID: "admin.coordinator", Requires: []string{"core", "coordinator", "admin.files"}, New: func(context.Context) (coordinator.ManagedComponent, error) { return adminAPI, nil }}); err != nil {
			return nil, nil, nil, nil, nil, err
		}
	}
	return registry, core, coordinatorPart, loader, fileAPI, nil
}
