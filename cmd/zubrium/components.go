package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sort"
	"sync"

	appconfig "github.com/Zubrium/cdn/cmd/zubrium/config"
	platform "github.com/Zubrium/cdn/core/presentation"
	loaderapp "github.com/Zubrium/cdn/engines/loader/features/files/application"
	contentadmin "github.com/Zubrium/cdn/presentation/admin/content"
	coordinatoradmin "github.com/Zubrium/cdn/presentation/admin/coordinator"
	formsadmin "github.com/Zubrium/cdn/presentation/admin/forms"
	adminapi "github.com/Zubrium/cdn/presentation/adminapi"
	"github.com/Zubrium/cdn/presentation/fileapi"
	"github.com/Zubrium/cdn/presentation/sitesapi"
	"github.com/google/uuid"
)

type coreComponent struct {
	config  appconfig.Resolved
	runtime *platform.Runtime
	secret  string
	catalog *platform.TaskCatalog
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
	if err := runtime.Registry.Register("coordinator", platform.CoordinatorDescriptor{}); err != nil {
		_ = runtime.Close()
		return fmt.Errorf("register Coordinator capability: %w", err)
	}
	settings := c.config.Coordinator
	if err := runtime.StartCoordinator(ctx, platform.CoordinatorRuntimeConfig{
		Fingerprint:   c.config.CoordinatorFingerprint,
		InstanceLease: settings.InstanceLease, HeartbeatInterval: settings.HeartbeatInterval,
		WorkerConcurrency: settings.WorkerConcurrency, JobLease: settings.JobLease,
		TaskTimeout: settings.TaskTimeout, PollInterval: settings.PollInterval,
		RetryBase: settings.RetryBase, RetryMaximum: settings.RetryMaximum,
		MaxAttempts: settings.MaxAttempts, MaxWorkflowTasks: settings.MaxWorkflowTasks,
		MaxChildrenPerTask: settings.MaxChildrenPerTask, WorkflowRetention: settings.WorkflowRetention,
		ShutdownTimeout: c.config.HTTP.ShutdownTimeout, TaskCatalog: c.catalog,
	}); err != nil {
		_ = runtime.Close()
		return fmt.Errorf("start Core Coordinator: %w", err)
	}
	return nil
}

func (c *coreComponent) Ready(ctx context.Context) error {
	if c == nil || c.runtime == nil {
		return errors.New("Core is not initialized")
	}
	if err := c.runtime.CoordinatorStartupReady(ctx); err != nil {
		return fmt.Errorf("Core Coordinator startup is not ready: %w", err)
	}
	return nil
}

func (c *coreComponent) ActivateAfterComponents(ctx context.Context) error {
	if c == nil || c.runtime == nil {
		return platform.ErrCoordinatorNotReady
	}
	return c.runtime.ActivateCoordinator(ctx)
}

func (c *coreComponent) QuiesceBeforeComponents(ctx context.Context) error {
	if c == nil || c.runtime == nil {
		return nil
	}
	return c.runtime.StopCoordinator(ctx)
}

func (c *coreComponent) Stop(ctx context.Context) error {
	if c == nil || c.runtime == nil {
		return nil
	}
	err := c.runtime.CloseContext(ctx)
	if err == nil {
		c.runtime = nil
	}
	return err
}

func (c *coreComponent) IsReady() bool {
	return c != nil && c.runtime != nil && c.runtime.CoordinatorReady()
}

func (c *coreComponent) Errors() <-chan error {
	if c == nil || c.runtime == nil {
		closed := make(chan error)
		close(closed)
		return closed
	}
	return c.runtime.CoordinatorErrors()
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
	service, err := loaderapp.New(c.core.runtime.GroupAuthorization(), c.core.runtime.StagedFiles(), moduleData, c.config.Loader, c.core.runtime.Sessions())
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
	core                 *coreComponent
	coordinatorAdmin     *coordinatorAdminComponent
	identityAdmin        *identityAdminComponent
	developmentFixtures  bool
	routerAdminEnabled   bool
	routerAdminHandler   http.Handler
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
	var fixtureAPIs *developmentFixtureAPIs
	if c.developmentFixtures {
		if !compiledDevelopmentFixtures() || c.core == nil || c.core.runtime == nil {
			return errors.New("development-fixture APIs are not available in this build")
		}
		var err error
		fixtureAPIs, err = createDevelopmentFixtures(c.core.runtime, c.config)
		if err != nil {
			return fmt.Errorf("initialize read-only development fixtures: %w", err)
		}
	}
	fileAPIConfig := c.config.FileAPI
	fileAPIConfig.AllowedOrigins = append([]string(nil), c.config.AdminAPI.AllowedOrigins...)
	handler, err := fileapi.NewHandler(c.loader.service, fileAPIConfig)
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.Handle("/api/v2/files", c.requireCoreRuntime(handler))
	mux.Handle("/api/v2/files/", c.requireCoreRuntime(handler))
	if c.coordinatorAdmin != nil {
		mux.Handle("/api/v2/coordinator/jobs", c.requireCoreRuntime(c.coordinatorAdmin))
		mux.Handle("/api/v2/coordinator/jobs/", c.requireCoreRuntime(c.coordinatorAdmin))
	}
	if c.identityAdmin != nil {
		coreAdapter, adapterErr := adminapi.NewCoreAdapter(c.core.runtime.IdentityAdmin(), c.core.runtime.Sessions())
		if adapterErr != nil {
			return adapterErr
		}
		handler, handlerErr := adminapi.NewHandlerWithSessions(coreAdapter, coreAdapter, adminapi.Config{
			MaxJSONBytes: c.config.AdminAPI.MaxJSONBytes, SessionCookieSecure: c.config.AdminAPI.SessionCookieSecure,
			AllowedOrigins: c.config.AdminAPI.AllowedOrigins,
			GroupAccess:    newSessionGroupAccessResolver(c.core.runtime, configuredGroupNames(c.config.Core.AccessGroups)),
		})
		if handlerErr != nil {
			return handlerErr
		}
		c.identityAdmin.setHandler(handler)
		sessionHandler := c.requireCoreRuntime(c.identityAdmin)
		if c.developmentFixtures {
			sessionHandler = syntheticDataMode(sessionHandler)
		}
		mux.Handle("/api/v2/admin/session", sessionHandler)
		mux.Handle("/api/v2/admin/", c.requireCoreRuntime(c.identityAdmin))
	}
	if fixtureAPIs != nil {
		websiteHandler, handlerErr := sitesapi.NewHandler(fixtureAPIs.website, sitesapi.Config{
			MaxJSONBytes: c.config.AdminAPI.MaxJSONBytes, DefaultPageSize: c.config.AdminAPI.DefaultPageSize,
			MaxPageSize: c.config.AdminAPI.MaxPageSize, AllowedOrigins: append([]string(nil), c.config.AdminAPI.AllowedOrigins...),
		})
		if handlerErr != nil {
			return fmt.Errorf("initialize synthetic Website API: %w", handlerErr)
		}
		contentHandler, handlerErr := contentadmin.NewHandler(fixtureAPIs.content, contentadmin.Config{
			MaxJSONBytes: c.config.AdminAPI.MaxJSONBytes, DefaultPageSize: c.config.AdminAPI.DefaultPageSize,
			MaxPageSize: c.config.AdminAPI.MaxPageSize, AllowedOrigins: append([]string(nil), c.config.AdminAPI.AllowedOrigins...),
		})
		if handlerErr != nil {
			return fmt.Errorf("initialize synthetic Content API: %w", handlerErr)
		}
		formsHandler, handlerErr := formsadmin.NewHandler(fixtureAPIs.forms, formsadmin.Config{
			MaxJSONBytes: c.config.AdminAPI.MaxJSONBytes, DefaultPageSize: c.config.AdminAPI.DefaultPageSize,
			MaxPageSize: c.config.AdminAPI.MaxPageSize, AllowedOrigins: append([]string(nil), c.config.AdminAPI.AllowedOrigins...),
		})
		if handlerErr != nil {
			return fmt.Errorf("initialize synthetic Forms API: %w", handlerErr)
		}
		mux.Handle("/api/v2/admin/sites/", c.requireCoreRuntime(syntheticDataMode(websiteHandler)))
		mux.Handle("/api/v2/admin/content/", c.requireCoreRuntime(syntheticDataMode(contentHandler)))
		mux.Handle("/api/v2/admin/forms/", c.requireCoreRuntime(syntheticDataMode(formsHandler)))
	}
	if c.routerAdminEnabled {
		routerAdmin := c.requireCoreRuntime(http.HandlerFunc(c.serveRouterAdmin))
		mux.Handle("/api/v2/admin/router", routerAdmin)
		mux.Handle("/api/v2/admin/router/", routerAdmin)
	}
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		if !c.IsReady() || !c.applicationReady() || c.core == nil || c.core.runtime == nil || !c.core.runtime.CoordinatorReady() {
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
		return platform.ErrCoordinatorInputInvalid
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

type identityAdminComponent struct {
	mu      sync.RWMutex
	ready   bool
	handler http.Handler
}

func (c *identityAdminComponent) setHandler(handler http.Handler) {
	c.mu.Lock()
	c.handler = handler
	c.mu.Unlock()
}

func (c *identityAdminComponent) Start(context.Context) error {
	if c == nil {
		return errors.New("Admin Identity API is not configured")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.handler == nil {
		return errors.New("Admin Identity API handler is not initialized")
	}
	c.ready = true
	return nil
}

func (c *identityAdminComponent) Ready(context.Context) error {
	if c == nil || !c.IsReady() {
		return errors.New("Admin Identity API is not initialized")
	}
	return nil
}

func (c *identityAdminComponent) Stop(context.Context) error {
	if c != nil {
		c.mu.Lock()
		c.ready = false
		c.mu.Unlock()
	}
	return nil
}

func (c *identityAdminComponent) IsReady() bool {
	if c == nil {
		return false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.ready && c.handler != nil
}

func (c *identityAdminComponent) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !c.IsReady() {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Retry-After", "1")
		http.Error(w, "service temporarily unavailable", http.StatusServiceUnavailable)
		return
	}
	c.mu.RLock()
	handler := c.handler
	c.mu.RUnlock()
	handler.ServeHTTP(w, r)
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

func (a coreCoordinatorGroupAuthorization) RequireGroupLevel(ctx context.Context, session platform.Session, group string, level platform.AccessLevel) error {
	capability, err := a.capability()
	if err != nil {
		return err
	}
	return capability.RequireGroupLevel(ctx, session, group, level)
}

type liveCoordinatorJobControl struct{ core *coreComponent }

func (c liveCoordinatorJobControl) List(ctx context.Context, filter platform.JobListFilter) (platform.CoordinatorJobPage, error) {
	if c.core == nil || c.core.runtime == nil {
		return platform.CoordinatorJobPage{}, platform.ErrCoordinatorNotReady
	}
	control, err := c.core.runtime.NewJobControl()
	if err != nil {
		return platform.CoordinatorJobPage{}, err
	}
	return control.List(ctx, filter)
}

func (c liveCoordinatorJobControl) Retry(ctx context.Context, id uuid.UUID) error {
	if c.core == nil || c.core.runtime == nil {
		return platform.ErrCoordinatorNotReady
	}
	control, err := c.core.runtime.NewJobControl()
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

func syntheticDataMode(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Zubrium-Data-Mode", "synthetic")
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
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

func (c *fileAPIComponent) requireCoreRuntime(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c == nil || !c.IsReady() || !c.applicationReady() || c.core == nil || c.core.runtime == nil || !c.core.runtime.CoordinatorReady() {
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

func (c *fileAPIComponent) setRouterAdminHandler(handler http.Handler) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.routerAdminHandler = handler
	c.mu.Unlock()
}

func (c *fileAPIComponent) serveRouterAdmin(w http.ResponseWriter, r *http.Request) {
	if c == nil {
		http.Error(w, "service temporarily unavailable", http.StatusServiceUnavailable)
		return
	}
	c.mu.RLock()
	handler := c.routerAdminHandler
	c.mu.RUnlock()
	if handler == nil {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Retry-After", "1")
		http.Error(w, "service temporarily unavailable", http.StatusServiceUnavailable)
		return
	}
	handler.ServeHTTP(w, r)
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

func registerComponents(resolved appconfig.Resolved, plan platform.BuildPlan) (*platform.RuntimeRegistry, *coreComponent, *loaderComponent, *fileAPIComponent, error) {
	registry := platform.NewRuntimeRegistry()
	catalog, err := registry.CompileTaskCatalog(plan)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	selected := make(map[string]struct{}, len(plan.Components))
	for _, id := range plan.Components {
		selected[id] = struct{}{}
	}
	if err := validateRunnableComposition(selected); err != nil {
		return nil, nil, nil, nil, err
	}
	core := &coreComponent{config: resolved, catalog: catalog}
	loader := &loaderComponent{core: core, config: resolved}
	fileAPI := &fileAPIComponent{loader: loader, core: core, config: resolved}
	_, fileAPI.developmentFixtures = selected["development.fixtures"]
	if fileAPI.developmentFixtures && !compiledDevelopmentFixtures() {
		return nil, nil, nil, nil, errors.New("development fixtures were selected without the dedicated development build")
	}
	if _, enabled := selected["admin.router"]; enabled {
		fileAPI.routerAdminEnabled = true
	}
	if _, enabled := selected["admin.coordinator"]; enabled {
		authorize, authErr := coordinatoradmin.NewGroupAuthorizer(coreCoordinatorGroupAuthorization{core: core}, resolved.Coordinator.JobAccessGroup)
		if authErr != nil {
			return nil, nil, nil, nil, authErr
		}
		handler, handlerErr := coordinatoradmin.NewHandler(liveCoordinatorJobControl{core: core}, authorize, coordinatoradmin.Config{
			DefaultPageSize:  resolved.Coordinator.JobsPageDefault,
			MaxPageSize:      resolved.Coordinator.JobsPageMaximum,
			AllowedOrigins:   append([]string(nil), resolved.AdminAPI.AllowedOrigins...),
			SessionAuthorize: coordinatorSessionAuthorizer(core, resolved.Coordinator.JobAccessGroup),
		})
		if handlerErr != nil {
			return nil, nil, nil, nil, handlerErr
		}
		fileAPI.coordinatorAdmin = &coordinatorAdminComponent{handler: handler}
	}
	if _, enabled := selected["admin.identity"]; enabled {
		fileAPI.identityAdmin = &identityAdminComponent{}
	}
	if err := registry.Register(platform.RuntimeComponent{ID: "core", New: func(context.Context) (platform.ManagedComponent, error) { return core, nil }}); err != nil {
		return nil, nil, nil, nil, err
	}
	if err := registry.Register(platform.RuntimeComponent{ID: "engine.loader", Requires: []string{"core"}, New: func(context.Context) (platform.ManagedComponent, error) { return loader, nil }}); err != nil {
		return nil, nil, nil, nil, err
	}
	if fileAPI.developmentFixtures {
		for _, id := range []string{"development.fixtures", "engine.website", "engine.content", "engine.forms"} {
			id := id
			requires := []string{"core"}
			component := &readyComponent{id: id}
			if err := registry.Register(platform.RuntimeComponent{ID: id, Requires: requires, New: func(context.Context) (platform.ManagedComponent, error) { return component, nil }}); err != nil {
				return nil, nil, nil, nil, err
			}
		}
	}
	if err := registry.Register(platform.RuntimeComponent{ID: "admin.files", Requires: []string{"core", "engine.loader"}, New: func(context.Context) (platform.ManagedComponent, error) { return fileAPI, nil }}); err != nil {
		return nil, nil, nil, nil, err
	}
	if adminAPI := fileAPI.coordinatorAdmin; adminAPI != nil {
		if err := registry.Register(platform.RuntimeComponent{ID: "admin.coordinator", Requires: []string{"core", "admin.files"}, New: func(context.Context) (platform.ManagedComponent, error) { return adminAPI, nil }}); err != nil {
			return nil, nil, nil, nil, err
		}
	}
	if adminAPI := fileAPI.identityAdmin; adminAPI != nil {
		if err := registry.Register(platform.RuntimeComponent{ID: "admin.identity", Requires: []string{"core", "admin.files"}, New: func(context.Context) (platform.ManagedComponent, error) { return adminAPI, nil }}); err != nil {
			return nil, nil, nil, nil, err
		}
	}
	if fileAPI.developmentFixtures {
		for _, item := range []struct {
			id       string
			engineID string
		}{
			{id: "admin.website", engineID: "engine.website"},
			{id: "admin.content", engineID: "engine.content"},
			{id: "admin.forms", engineID: "engine.forms"},
		} {
			component := &readyComponent{id: item.id}
			if err := registry.Register(platform.RuntimeComponent{ID: item.id, Requires: []string{"core", "admin.files", item.engineID}, New: func(context.Context) (platform.ManagedComponent, error) { return component, nil }}); err != nil {
				return nil, nil, nil, nil, err
			}
		}
	}
	var routerEngine *routerEngineComponent
	if _, enabled := selected["engine.router"]; enabled {
		routerEngine = &routerEngineComponent{core: core, loader: loader, config: resolved, selected: selected}
		if err := registry.Register(platform.RuntimeComponent{ID: "engine.router", Requires: []string{"core", "engine.loader"}, New: func(context.Context) (platform.ManagedComponent, error) { return routerEngine, nil }}); err != nil {
			return nil, nil, nil, nil, err
		}
	}
	if _, enabled := selected["admin.router"]; enabled {
		if routerEngine == nil {
			return nil, nil, nil, nil, platform.ErrComponentUnavailable
		}
		adminAPI := &routerAdminComponent{fileAPI: fileAPI, engine: routerEngine, config: resolved}
		if err := registry.Register(platform.RuntimeComponent{ID: "admin.router", Requires: []string{"core", "engine.loader", "admin.files", "engine.router"}, New: func(context.Context) (platform.ManagedComponent, error) { return adminAPI, nil }}); err != nil {
			return nil, nil, nil, nil, err
		}
	}
	if _, enabled := selected["public.router"]; enabled {
		if routerEngine == nil {
			return nil, nil, nil, nil, platform.ErrComponentUnavailable
		}
		publicAPI := &publicRouterComponent{core: core, engine: routerEngine, config: resolved}
		if err := registry.Register(platform.RuntimeComponent{ID: "public.router", Requires: []string{"engine.router"}, New: func(context.Context) (platform.ManagedComponent, error) { return publicAPI, nil }}); err != nil {
			return nil, nil, nil, nil, err
		}
	}
	if _, enabled := selected["public.form-upload"]; enabled {
		if routerEngine == nil {
			return nil, nil, nil, nil, platform.ErrComponentUnavailable
		}
		uploadAPI := &publicUploadComponent{engine: routerEngine}
		if err := registry.Register(platform.RuntimeComponent{ID: "public.form-upload", Requires: []string{"engine.router", "public.router"}, New: func(context.Context) (platform.ManagedComponent, error) { return uploadAPI, nil }}); err != nil {
			return nil, nil, nil, nil, err
		}
	}
	return registry, core, loader, fileAPI, nil
}

func validateRunnableComposition(selected map[string]struct{}) error {
	for _, component := range []string{"core", "engine.loader", "admin.files"} {
		if _, ok := selected[component]; !ok {
			return fmt.Errorf("selected build profile requires %s for the current command", component)
		}
	}
	available := map[string]struct{}{
		"core": {}, "engine.loader": {}, "admin.files": {}, "admin.coordinator": {}, "admin.identity": {},
		"engine.router": {}, "admin.router": {}, "public.router": {}, "public.form-upload": {},
	}
	if _, fixtures := selected["development.fixtures"]; fixtures {
		for _, component := range []string{"development.fixtures", "engine.website", "engine.content", "engine.forms", "admin.website", "admin.content", "admin.forms"} {
			available[component] = struct{}{}
		}
	}
	for component := range selected {
		if _, ok := available[component]; !ok {
			return fmt.Errorf("selected component %q has no runtime implementation in this composition", component)
		}
	}
	return nil
}

type readyComponent struct {
	id    string
	ready bool
}

func (component *readyComponent) Start(context.Context) error {
	if component == nil || component.id == "" {
		return platform.ErrComponentUnavailable
	}
	component.ready = true
	return nil
}
func (component *readyComponent) Ready(context.Context) error {
	if component == nil || !component.ready {
		return platform.ErrComponentUnavailable
	}
	return nil
}
func (component *readyComponent) Stop(context.Context) error {
	if component != nil {
		component.ready = false
	}
	return nil
}

type sessionGroupAccessCore interface {
	Sessions() platform.SessionCapability
	GroupAuthorization() platform.GroupAuthorizationCapability
}

type sessionGroupAccessResolver struct {
	core   sessionGroupAccessCore
	groups []string
}

func newSessionGroupAccessResolver(core sessionGroupAccessCore, groups []string) adminapi.SessionGroupAccessResolver {
	return &sessionGroupAccessResolver{core: core, groups: append([]string(nil), groups...)}
}

func (r *sessionGroupAccessResolver) ResolveGroupAccess(ctx context.Context, token string) (map[string]platform.AccessLevel, error) {
	if r == nil || r.core == nil || token == "" {
		return nil, platform.ErrUnavailable
	}
	session, _, err := r.core.Sessions().AuthenticateSession(ctx, token)
	if err != nil {
		return nil, err
	}
	access := make(map[string]platform.AccessLevel)
	for _, group := range r.groups {
		err := r.core.GroupAuthorization().RequireGroupLevel(ctx, session, []string{group}, platform.AccessLevelWrite)
		if err == nil {
			access[group] = platform.AccessLevelWrite
			continue
		}
		if !errors.Is(err, platform.ErrForbidden) {
			return nil, err
		}
		err = r.core.GroupAuthorization().RequireGroupLevel(ctx, session, []string{group}, platform.AccessLevelRead)
		if err == nil {
			access[group] = platform.AccessLevelRead
			continue
		}
		if !errors.Is(err, platform.ErrForbidden) {
			return nil, err
		}
	}
	return access, nil
}

func configuredGroupNames(groups map[string]platform.AccessLevel) []string {
	names := make([]string, 0, len(groups))
	for group := range groups {
		names = append(names, group)
	}
	sort.Strings(names)
	return names
}

func coordinatorSessionAuthorizer(core *coreComponent, group string) coordinatoradmin.AuthorizeFunc {
	return func(ctx context.Context, token string, permission coordinatoradmin.Permission) error {
		if core == nil || core.runtime == nil {
			return platform.ErrUnavailable
		}
		session, _, err := core.runtime.Sessions().AuthenticateSession(ctx, token)
		if err != nil {
			return err
		}
		level := platform.AccessLevelRead
		if permission == coordinatoradmin.RetryJobs {
			level = platform.AccessLevelWrite
		} else if permission != coordinatoradmin.ReadJobs {
			return platform.ErrForbidden
		}
		return core.runtime.CoordinatorAuthorization().RequireGroupLevel(ctx, session, group, level)
	}
}
