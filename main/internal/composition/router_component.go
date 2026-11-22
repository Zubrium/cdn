package composition

import (
	"context"
	"errors"
	"fmt"

	platform "github.com/Zubrium/cdn/core/presentation"
	routerengine "github.com/Zubrium/cdn/engines/router"
	routesapp "github.com/Zubrium/cdn/engines/router/features/routes/application"
	routesrepo "github.com/Zubrium/cdn/engines/router/features/routes/repository"
	routerenginepresentation "github.com/Zubrium/cdn/engines/router/presentation"
	appconfig "github.com/Zubrium/cdn/main/internal/config"
	"github.com/Zubrium/cdn/pkg/contracts"
	"github.com/Zubrium/cdn/presentation/admin/base/transport"
	"github.com/Zubrium/cdn/presentation/router"
	"github.com/Zubrium/cdn/presentation/router/publiccontentruntime"
	"github.com/Zubrium/cdn/presentation/router/publicforms"
	"github.com/Zubrium/cdn/presentation/router/publicupload"
)

type routerEngineComponent struct {
	core       *coreComponent
	loader     *loaderComponent
	forms      *formsEngineComponent
	content    *contentEngineComponent
	config     appconfig.Resolved
	selected   map[string]struct{}
	dispatcher contracts.Dispatcher
	api        *routerenginepresentation.APIAdapter
	registry   *router.PublicOperationRegistry
}

func (component *routerEngineComponent) Start(ctx context.Context) error {
	if component == nil || component.core == nil || component.core.runtime == nil || component.loader == nil || component.loader.service == nil {
		return errors.New("Router dependencies are not initialized")
	}
	settings := component.config.RouterAPI
	operations := make([]routerenginepresentation.PublicOperation, 0, 4)
	var formsAPI publicforms.VisitorAPI
	if component.forms != nil {
		if component.forms.engine == nil {
			return errors.New("Forms Engine is not initialized")
		}
		formsAPI = component.forms.engine
	}
	if _, enabled := component.selected["public.content-runtime"]; enabled {
		if component.content == nil || component.content.engine == nil || component.dispatcher == nil {
			return errors.New("Content runtime bus is not initialized")
		}
		content, err := publiccontentruntime.New(busPublishedContentReader{dispatcher: component.dispatcher})
		if err != nil {
			return fmt.Errorf("configure public Content runtime operation: %w", err)
		}
		operations = append(operations, content)
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
			Forms:              formsAPI,
		})
		if err != nil {
			return fmt.Errorf("configure Router public upload: %w", err)
		}
		operations = append(operations, upload)
	}
	registry, err := router.NewPublicOperationRegistry(operations...)
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
	validator, err := router.NewCoreTargetValidator(component.core.runtime.EngineIdentity(), component.core.runtime.EngineFiles(), settings.ServiceCredential, registry)
	if err != nil {
		return err
	}
	routeService, err := routesapp.New(repository, identity, component.core.runtime.GroupAuthorization(), validator, routesapp.Config{
		BranchID: settings.BranchID, AccessGroup: "router_routes", DefaultPageSize: settings.DefaultPageSize, MaxPageSize: settings.MaxPageSize,
	})
	if err != nil {
		return err
	}
	delivery, err := router.NewCoreDelivery(settings.BranchID, settings.ServiceCredential, component.core.runtime.EngineIdentity(),
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

type routerAdminComponent struct{}

func (*routerAdminComponent) Start(context.Context) error { return nil }
func (*routerAdminComponent) Ready(context.Context) error { return nil }
func (*routerAdminComponent) Stop(context.Context) error  { return nil }

type publicRouterComponent struct {
	core   *coreComponent
	engine *routerEngineComponent
	config appconfig.Resolved
	server *transport.PublicServer
}

func (component *publicRouterComponent) Start(ctx context.Context) error {
	if component == nil || component.core == nil || component.core.runtime == nil || component.engine == nil || component.engine.api == nil {
		return errors.New("public Router dependencies are not initialized")
	}
	settings := component.config.RouterAPI
	server, err := transport.NewPublicServer(transport.PublicConfig{
		Server: transport.ServerConfig{
			Address: settings.PublicListenAddress, ReadHeaderTimeout: component.config.HTTP.ReadHeaderTimeout,
			ReadTimeout: component.config.HTTP.ReadTimeout, WriteTimeout: component.config.HTTP.WriteTimeout,
			IdleTimeout: component.config.HTTP.IdleTimeout, MaxHeaderBytes: component.config.HTTP.MaxHeaderBytes,
		},
		Router: routerPresentationConfig(component.config, component.engine.registry),
	}, component.engine.api, component.core.IsReady)
	if err != nil {
		return err
	}
	component.server = server
	return server.Start(ctx)
}

func (component *publicRouterComponent) Ready(context.Context) error {
	if component == nil || component.server == nil || !component.server.IsReady() {
		return errors.New("public Router transport is not initialized")
	}
	return nil
}

func (component *publicRouterComponent) Stop(ctx context.Context) error {
	if component == nil || component.server == nil {
		return nil
	}
	return component.server.Stop(ctx)
}

func (component *publicRouterComponent) IsReady() bool {
	return component != nil && component.server != nil && component.server.IsReady()
}

func (component *publicRouterComponent) Errors() <-chan error {
	if component == nil || component.server == nil {
		return idleFailureStream()
	}
	return component.server.Errors()
}

func routerPresentationConfig(config appconfig.Resolved, operations *router.PublicOperationRegistry) router.Config {
	settings := config.RouterAPI
	return router.Config{
		BranchID: settings.BranchID, MaxJSONBytes: settings.MaxJSONBytes, DefaultPageSize: settings.DefaultPageSize,
		MaxPageSize: settings.MaxPageSize, AllowedOrigins: append([]string(nil), config.AdminAPI.AllowedOrigins...),
		PublicOperations: operations, FormSessionCookieSecure: settings.FormSessionCookieSecure,
	}
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
