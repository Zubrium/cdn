package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	publicruntime "websiteengine/pkg/runtime"

	"websiteengine/features/auth"
	"websiteengine/features/features"
	filefeature "websiteengine/features/files"
	"websiteengine/features/forms"
	"websiteengine/features/media"
	"websiteengine/features/overview"
	"websiteengine/features/settings"
	"websiteengine/features/sitebuilder"
	"websiteengine/migrations"
	"websiteengine/pkg/config"
	"websiteengine/pkg/environment"
	"websiteengine/pkg/postgres"
	"websiteengine/pkg/storage/s3"
	httptransport "websiteengine/presentation/http"
	hauth "websiteengine/presentation/http/handlers/auth"
	hfeatures "websiteengine/presentation/http/handlers/features"
	hfiles "websiteengine/presentation/http/handlers/files"
	hforms "websiteengine/presentation/http/handlers/forms"
	hmedia "websiteengine/presentation/http/handlers/media"
	hoverview "websiteengine/presentation/http/handlers/overview"
	hsettings "websiteengine/presentation/http/handlers/settings"
	hsitebuilder "websiteengine/presentation/http/handlers/sitebuilder"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	var err error
	if len(os.Args) > 1 && os.Args[1] == "rename-branch" {
		err = renameBranch(os.Args[2:])
	} else if len(os.Args) > 1 && os.Args[1] == "rename-service" {
		err = renameService(os.Args[2:])
	} else {
		err = run(log)
	}
	if err != nil {
		log.Error("server stopped with error", "error", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	// API timestamps are always emitted in UTC regardless of host timezone.
	time.Local = time.UTC
	startup, err := loadStartupConfiguration()
	if err != nil {
		return err
	}
	cfg, filesCfg, pagination := startup.Engine, startup.Files, startup.Pagination
	listenAddresses := make([]string, 0, len(cfg.Branches)+1)
	listenAddresses = append(listenAddresses, cfg.HTTPAddr)
	for _, branch := range cfg.Branches {
		listenAddresses = append(listenAddresses, branch.HTTPAddr)
	}
	listeners, err := bindHTTPAddresses(listenAddresses)
	if err != nil {
		return err
	}
	defer closeHTTPListeners(listeners)
	level := slog.LevelInfo
	switch cfg.LogLevel {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	log = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := postgres.Open(ctx, cfg.DatabaseURL, cfg.Database)
	if err != nil {
		return err
	}
	defer pool.Close()

	if cfg.MigrateOnStart {
		if err := postgres.Migrate(ctx, pool, migrations.FS, cfg.MigrationLockTimeout); err != nil {
			return fmt.Errorf("migrations: %w", err)
		}
		log.Info("database migrations applied")
	}

	authRepo := auth.NewRepository(pool)
	authService := auth.NewService(authRepo)
	if err := authService.SetListLimit(pagination.AdminPageSize); err != nil {
		return fmt.Errorf("token pagination: %w", err)
	}
	created, err := authService.Bootstrap(ctx, cfg.BootstrapAdminToken)
	if err != nil {
		return fmt.Errorf("bootstrap admin token: %w", err)
	}
	if created {
		log.Warn("bootstrap administrator token created")
	} else {
		hasAdmin, err := authService.LookupForBootstrap(ctx)
		if err != nil {
			return err
		}
		if !hasAdmin {
			log.Warn("no active admin token exists; set a new BOOTSTRAP_ADMIN_TOKEN or restore an admin token")
		}
	}

	objectStore, err := s3.New(ctx, cfg.S3)
	if err != nil {
		return fmt.Errorf("S3 storage: %w", err)
	}
	if cfg.S3.EnsureBucket {
		if err := ensureBucket(ctx, objectStore, cfg.S3StartupTimeout, cfg.S3RetryInterval); err != nil {
			return fmt.Errorf("ensure S3 bucket: %w", err)
		}
	}
	if err := objectStore.RequirePrivate(ctx); err != nil {
		return fmt.Errorf("file storage privacy: %w", err)
	}
	featureBuildService := sitebuilder.NewBuildService(pool, objectStore, cfg.MaxFeatureBuildBytes)
	if err := featureBuildService.SetRegistrationTimeout(cfg.FeatureBuildRegistrationTimeout); err != nil {
		return fmt.Errorf("feature build registration: %w", err)
	}
	featureBuildHandler := hsitebuilder.NewBuildsHandler(featureBuildService, log, cfg.MaxFeatureBuildBytes)
	snapshotService := sitebuilder.NewSnapshotService(pool, featureBuildService)
	if err := snapshotService.SetDesignPreviewTTL(cfg.DesignPreviewTTL); err != nil {
		return fmt.Errorf("design preview configuration: %w", err)
	}
	branches, err := snapshotService.RegisterBranches(ctx, cfg.Branches)
	if err != nil {
		return fmt.Errorf("register branches: %w", err)
	}
	corsByBranch := map[environment.Environment][]string{}
	corsOrigins := []string{}
	seenCORSOrigin := map[string]bool{}
	for _, branch := range branches {
		corsByBranch[environment.Environment(branch.ID.String())] = branch.CORSOrigins
		for _, origin := range branch.CORSOrigins {
			if !seenCORSOrigin[origin] {
				seenCORSOrigin[origin] = true
				corsOrigins = append(corsOrigins, origin)
			}
		}
	}

	featureRepo := features.NewRepository(pool)
	featureService := features.NewService(featureRepo)
	featureHandler := hfeatures.NewHandler(featureService, log)
	if err := featureHandler.SetPagination(pagination.PublicPageSize, pagination.AdminPageSize); err != nil {
		return fmt.Errorf("feature pagination: %w", err)
	}

	formsRepo := forms.NewRepository(pool)
	formsService := forms.NewService(formsRepo, featureRepo)
	if err := formsService.SetLimits(pagination.AdminPageSize, pagination.SubmissionExportMaxRows); err != nil {
		return fmt.Errorf("form limits: %w", err)
	}
	formsService.SetSnapshotSchemaProvider(snapshotService)
	formsHandler := hforms.NewHandler(formsService, log, cfg.MaxJSONBytes)
	if err := formsHandler.SetLimits(pagination.PublicPageSize, pagination.AdminPageSize); err != nil {
		return fmt.Errorf("form pagination: %w", err)
	}
	snapshotHandler := hsitebuilder.NewSnapshotsHandler(snapshotService, formsService, log, cfg.MaxJSONBytes)
	if err := snapshotHandler.SetPagination(pagination.PublicPageSize, pagination.AdminPageSize); err != nil {
		return fmt.Errorf("snapshot pagination: %w", err)
	}
	sitepackCfg := startup.Sitepack
	sitepackHandler := hsitebuilder.NewSitepackHandler(snapshotService, sitebuilder.SitepackOptions{TempDir: sitepackCfg.TempDir, MaxBytes: sitepackCfg.MaxBytes, MaxExpandedBytes: sitepackCfg.MaxExpandedBytes, MaxEntries: sitepackCfg.MaxEntries, MaxIndexBytes: sitepackCfg.MaxIndexBytes, MaxSVGBytes: sitepackCfg.MaxSVGBytes, MaxBrowserBlobBytes: sitepackCfg.MaxBrowserBlobBytes}, log)

	mediaRepo := media.NewRepository(pool)
	processor, err := media.NewFFmpegProcessorWithSettings(filesCfg.TempDir, filesCfg.ProcessingTimeout)
	if err != nil {
		return fmt.Errorf("media processor: %w", err)
	}
	mediaService := media.NewServiceWithProcessor(mediaRepo, objectStore, cfg.MaxUploadBytes, processor)
	if err := mediaService.SetRenditionWorkerConfig(cfg.MediaRenditionLease, cfg.MediaWorkerPollInterval); err != nil {
		return fmt.Errorf("media rendition worker configuration: %w", err)
	}
	snapshotService.SetMediaService(mediaService)
	mediaService.SetReferenceChecker(snapshotService.MediaReferenced)
	go mediaService.RunWorker(ctx, log)
	mediaHandler := hmedia.NewHandler(mediaService, log, cfg.MaxUploadBytes)
	if err := mediaHandler.SetPagination(pagination.PublicPageSize, pagination.AdminPageSize); err != nil {
		return fmt.Errorf("media pagination: %w", err)
	}
	if err := mediaHandler.SetRedirectCacheTTL(cfg.MediaRedirectCacheTTL); err != nil {
		return fmt.Errorf("media redirect cache TTL: %w", err)
	}
	mediaHandler.SetProfileProvider(snapshotService)
	fileService, err := filefeature.NewService(pool, objectStore, filesCfg)
	if err != nil {
		return err
	}
	if err := fileService.InitOwners(ctx); err != nil {
		return err
	}
	fileService.SetReferenceManager(snapshotService)
	go fileService.RunCleanup(ctx, log)
	fileHandler := hfiles.NewHandler(fileService, log, cfg.MaxJSONBytes)
	fileHandler.SetTrustProxy(cfg.TrustProxyHeaders)
	mediaHandler.SetFileHandler(fileHandler)

	overviewRepo := overview.NewRepository(pool)
	overviewHandler := hoverview.NewHandler(overviewRepo, log)

	authHandler := hauth.NewHandler(authService, log, cfg.MaxJSONBytes)

	settingsService := settings.NewService(settings.Config{
		MaxJSONBytes:            cfg.MaxJSONBytes,
		MaxUploadBytes:          cfg.MaxUploadBytes,
		FormRateLimit:           cfg.FormRateLimit,
		FormRateWindowSeconds:   int(cfg.FormRateWindow / time.Second),
		PublicCORSOrigins:       corsOrigins,
		AdminPageSize:           pagination.AdminPageSize,
		PublicPageSize:          pagination.PublicPageSize,
		SubmissionExportMaxRows: pagination.SubmissionExportMaxRows,
		AcceptedContentTypes:    media.AcceptedContentTypes(),
		ImageVariants:           media.ImageVariantNames(),
	})
	settingsHandler := hsettings.NewHandler(settingsService)

	health := func(checkCtx context.Context) error {
		if err := pool.Ping(checkCtx); err != nil {
			return fmt.Errorf("postgres: %w", err)
		}
		if err := mediaService.Ready(checkCtx); err != nil {
			return fmt.Errorf("media storage: %w", err)
		}
		return nil
	}

	prerenderOptions := startup.Prerender
	prerenderers := map[environment.Environment]*publicruntime.Prerenderer{}
	indexable := map[environment.Environment]bool{}
	for _, branch := range branches {
		key := environment.Environment(branch.ID.String())
		indexable[key] = branch.Indexable
		if prerenderOptions != nil {
			_, port, _ := net.SplitHostPort(branch.HTTPAddr)
			renderer, err := publicruntime.NewPrerenderer("http://127.0.0.1:"+port, *prerenderOptions)
			if err != nil {
				return err
			}
			renderer.SetLogger(log)
			prerenderers[key] = renderer
		}
	}
	deps := httptransport.Dependencies{
		BranchIndexable: indexable, Prerender: prerenderers,
		Log: log, AuthService: authService, Auth: authHandler, Files: fileHandler,
		Features: featureHandler, Forms: formsHandler, Media: mediaHandler,
		Overview: overviewHandler, Health: health, MaxJSONBytes: cfg.MaxJSONBytes, ReadinessTimeout: cfg.ReadinessTimeout,
		FeatureBuilds: featureBuildHandler, SiteSnapshots: snapshotHandler, Sitepacks: sitepackHandler,
		Settings:      settingsHandler,
		FormRateLimit: cfg.FormRateLimit, FormRateWindow: cfg.FormRateWindow,
		FormRateMaxClients: cfg.FormRateMaxClients, FormRateSweepInterval: cfg.FormRateSweepInterval,
		RuntimeAssetCacheTTL: cfg.RuntimeAssetCacheTTL,
		TrustProxy:           cfg.TrustProxyHeaders, PublicCORSByBranch: corsByBranch,
		S3PublicBaseURL: cfg.S3.PublicBaseURL,
	}

	servers := []*http.Server{newServer(cfg.HTTPAddr, httptransport.NewRouter(deps), cfg)}
	for _, branch := range branches {
		servers = append(servers, newServer(branch.HTTPAddr, httptransport.NewPublicRouter(deps, environment.Environment(branch.ID.String())), cfg))
	}

	errCh := make(chan error, len(servers))
	for _, server := range servers {
		server := server
		listener, ok := listeners[server.Addr]
		if !ok {
			return fmt.Errorf("no listener was reserved for %q", server.Addr)
		}
		go func() {
			log.Info("HTTP server listening", "addr", server.Addr)
			errCh <- server.Serve(listener)
		}()
	}

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		for _, server := range servers {
			if err := server.Shutdown(shutdownCtx); err != nil {
				return fmt.Errorf("shutdown: %w", err)
			}
		}
		log.Info("server stopped")
		return nil
	case err := <-errCh:
		for _, server := range servers {
			_ = server.Close()
		}
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

type startupConfiguration struct {
	Engine     config.Config
	Files      config.FilesConfig
	Pagination config.PaginationConfig
	Sitepack   config.SitepackConfig
	Prerender  *publicruntime.PrerenderOptions
}

// loadStartupConfiguration validates every operator-controlled group before
// the process binds a port or connects to PostgreSQL or object storage.
func loadStartupConfiguration() (startupConfiguration, error) {
	var result startupConfiguration
	var err error
	result.Engine, err = config.Load()
	if err != nil {
		return startupConfiguration{}, fmt.Errorf("configuration: %w", err)
	}
	result.Files, err = config.LoadFiles()
	if err != nil {
		return startupConfiguration{}, fmt.Errorf("file configuration: %w", err)
	}
	result.Pagination, err = config.LoadPagination()
	if err != nil {
		return startupConfiguration{}, fmt.Errorf("pagination configuration: %w", err)
	}
	result.Sitepack, err = config.LoadSitepack()
	if err != nil {
		return startupConfiguration{}, fmt.Errorf("sitepack configuration: %w", err)
	}
	result.Prerender, err = config.LoadPrerender()
	if err != nil {
		return startupConfiguration{}, fmt.Errorf("prerender configuration: %w", err)
	}
	return result, nil
}

func bindHTTPAddresses(addresses []string) (map[string]net.Listener, error) {
	listeners := make(map[string]net.Listener, len(addresses))
	for _, address := range addresses {
		listener, err := net.Listen("tcp", address)
		if err != nil {
			closeHTTPListeners(listeners)
			return nil, fmt.Errorf("bind HTTP listener %q: %w", address, err)
		}
		listeners[address] = listener
	}
	return listeners, nil
}

func closeHTTPListeners(listeners map[string]net.Listener) {
	for _, listener := range listeners {
		_ = listener.Close()
	}
}

func newServer(addr string, handler http.Handler, cfg config.Config) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		MaxHeaderBytes:    cfg.MaxHeaderBytes,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
	}
}

// ensureBucket tolerates object storage starting slightly after the API in
// local Docker Compose setups.
type bucketEnsurer interface {
	EnsureBucket(context.Context) error
}

func ensureBucket(ctx context.Context, store bucketEnsurer, timeout, retryInterval time.Duration) error {
	if timeout <= 0 || retryInterval <= 0 {
		return fmt.Errorf("S3 startup timeout and retry interval must be explicitly configured")
	}
	startupCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		if err := startupCtx.Err(); err != nil {
			return err
		}
		if err := store.EnsureBucket(startupCtx); err == nil {
			return nil
		} else if startupCtx.Err() != nil {
			return startupCtx.Err()
		}
		timer := time.NewTimer(retryInterval)
		select {
		case <-startupCtx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return startupCtx.Err()
		case <-timer.C:
		}
	}
}
