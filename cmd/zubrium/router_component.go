package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"

	appconfig "github.com/Zubrium/cdn/cmd/zubrium/config"
	platform "github.com/Zubrium/cdn/core/presentation"
	routerengine "github.com/Zubrium/cdn/engines/router"
	routesapp "github.com/Zubrium/cdn/engines/router/features/routes/application"
	routesrepo "github.com/Zubrium/cdn/engines/router/features/routes/repository"
	routerenginepresentation "github.com/Zubrium/cdn/engines/router/presentation"
	adminrouter "github.com/Zubrium/cdn/presentation/admin/router"
	routerhttp "github.com/Zubrium/cdn/presentation/router"
	"github.com/Zubrium/cdn/presentation/router/publicforms"
	"github.com/Zubrium/cdn/presentation/router/publicupload"
)

type routerEngineComponent struct {
	core     *coreComponent
	loader   *loaderComponent
	forms    *formsEngineComponent
	config   appconfig.Resolved
	selected map[string]struct{}
	api      *routerenginepresentation.APIAdapter
	registry *routerhttp.PublicOperationRegistry
}

func (component *routerEngineComponent) Start(ctx context.Context) error {
	if component == nil || component.core == nil || component.core.runtime == nil || component.loader == nil || component.loader.service == nil {
		return errors.New("Router dependencies are not initialized")
	}
	settings := component.config.RouterAPI
	operations := make([]routerenginepresentation.PublicOperation, 0, 3)
	var formsAPI publicforms.VisitorAPI
	if component.forms != nil {
		if component.forms.engine == nil {
			return errors.New("Forms Engine is not initialized")
		}
		formsAPI = component.forms.engine
	}
	if _, enabled := component.selected["public.form-session"]; enabled {
		session, err := publicforms.NewSessionOperation(formsAPI)
		if err != nil {
			return fmt.Errorf("configure public Forms session operation: %w", err)
		}
		operations = append(operations, session)
	}
	if _, enabled := component.selected["public.form-submit"]; enabled {
		submit, err := publicforms.NewSubmitOperation(formsAPI, settings.MaxJSONBytes)
		if err != nil {
			return fmt.Errorf("configure public Forms submit operation: %w", err)
		}
		operations = append(operations, submit)
	}
	if _, enabled := component.selected["public.form-upload"]; enabled {
		upload, err := publicupload.New(component.loader.service, publicupload.Config{
			Credential: settings.ServiceCredential, StagingDirectory: settings.UploadStagingDirectory,
			MaxFileBytes: settings.UploadMaxFileBytes, PartSize: component.config.Loader.PartSize,
			MaxParts: component.config.Loader.MaxParts, MaxConcurrentUploads: settings.UploadMaxConcurrent,
			MaxStagedBytes: settings.UploadMaxStagedBytes, AllowedContentTypes: append([]string(nil), settings.UploadAllowedContentTypes...),
			GroupAuthorization: component.core.runtime.GroupAuthorization(),
			Forms: formsAPI,
		})
		if err != nil {
			return fmt.Errorf("configure Router public upload: %w", err)
		}
		operations = append(operations, upload)
	}
	registry, err := routerhttp.NewPublicOperationRegistry(operations...)
	if err != nil {
		return err
	}
	moduleData, err := component.core.runtime.RegisterDataModule(routerengine.ModuleDataDescriptor())
	if err != nil {
		return fmt.Errorf("register Router Core module data: %w", err)
	}
	repository, err := routesrepo.NewModuleDataRepository(moduleData)
	if err != nil {
		return err
	}
	identity := routerIdentityAdapter{identity: component.core.runtime.EngineIdentity(), sessions: component.core.runtime.Sessions()}
	validator, err := routerhttp.NewCoreTargetValidator(component.core.runtime.EngineIdentity(), component.core.runtime.EngineFiles(), settings.ServiceCredential, registry)
	if err != nil {
		return err
	}
	routeService, err := routesapp.New(repository, identity, component.core.runtime.GroupAuthorization(), validator, routesapp.Config{
		BranchID: settings.BranchID, AccessGroup: "router_routes", DefaultPageSize: settings.DefaultPageSize, MaxPageSize: settings.MaxPageSize,
	})
	if err != nil {
		return err
	}
	delivery, err := routerhttp.NewCoreDelivery(settings.BranchID, settings.ServiceCredential, component.core.runtime.EngineIdentity(),
		component.core.runtime.EngineFiles(), routeService, registry)
	if err != nil {
		return err
	}
	api, err := routerenginepresentation.NewAPIAdapter(routeService, delivery, settings.BranchID)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	component.registry, component.api = registry, api
	return nil
}

func (component *routerEngineComponent) Ready(context.Context) error {
	if component == nil || component.api == nil || component.registry == nil {
		return errors.New("Router Engine is not initialized")
	}
	return nil
}

func (component *routerEngineComponent) Stop(context.Context) error {
	if component != nil {
		component.api = nil
		component.registry = nil
	}
	return nil
}

func (component *routerEngineComponent) IsReady() bool {
	return component != nil && component.api != nil && component.registry != nil
}

type routerIdentityAdapter struct {
	identity platform.EngineIdentityCapability
	sessions platform.SessionCapability
}

func (adapter routerIdentityAdapter) Authenticate(ctx context.Context, credential string) (platform.Session, error) {
	if adapter.identity == nil {
		return platform.Session{}, platform.ErrUnavailable
	}
	session, _, err := adapter.identity.Authenticate(ctx, credential)
	return session, err
}

func (adapter routerIdentityAdapter) AuthenticateSession(ctx context.Context, token string) (platform.Session, platform.Principal, error) {
	if adapter.sessions == nil {
		return platform.Session{}, platform.Principal{}, platform.ErrUnavailable
	}
	return adapter.sessions.AuthenticateSession(ctx, token)
}

type routerAdminComponent struct {
	fileAPI *fileAPIComponent
	engine  *routerEngineComponent
	config  appconfig.Resolved
	handler http.Handler
}

func (component *routerAdminComponent) Start(context.Context) error {
	if component == nil || component.fileAPI == nil || component.engine == nil || component.engine.api == nil {
		return errors.New("Router Admin dependencies are not initialized")
	}
	settings := component.config.RouterAPI
	handler, err := adminrouter.NewHandler(component.engine.api, routerhttp.Config{
		BranchID: settings.BranchID, MaxJSONBytes: settings.MaxJSONBytes, DefaultPageSize: settings.DefaultPageSize,
		MaxPageSize: settings.MaxPageSize, AllowedOrigins: append([]string(nil), component.config.AdminAPI.AllowedOrigins...),
		PublicOperations: component.engine.registry, FormSessionCookieSecure: settings.FormSessionCookieSecure,
	})
	if err != nil {
		return err
	}
	component.fileAPI.setRouterAdminHandler(handler)
	component.handler = handler
	return nil
}

func (component *routerAdminComponent) Ready(context.Context) error {
	if component == nil || component.handler == nil {
		return errors.New("Router Admin handler is not initialized")
	}
	return nil
}

func (*routerAdminComponent) Stop(context.Context) error { return nil }

type publicRouterComponent struct {
	core     *coreComponent
	engine   *routerEngineComponent
	config   appconfig.Resolved
	mu       sync.RWMutex
	server   *http.Server
	listener net.Listener
	done     chan struct{}
	failures chan error
	ready    bool
}

func (component *publicRouterComponent) Start(context.Context) error {
	if component == nil || component.core == nil || component.engine == nil || component.engine.api == nil {
		return errors.New("public Router dependencies are not initialized")
	}
	settings := component.config.RouterAPI
	combined, err := routerhttp.NewHandler(component.engine.api, routerhttp.Config{
		BranchID: settings.BranchID, MaxJSONBytes: settings.MaxJSONBytes, DefaultPageSize: settings.DefaultPageSize,
		MaxPageSize: settings.MaxPageSize, AllowedOrigins: append([]string(nil), component.config.AdminAPI.AllowedOrigins...),
		PublicOperations: component.engine.registry, FormSessionCookieSecure: settings.FormSessionCookieSecure,
	})
	if err != nil {
		return err
	}
	publicHandler := combined.PublicHTTPHandler()
	guarded := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if component.core == nil || component.core.runtime == nil || !component.core.runtime.CoordinatorReady() {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Retry-After", "1")
			http.Error(w, "service temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		publicHandler.ServeHTTP(w, r)
	})
	server := &http.Server{Handler: guarded, ReadHeaderTimeout: component.config.HTTP.ReadHeaderTimeout,
		ReadTimeout: component.config.HTTP.ReadTimeout, WriteTimeout: component.config.HTTP.WriteTimeout,
		IdleTimeout: component.config.HTTP.IdleTimeout, MaxHeaderBytes: component.config.HTTP.MaxHeaderBytes}
	listener, err := net.Listen("tcp", settings.PublicListenAddress)
	if err != nil {
		return fmt.Errorf("listen on internal Router address: %w", err)
	}
	component.mu.Lock()
	component.server, component.listener = server, listener
	component.done, component.failures = make(chan struct{}), make(chan error, 1)
	component.ready = true
	component.mu.Unlock()
	go func() {
		defer close(component.done)
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			component.mu.Lock()
			component.ready = false
			component.mu.Unlock()
			select {
			case component.failures <- errors.New("public Router listener stopped unexpectedly"):
			default:
			}
		}
	}()
	return nil
}

func (component *publicRouterComponent) Ready(context.Context) error {
	if !component.IsReady() {
		return errors.New("public Router listener is not initialized")
	}
	return nil
}

func (component *publicRouterComponent) Stop(ctx context.Context) error {
	if component == nil {
		return nil
	}
	component.mu.Lock()
	server, done := component.server, component.done
	component.ready = false
	component.mu.Unlock()
	if server == nil {
		return nil
	}
	err := server.Shutdown(ctx)
	if err != nil {
		_ = server.Close()
	}
	select {
	case <-done:
	case <-ctx.Done():
		err = errors.Join(err, ctx.Err())
	}
	if closeErr := server.Close(); closeErr != nil && !errors.Is(closeErr, http.ErrServerClosed) {
		err = errors.Join(err, closeErr)
	}
	component.mu.Lock()
	component.server, component.listener, component.done = nil, nil, nil
	component.mu.Unlock()
	return err
}

func (component *publicRouterComponent) IsReady() bool {
	if component == nil {
		return false
	}
	component.mu.RLock()
	defer component.mu.RUnlock()
	return component.ready && component.listener != nil
}

func (component *publicRouterComponent) Errors() <-chan error {
	if component == nil || component.failures == nil {
		closed := make(chan error)
		close(closed)
		return closed
	}
	return component.failures
}

type publicUploadComponent struct {
	engine *routerEngineComponent
	ready  bool
}

func (component *publicUploadComponent) Start(ctx context.Context) error {
	if component == nil || component.engine == nil || component.engine.registry == nil {
		return errors.New("public upload operation is unavailable")
	}
	if _, err := component.engine.registry.ResolvePublicOperation(ctx, publicupload.OperationID); err != nil {
		return err
	}
	component.ready = true
	return nil
}

func (component *publicUploadComponent) Ready(context.Context) error {
	if component == nil || !component.ready {
		return errors.New("public upload operation is not initialized")
	}
	return nil
}

func (component *publicUploadComponent) Stop(context.Context) error {
	if component != nil {
		component.ready = false
	}
	return nil
}
