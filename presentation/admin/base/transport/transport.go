package transport

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	contentengine "github.com/Zubrium/cdn/engines/content/presentation"
	formsengine "github.com/Zubrium/cdn/engines/forms/presentation"
	routerenginepresentation "github.com/Zubrium/cdn/engines/router/presentation"
	websiteengine "github.com/Zubrium/cdn/engines/website/presentation"
	"github.com/Zubrium/cdn/pkg/contracts"
	"github.com/Zubrium/cdn/pkg/observability"
	coordinatoradmin "github.com/Zubrium/cdn/presentation/admin/base/admin/coordinator"
	adminapi "github.com/Zubrium/cdn/presentation/admin/base/admin/main"
	contentadmin "github.com/Zubrium/cdn/presentation/admin/content"
	formsadmin "github.com/Zubrium/cdn/presentation/admin/forms"
	adminrouter "github.com/Zubrium/cdn/presentation/admin/router"
	"github.com/Zubrium/cdn/presentation/fileapi"
	routerhttp "github.com/Zubrium/cdn/presentation/router"
	sitesapi "github.com/Zubrium/cdn/presentation/sitesapi"
)

func NewNotificationHub() *NotificationHub { return adminapi.NewNotificationHub() }

type NotificationHub = adminapi.NotificationHub

type ServerConfig struct {
	Address           string
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	MaxHeaderBytes    int
	MaxInFlight       int
	RequestTimeout    time.Duration
	ShutdownTimeout   time.Duration
}

type AdminConfig struct {
	Server                   ServerConfig
	FileAPI                  fileapi.Config
	AllowedOrigins           []string
	MaxJSONBytes             int64
	DefaultPageSize          int
	MaxPageSize              int
	SessionCookieSecure      bool
	FormsExportMaxConcurrent int
	FormsExportMaxRows       int
	FormsExportMaxBytes      int64
	WorkflowAccessGroup      string
	WorkflowPageDefault      int
	WorkflowPageMaximum      int
	RouterAdminEnabled       bool
	Router                   routerhttp.Config
	UIHandler                http.Handler
	SyntheticData            bool
	IdentityEnabled          bool
	BuildIdentity            string
	Observer                 *observability.Observer
}

type AdminDependencies struct {
	CoreReady                func() bool
	Identity                 adminapi.IdentityAdminAPI
	Sessions                 adminapi.SessionAdminAPI
	GroupAccess              adminapi.SessionGroupAccessResolver
	CoordinatorAuthorization coordinatoradmin.GroupAuthorization
	Files                    fileapi.Loader
	WorkflowControl          coordinatoradmin.WorkflowControl
	Website                  websiteengine.API
	Retirement               sitesapi.PageRetirementAPI
	SnapshotForkWorkflow     sitesapi.SnapshotForkWorkflowStarter
	Content                  contentengine.API
	ContentSchemas           contentadmin.SchemaSnapshots
	Forms                    formsengine.API
	Router                   *routerenginepresentation.APIAdapter
	RouterOperations         *routerhttp.PublicOperationRegistry
	Notifications            *adminapi.NotificationHub
}

type Readiness interface {
	Ready() bool
}

type AdminServer struct {
	config      AdminConfig
	deps        AdminDependencies
	ingress     *ingressLimiter
	mu          sync.RWMutex
	server      *http.Server
	listener    net.Listener
	done        chan struct{}
	failures    chan error
	ready       bool
	application Readiness
}

func NewAdminServer(config AdminConfig, deps AdminDependencies) (*AdminServer, error) {
	if config.Server.Address == "" || deps.CoreReady == nil || deps.Files == nil {
		return nil, errors.New("Admin transport dependencies are incomplete")
	}
	ingress, err := newIngressLimiter(config.Server, config.Observer, "admin")
	if err != nil {
		return nil, err
	}
	if config.IdentityEnabled && (deps.Identity == nil || deps.Sessions == nil || deps.GroupAccess == nil) {
		return nil, errors.New("Admin identity dependencies are incomplete")
	}
	if deps.WorkflowControl != nil && (deps.CoordinatorAuthorization == nil || deps.Sessions == nil) {
		return nil, errors.New("Admin coordinator dependencies are incomplete")
	}
	publishConcurrencyContract(config.Observer, "admin")
	return &AdminServer{config: config, deps: deps, ingress: ingress}, nil
}

func (s *AdminServer) SetApplicationReadiness(readiness Readiness) {
	s.mu.Lock()
	s.application = readiness
	s.mu.Unlock()
}

func (s *AdminServer) Start(ctx context.Context) error {
	if s == nil {
		return errors.New("Admin transport is not configured")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	mux, err := s.adminMux()
	if err != nil {
		return err
	}
	var handler http.Handler = mux
	if s.config.Observer != nil {
		mux.Handle("GET /metrics", s.config.Observer.MetricsHandler())
		handler = s.config.Observer.Handler(observability.Identity{
			ModuleKind: observability.ModuleKindPresentation, ModuleID: "admin", ComponentID: "http-server",
		}, mux)
	}
	handler = s.ingress.Wrap(handler)
	server := &http.Server{Handler: handler, ReadHeaderTimeout: s.config.Server.ReadHeaderTimeout,
		ReadTimeout: s.config.Server.ReadTimeout, WriteTimeout: s.config.Server.WriteTimeout,
		IdleTimeout: s.config.Server.IdleTimeout, MaxHeaderBytes: s.config.Server.MaxHeaderBytes}
	listener, err := net.Listen("tcp", s.config.Server.Address)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.server, s.listener, s.done, s.failures, s.ready = server, listener, make(chan struct{}), make(chan error, 1), true
	s.mu.Unlock()
	go func() {
		defer close(s.done)
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.mu.Lock()
			s.ready = false
			s.mu.Unlock()
			select {
			case s.failures <- errors.New("Admin HTTP server stopped unexpectedly"):
			default:
			}
		}
	}()
	return nil
}

func (s *AdminServer) adminMux() (*http.ServeMux, error) {
	mux := http.NewServeMux()
	guard := func(handler http.Handler) http.Handler { return s.requireCoreRuntime(handler) }
	fileConfig := s.config.FileAPI
	fileConfig.AllowedOrigins = append([]string(nil), s.config.AllowedOrigins...)
	fileHandler, err := fileapi.NewHandler(s.deps.Files, fileConfig)
	if err != nil {
		return nil, err
	}
	mux.Handle("/api/files", guard(fileHandler))
	mux.Handle("/api/files/", guard(fileHandler))
	if s.deps.WorkflowControl != nil {
		authorize, err := coordinatoradmin.NewGroupAuthorizer(s.deps.CoordinatorAuthorization, s.config.WorkflowAccessGroup)
		if err != nil {
			return nil, err
		}
		handler, err := coordinatoradmin.NewHandler(s.deps.WorkflowControl, authorize, coordinatoradmin.Config{
			DefaultPageSize: s.config.WorkflowPageDefault, MaxPageSize: s.config.WorkflowPageMaximum,
			AllowedOrigins:   append([]string(nil), s.config.AllowedOrigins...),
			SessionAuthorize: s.coordinatorSessionAuthorize,
		})
		if err != nil {
			return nil, err
		}
		mux.Handle("/api/coordinator/workflows", guard(handler))
		mux.Handle("/api/coordinator/workflows/", guard(handler))
	}
	if s.config.IdentityEnabled {
		handler, err := adminapi.NewHandlerWithSessions(s.deps.Identity, s.deps.Sessions, adminapi.Config{
			MaxJSONBytes: s.config.MaxJSONBytes, SessionCookieSecure: s.config.SessionCookieSecure,
			AllowedOrigins: append([]string(nil), s.config.AllowedOrigins...),
			GroupAccess:    s.deps.GroupAccess,
			Notifications:  s.deps.Notifications,
		})
		if err != nil {
			return nil, err
		}
		sessionHandler := guard(handler)
		if s.config.SyntheticData {
			sessionHandler = syntheticDataMode(sessionHandler)
		}
		mux.Handle("/api/admin/session", sessionHandler)
		mux.Handle("/api/admin/", guard(handler))
	}
	if s.deps.Website != nil {
		handler, err := sitesapi.NewHandler(s.deps.Website, sitesapi.Config{
			MaxJSONBytes: s.config.MaxJSONBytes, DefaultPageSize: s.config.DefaultPageSize,
			MaxPageSize: s.config.MaxPageSize, AllowedOrigins: append([]string(nil), s.config.AllowedOrigins...),
			Retirement: s.deps.Retirement, SnapshotForkWorkflow: s.deps.SnapshotForkWorkflow,
		})
		if err != nil {
			return nil, fmt.Errorf("initialize Website API: %w", err)
		}
		wrapped := guard(handler)
		if s.config.SyntheticData {
			wrapped = syntheticDataMode(wrapped)
		}
		for _, pattern := range []string{
			"/api/admin/model",
			"/api/admin/model/",
			"/api/admin/pages",
			"/api/admin/pages/",
			"/api/admin/templates",
			"/api/admin/templates/",
			"/api/admin/snapshots",
			"/api/admin/snapshots/",
			"/api/admin/builds",
			"/api/admin/builds/",
			"/api/admin/features",
			"/api/admin/features/",
			"/api/admin/feature-imports",
			"/api/admin/feature-imports/",
			"/api/admin/media",
			"/api/admin/media/",
		} {
			mux.Handle(pattern, wrapped)
		}
	}
	if s.deps.Content != nil {
		handler, err := contentadmin.NewHandler(s.deps.Content, s.deps.ContentSchemas, contentadmin.Config{
			MaxJSONBytes: s.config.MaxJSONBytes, DefaultPageSize: s.config.DefaultPageSize,
			MaxPageSize: s.config.MaxPageSize, AllowedOrigins: append([]string(nil), s.config.AllowedOrigins...),
		})
		if err != nil {
			return nil, fmt.Errorf("initialize Content API: %w", err)
		}
		wrapped := guard(handler)
		if s.config.SyntheticData {
			wrapped = syntheticDataMode(wrapped)
		}
		mux.Handle("/api/admin/content/", wrapped)
	}
	if s.deps.Forms != nil {
		handler, err := formsadmin.NewHandler(s.deps.Forms, formsadmin.Config{
			MaxJSONBytes: s.config.MaxJSONBytes, DefaultPageSize: s.config.DefaultPageSize,
			MaxPageSize: s.config.MaxPageSize, ExportMaxConcurrent: s.config.FormsExportMaxConcurrent,
			ExportMaxRows: s.config.FormsExportMaxRows, ExportMaxBytes: s.config.FormsExportMaxBytes,
			AllowedOrigins: append([]string(nil), s.config.AllowedOrigins...),
		})
		if err != nil {
			return nil, fmt.Errorf("initialize Forms API: %w", err)
		}
		wrapped := guard(handler)
		if s.config.SyntheticData {
			wrapped = syntheticDataMode(wrapped)
		}
		mux.Handle("/api/admin/forms/", wrapped)
	}
	if s.config.RouterAdminEnabled {
		if s.deps.Router == nil || s.deps.RouterOperations == nil {
			return nil, errors.New("Router Admin dependencies are not initialized")
		}
		routerConfig := s.config.Router
		routerConfig.AllowedOrigins = append([]string(nil), s.config.AllowedOrigins...)
		routerConfig.PublicOperations = s.deps.RouterOperations
		handler, err := adminrouter.NewHandler(s.deps.Router, routerConfig)
		if err != nil {
			return nil, err
		}
		mux.Handle("/api/admin/router", guard(handler))
		mux.Handle("/api/admin/router/", guard(handler))
	}
	if s.config.UIHandler != nil {
		mux.Handle("/admin", s.config.UIHandler)
		mux.Handle("/admin/", s.config.UIHandler)
	}
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if s.config.BuildIdentity != "" {
			w.Header().Set("X-Zubrium-Build-Identity", s.config.BuildIdentity)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		if !s.readyForRequests() {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Retry-After", "1")
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNoContent)
	})
	return mux, nil
}

func (s *AdminServer) requireCoreRuntime(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.readyForRequests() {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Retry-After", "1")
			http.Error(w, "service temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *AdminServer) readyForRequests() bool {
	if s == nil || !s.IsReady() || !s.deps.CoreReady() {
		return false
	}
	s.mu.RLock()
	application := s.application
	s.mu.RUnlock()
	return application != nil && application.Ready()
}

func (s *AdminServer) Ready(context.Context) error {
	if !s.IsReady() {
		return errors.New("Admin HTTP listener is not initialized")
	}
	return nil
}

func (s *AdminServer) Stop(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	server, done := s.server, s.done
	s.ready = false
	s.mu.Unlock()
	if server == nil {
		return nil
	}
	ingressErr := s.ingress.Close(ctx)
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
	err = errors.Join(ingressErr, err)
	s.mu.Lock()
	s.server, s.listener, s.done = nil, nil, nil
	s.mu.Unlock()
	return err
}

func (s *AdminServer) IsReady() bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ready && s.listener != nil
}

func (s *AdminServer) Errors() <-chan error {
	if s == nil {
		return closedErrors()
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.failures == nil {
		return closedErrors()
	}
	return s.failures
}

func (s *AdminServer) Address() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.listener == nil {
		return ""
	}
	return s.listener.Addr().String()
}

type PublicConfig struct {
	Server   ServerConfig
	Router   routerhttp.Config
	Observer *observability.Observer
	// PerRoute is supplied by composition from router.concurrency.http.per_route.
	// Its keys are static startup configuration, never visitor input.
	PerRoute PerRouteAdmission
}

type PublicServer struct {
	config   PublicConfig
	api      *routerenginepresentation.APIAdapter
	readyFn  func() bool
	ingress  *ingressLimiter
	mu       sync.RWMutex
	server   *http.Server
	listener net.Listener
	done     chan struct{}
	failures chan error
	ready    bool
}

func NewPublicServer(config PublicConfig, api *routerenginepresentation.APIAdapter, ready func() bool) (*PublicServer, error) {
	if config.Server.Address == "" || api == nil || ready == nil {
		return nil, errors.New("public Router transport dependencies are incomplete")
	}
	routes, err := buildRouteAdmission(config.PerRoute, config.Server.MaxInFlight)
	if err != nil {
		return nil, err
	}
	ingress, err := newIngressLimiterWithRoutes(config.Server, config.Observer, "public-router", routes)
	if err != nil {
		return nil, err
	}
	publishConcurrencyContract(config.Observer, "public-router")
	return &PublicServer{config: config, api: api, readyFn: ready, ingress: ingress}, nil
}

func (s *PublicServer) Start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	handler, err := routerhttp.NewHandler(s.api, s.config.Router)
	if err != nil {
		return err
	}
	publicHandler := handler.PublicHTTPHandler()
	guarded := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.IsReady() || !s.readyFn() {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Retry-After", "1")
			http.Error(w, "service temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		publicHandler.ServeHTTP(w, r)
	})
	var observed http.Handler = guarded
	if s.config.Observer != nil {
		observed = s.config.Observer.Handler(observability.Identity{
			ModuleKind: observability.ModuleKindPresentation, ModuleID: "public-router", ComponentID: "http-server",
		}, guarded)
	}
	observed = s.ingress.Wrap(observed)
	server := &http.Server{Handler: observed, ReadHeaderTimeout: s.config.Server.ReadHeaderTimeout,
		ReadTimeout: s.config.Server.ReadTimeout, WriteTimeout: s.config.Server.WriteTimeout,
		IdleTimeout: s.config.Server.IdleTimeout, MaxHeaderBytes: s.config.Server.MaxHeaderBytes}
	listener, err := net.Listen("tcp", s.config.Server.Address)
	if err != nil {
		return fmt.Errorf("listen on internal Router address: %w", err)
	}
	s.mu.Lock()
	s.server, s.listener, s.done, s.failures, s.ready = server, listener, make(chan struct{}), make(chan error, 1), true
	s.mu.Unlock()
	go func() {
		defer close(s.done)
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.mu.Lock()
			s.ready = false
			s.mu.Unlock()
			select {
			case s.failures <- errors.New("public Router listener stopped unexpectedly"):
			default:
			}
		}
	}()
	return nil
}

func (s *PublicServer) Ready(context.Context) error {
	if !s.IsReady() {
		return errors.New("public Router listener is not initialized")
	}
	return nil
}

func (s *PublicServer) Stop(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	server, done := s.server, s.done
	s.ready = false
	s.mu.Unlock()
	if server == nil {
		return nil
	}
	ingressErr := s.ingress.Close(ctx)
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
	err = errors.Join(ingressErr, err)
	s.mu.Lock()
	s.server, s.listener, s.done = nil, nil, nil
	s.mu.Unlock()
	return err
}

func (s *PublicServer) IsReady() bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ready && s.listener != nil
}

func (s *PublicServer) Errors() <-chan error {
	if s == nil {
		return closedErrors()
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.failures == nil {
		return closedErrors()
	}
	return s.failures
}

func (s *PublicServer) Address() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.listener == nil {
		return ""
	}
	return s.listener.Addr().String()
}

func syntheticDataMode(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Zubrium-Data-Mode", "synthetic")
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func (s *AdminServer) coordinatorSessionAuthorize(ctx context.Context, token string, permission coordinatoradmin.Permission) error {
	session, _, err := s.deps.Sessions.AuthenticateSession(ctx, token)
	if err != nil {
		return err
	}
	level := contracts.AccessLevelRead
	if permission == coordinatoradmin.RetryTasks {
		level = contracts.AccessLevelWrite
	} else if permission != coordinatoradmin.ReadWorkflows {
		return contracts.ErrAccessForbidden
	}
	return s.deps.CoordinatorAuthorization.RequireGroupLevel(ctx, session, s.config.WorkflowAccessGroup, level)
}

func closedErrors() <-chan error {
	channel := make(chan error)
	close(channel)
	return channel
}
