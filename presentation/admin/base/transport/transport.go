package transport

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sort"
	"sync"
	"time"

	core "github.com/Zubrium/cdn/core/presentation"
	contentengine "github.com/Zubrium/cdn/engines/content/presentation"
	formsengine "github.com/Zubrium/cdn/engines/forms/presentation"
	routerenginepresentation "github.com/Zubrium/cdn/engines/router/presentation"
	websiteengine "github.com/Zubrium/cdn/engines/website/presentation"
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
	AccessGroups             []string
	RouterAdminEnabled       bool
	Router                   routerhttp.Config
	SyntheticData            bool
	IdentityEnabled          bool
}

type AdminDependencies struct {
	Core             *core.Runtime
	Files            fileapi.Loader
	WorkflowControl  coordinatoradmin.WorkflowControl
	Website          websiteengine.API
	Content          contentengine.API
	ContentSchemas   contentadmin.SchemaSnapshots
	Forms            formsengine.API
	Router           *routerenginepresentation.APIAdapter
	RouterOperations *routerhttp.PublicOperationRegistry
	Notifications    *adminapi.NotificationHub
}

type Readiness interface {
	Ready() bool
}

type AdminServer struct {
	config      AdminConfig
	deps        AdminDependencies
	mu          sync.RWMutex
	server      *http.Server
	listener    net.Listener
	done        chan struct{}
	failures    chan error
	ready       bool
	application Readiness
}

func NewAdminServer(config AdminConfig, deps AdminDependencies) (*AdminServer, error) {
	if config.Server.Address == "" || deps.Core == nil || deps.Files == nil {
		return nil, errors.New("Admin transport dependencies are incomplete")
	}
	return &AdminServer{config: config, deps: deps}, nil
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
	server := &http.Server{Handler: mux, ReadHeaderTimeout: s.config.Server.ReadHeaderTimeout,
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
		authorization := coordinatorRuntimeAuthorization{runtime: s.deps.Core}
		authorize, err := coordinatoradmin.NewGroupAuthorizer(authorization, s.config.WorkflowAccessGroup)
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
		coreAdapter, err := adminapi.NewCoreAdapter(s.deps.Core.IdentityAdmin(), s.deps.Core.Sessions())
		if err != nil {
			return nil, err
		}
		handler, err := adminapi.NewHandlerWithSessions(coreAdapter, coreAdapter, adminapi.Config{
			MaxJSONBytes: s.config.MaxJSONBytes, SessionCookieSecure: s.config.SessionCookieSecure,
			AllowedOrigins: append([]string(nil), s.config.AllowedOrigins...),
			GroupAccess:    runtimeGroupAccess{s.deps.Core, append([]string(nil), s.config.AccessGroups...)},
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
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
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
	if s == nil || !s.IsReady() || !s.deps.Core.CoordinatorReady() {
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
	Server ServerConfig
	Router routerhttp.Config
}

type PublicServer struct {
	config   PublicConfig
	api      *routerenginepresentation.APIAdapter
	readyFn  func() bool
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
	return &PublicServer{config: config, api: api, readyFn: ready}, nil
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
	server := &http.Server{Handler: guarded, ReadHeaderTimeout: s.config.Server.ReadHeaderTimeout,
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

type coordinatorRuntimeAuthorization struct{ runtime *core.Runtime }

func (a coordinatorRuntimeAuthorization) Authenticate(ctx context.Context, credential string) (core.Session, error) {
	return a.runtime.CoordinatorAuthorization().Authenticate(ctx, credential)
}

func (a coordinatorRuntimeAuthorization) RequireGroupLevel(ctx context.Context, session core.Session, group string, level core.AccessLevel) error {
	return a.runtime.CoordinatorAuthorization().RequireGroupLevel(ctx, session, group, level)
}

func (s *AdminServer) coordinatorSessionAuthorize(ctx context.Context, token string, permission coordinatoradmin.Permission) error {
	session, _, err := s.deps.Core.Sessions().AuthenticateSession(ctx, token)
	if err != nil {
		return err
	}
	level := core.AccessLevelRead
	if permission == coordinatoradmin.RetryTasks {
		level = core.AccessLevelWrite
	} else if permission != coordinatoradmin.ReadWorkflows {
		return core.ErrForbidden
	}
	return s.deps.Core.CoordinatorAuthorization().RequireGroupLevel(ctx, session, s.config.WorkflowAccessGroup, level)
}

type runtimeGroupAccess struct {
	runtime *core.Runtime
	groups  []string
}

func (r runtimeGroupAccess) ResolveGroupAccess(ctx context.Context, token string) (map[string]core.AccessLevel, error) {
	if r.runtime == nil || token == "" {
		return nil, core.ErrUnavailable
	}
	session, _, err := r.runtime.Sessions().AuthenticateSession(ctx, token)
	if err != nil {
		return nil, err
	}
	access := make(map[string]core.AccessLevel)
	for _, group := range r.groups {
		err := r.runtime.GroupAuthorization().RequireGroupLevel(ctx, session, []string{group}, core.AccessLevelWrite)
		if err == nil {
			access[group] = core.AccessLevelWrite
			continue
		}
		if !errors.Is(err, core.ErrForbidden) {
			return nil, err
		}
		err = r.runtime.GroupAuthorization().RequireGroupLevel(ctx, session, []string{group}, core.AccessLevelRead)
		if err == nil {
			access[group] = core.AccessLevelRead
		} else if !errors.Is(err, core.ErrForbidden) {
			return nil, err
		}
	}
	return access, nil
}

func configuredGroupNames(groups map[string]core.AccessLevel) []string {
	names := make([]string, 0, len(groups))
	for group := range groups {
		names = append(names, group)
	}
	sort.Strings(names)
	return names
}

func closedErrors() <-chan error {
	channel := make(chan error)
	close(channel)
	return channel
}
