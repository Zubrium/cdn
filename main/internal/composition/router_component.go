package composition

import (
	"context"
	"errors"
	"fmt"
	"time"

	platform "github.com/Zubrium/cdn/core/presentation"
	routerengine "github.com/Zubrium/cdn/engines/router"
	routesapp "github.com/Zubrium/cdn/engines/router/features/routes/application"
	routesrepo "github.com/Zubrium/cdn/engines/router/features/routes/repository"
	routerenginepresentation "github.com/Zubrium/cdn/engines/router/presentation"
	"github.com/Zubrium/cdn/main/internal/bus"
	appconfig "github.com/Zubrium/cdn/main/internal/config"
	"github.com/Zubrium/cdn/pkg/contracts"
	"github.com/Zubrium/cdn/pkg/observability"
	"github.com/Zubrium/cdn/presentation/admin/base/transport"
	"github.com/Zubrium/cdn/presentation/router"
	"github.com/Zubrium/cdn/presentation/router/publiccontentruntime"
	"github.com/Zubrium/cdn/presentation/router/publicforms"
	"github.com/Zubrium/cdn/presentation/router/publicupload"
)

type routerEngineComponent struct {
	core         *coreComponent
	loader       *loaderComponent
	forms        *formsEngineComponent
	content      *contentEngineComponent
	config       appconfig.Resolved
	selected     map[string]struct{}
	dispatcher   contracts.Dispatcher
	observer     *observability.Observer
	api          *routerenginepresentation.APIAdapter
	registry     *router.PublicOperationRegistry
	routes       router.RouteResolver
	delivery     *router.CoreDelivery
	public       *routerPublicServices
	warmupReady  bool
	warmupCancel context.CancelFunc
	warmupDone   chan struct{}
}

func (component *routerEngineComponent) Start(ctx context.Context) error {
	if component == nil || component.core == nil || component.core.runtime == nil || component.loader == nil || component.loader.service == nil {
		return errors.New("Router dependencies are not initialized")
	}
	settings := component.config.RouterAPI
	operations := make([]routerenginepresentation.PublicOperation, 0, 4)
	moduleDispatcher := bus.NewModuleDispatcher(component.dispatcher, "engine.router")
	access, err := contracts.NewCoreAccessClient(moduleDispatcher)
	if err != nil {
		return fmt.Errorf("configure Router Core access DataBus client: %w", err)
	}
	var formsEngineAPI routerPublicFormsEngine
	if component.forms != nil {
		if component.forms.engine == nil {
			return errors.New("Forms Engine is not initialized")
		}
		formsEngineAPI = component.forms.engine
	}
	publicServices, err := newRouterPublicServices(routerPublicDependencies{
		forms: formsEngineAPI, loader: component.loader.service,
		staging: loaderFiles{runtime: component.core.runtime}, authorization: moduleCoreGroupAuthorization{access: access},
		credential: settings.ServiceCredential, partSize: component.config.Loader.PartSize,
		maxFile: settings.UploadMaxFileBytes, maxParts: component.config.Loader.MaxParts,
		maxInFlight: settings.UploadMaxConcurrent,
	})
	if err != nil {
		return fmt.Errorf("configure public Router DataBus services: %w", err)
	}
	component.public = publicServices
	formsAPI := publicforms.VisitorAPI(publicServices.Forms)
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
		upload, err := publicupload.New(publicServices.Loader, publicupload.Config{
			Credential: settings.ServiceCredential, MaxFileBytes: settings.UploadMaxFileBytes,
			PartSize: component.config.Loader.PartSize, MaxParts: component.config.Loader.MaxParts,
			MaxConcurrentUploads: settings.UploadMaxConcurrent,
			AllowedContentTypes:  append([]string(nil), settings.UploadAllowedContentTypes...),
			GroupAuthorization:   moduleCoreGroupAuthorization{access: access},
			Forms:                formsAPI,
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
	moduleData, err := component.core.registerModuleData(routerengine.RecordDescriptor())
	if err != nil {
		return fmt.Errorf("register Router Core module data: %w", err)
	}
	repository, err := routesrepo.NewModuleDataRepository(moduleData)
	if err != nil {
		return err
	}
	fileSession, _, err := component.core.runtime.Identity().Authenticate(ctx, settings.ServiceCredential)
	if err != nil {
		return fmt.Errorf("authenticate Router file service: %w", err)
	}
	component.core.setCoreFiles("engine.router", coreSessionFileAdapter{files: component.core.runtime.Files(), session: fileSession})
	files, err := contracts.NewCoreFilesClient(moduleDispatcher, func() contracts.StreamRegistrar {
		return bus.NewScopedStreams()
	})
	if err != nil {
		return fmt.Errorf("configure Router Core file DataBus client: %w", err)
	}
	validator, err := router.NewCoreTargetValidator(files, registry)
	if err != nil {
		return err
	}
	routeService, err := routesapp.New(repository, access, access, validator, routesapp.Config{
		BranchID: settings.BranchID, AccessGroup: "router_routes", DefaultPageSize: settings.DefaultPageSize, MaxPageSize: settings.MaxPageSize,
	})
	if err != nil {
		return err
	}
	scales := component.config.Scales.RouterCache
	manifestTTL, err := time.ParseDuration(scales.Manifests.TTL)
	if err != nil {
		return fmt.Errorf("invalid router manifest cache TTL: %w", err)
	}
	routeTTL, err := time.ParseDuration(scales.Routes.TTL)
	if err != nil {
		return fmt.Errorf("invalid router route cache TTL: %w", err)
	}
	delivery, err := router.NewCoreDeliveryWithCache(settings.BranchID, files, routeService, registry, router.RouterCacheConfig{
		Manifests: router.CacheSettings{Enabled: scales.Manifests.Enabled, MaxEntries: scales.Manifests.MaxEntries, MaxBytes: scales.Manifests.MaxBytes, TTL: manifestTTL},
		Routes:    router.CacheSettings{Enabled: scales.Routes.Enabled, MaxEntries: scales.Routes.MaxEntries, MaxBytes: scales.Routes.MaxBytes, TTL: routeTTL},
		Observer:  routerCacheObserver{observer: component.observer},
	})
	if err != nil {
		return err
	}
	warmupConfig := routerWarmupConfig(component.config)
	warmupTargets := routerWarmupTargets(component.config)
	if len(warmupTargets) > 0 && (warmupConfig.Manifests || warmupConfig.Routes) {
		result, err := delivery.Warmup(ctx, warmupTargets, warmupConfig)
		if err != nil && component.config.Core.Warmup.Mode == platform.CacheModeStrict {
			return fmt.Errorf("warm Router caches before readiness: %w", err)
		}
		if err != nil && component.observer != nil {
			component.observer.Log(ctx, observability.LevelWarn, "Router cache warm-up completed with failures",
				observability.String("component.id", "engine.router"),
				observability.Int("warmup.failed", int64(result.Failed)))
		}
	}
	api, err := routerenginepresentation.NewAPIAdapter(routeService, delivery, settings.BranchID)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if warmupConfig.RefreshInterval > 0 && len(warmupTargets) > 0 {
		refreshCtx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		component.warmupCancel, component.warmupDone = cancel, done
		go func() {
			defer close(done)
			_ = delivery.WarmupRefreshLoop(refreshCtx, warmupTargets, warmupConfig)
		}()
	}
	component.registry, component.api, component.routes, component.delivery = registry, api, routeService, delivery
	component.warmupReady = true
	return nil
}

func (component *routerEngineComponent) Ready(context.Context) error {
	if component == nil || component.api == nil || component.registry == nil || !component.warmupReady {
		return errors.New("Router Engine is not initialized")
	}
	return nil
}

func (component *routerEngineComponent) Stop(ctx context.Context) error {
	if component == nil {
		return nil
	}
	if component.warmupCancel != nil {
		component.warmupCancel()
	}
	if component.warmupDone != nil {
		select {
		case <-component.warmupDone:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	component.warmupCancel = nil
	component.warmupDone = nil
	if component.public != nil {
		if err := component.public.Close(ctx); err != nil {
			return err
		}
		component.public = nil
	}
	component.api = nil
	component.registry = nil
	component.delivery = nil
	component.warmupReady = false
	return nil
}

func (component *routerEngineComponent) IsReady() bool {
	return component != nil && component.api != nil && component.registry != nil && component.warmupReady
}

type routerAdminComponent struct{}

func (*routerAdminComponent) Start(context.Context) error { return nil }
func (*routerAdminComponent) Ready(context.Context) error { return nil }
func (*routerAdminComponent) Stop(context.Context) error  { return nil }

type publicRouterComponent struct {
	core     *coreComponent
	engine   *routerEngineComponent
	config   appconfig.Resolved
	observer *observability.Observer
	server   *transport.PublicServer
}

func resolvePerRouteAdmission(ctx context.Context, component *publicRouterComponent) (transport.PerRouteAdmission, error) {
	setting := component.config.Scales.RouterHTTP.PerRoute
	if !setting.Enabled {
		return transport.PerRouteAdmission{}, nil
	}
	admission := transport.PerRouteAdmission{
		Enabled:            true,
		DefaultMaxInFlight: setting.DefaultMaxInFlight,
		Overrides:          make([]transport.RouteAdmissionOverride, 0, len(setting.Overrides)),
	}
	for _, override := range setting.Overrides {
		route, err := component.engine.api.GetRoute(ctx, override.ID)
		if err != nil {
			return transport.PerRouteAdmission{}, fmt.Errorf("resolve per-route admission override %q: %w", override.ID, err)
		}
		admission.Overrides = append(admission.Overrides, transport.RouteAdmissionOverride{
			ID: override.ID, Path: route.Path, MaxInFlight: override.MaxInFlight,
		})
	}
	return admission, nil
}

func (component *publicRouterComponent) Start(ctx context.Context) error {
	if component == nil || component.core == nil || component.core.runtime == nil || component.engine == nil || component.engine.api == nil {
		return errors.New("public Router dependencies are not initialized")
	}
	perRoute, err := resolvePerRouteAdmission(ctx, component)
	if err != nil {
		return err
	}
	settings := component.config.RouterAPI
	server, err := transport.NewPublicServer(transport.PublicConfig{
		Observer: component.observer,
		Server: transport.ServerConfig{
			Address: settings.PublicListenAddress, ReadHeaderTimeout: component.config.HTTP.ReadHeaderTimeout,
			ReadTimeout: component.config.HTTP.ReadTimeout, WriteTimeout: component.config.HTTP.WriteTimeout,
			IdleTimeout: component.config.HTTP.IdleTimeout, MaxHeaderBytes: component.config.HTTP.MaxHeaderBytes,
			MaxInFlight:     component.config.Scales.RouterHTTP.MaxInFlight,
			RequestTimeout:  component.config.Scales.RouterHTTP.RequestDuration(),
			ShutdownTimeout: component.config.HTTP.ShutdownTimeout,
		},
		Router:   routerPublicPresentationConfig(component.config, component.engine.registry, component.engine.routes, component.engine.publicStreams()),
		PerRoute: perRoute,
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
		Compression: router.CompressionConfig{
			Enabled:              config.Scales.RouterCache.HTTP.Compression.Enabled,
			Algorithms:           append([]string(nil), config.Scales.RouterCache.HTTP.Compression.Algorithms...),
			MinBytes:             config.Scales.RouterCache.HTTP.Compression.MinBytes,
			MaxUncompressedBytes: config.Scales.RouterCache.HTTP.Compression.MaxUncompressedBytes,
			MaxCompressedBytes:   config.Scales.RouterCache.HTTP.Compression.MaxCompressedBytes,
			MediaTypes:           append([]string(nil), config.Scales.RouterCache.HTTP.Compression.MediaTypes...),
		},
	}
}

// routerPublicPresentationConfig adds the explicitly compiled locale
// preference operation to the anonymous listener only. The Admin listener
// keeps the plain configuration and never registers a public preference path.
func routerPublicPresentationConfig(config appconfig.Resolved, operations *router.PublicOperationRegistry, routes router.RouteResolver,
	streams func() contracts.PublicUploadStreams) router.Config {
	cfg := routerPresentationConfig(config, operations)
	settings := config.RouterAPI
	cfg.LocalePreference = router.LocalePreferenceConfig{
		Enabled: settings.LocalePreferenceEnabled,
		Secure:  settings.LocalePreferenceSecure,
		MaxAge:  settings.LocalePreferenceMaxAge,
	}
	cfg.LocaleResolver = routes
	cfg.UploadStreams = streams
	return cfg
}

func (component *routerEngineComponent) publicStreams() func() contracts.PublicUploadStreams {
	if component == nil || component.public == nil {
		return nil
	}
	return component.public.Streams
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
