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

	"github.com/Zubrium/cdn/pkg/observability"
	fileapi "github.com/Zubrium/cdn/presentation/fileapi"
	routerhttp "github.com/Zubrium/cdn/presentation/router"
)

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
	AuthorizeWorkflowSession func(context.Context, string, string) error
	HandlerMounts            func(HandlerContext) ([]HandlerMount, error)
}

type Readiness interface {
	Ready() bool
}

type AdminServer struct {
	config      AdminConfig
	deps        AdminDependencies
	ingress     *ingressLimiter
	contracts   *concurrencyContractPublisher
	mu          sync.RWMutex
	server      *http.Server
	listener    net.Listener
	done        chan struct{}
	failures    chan error
	ready       bool
	application Readiness
}

func NewAdminServer(config AdminConfig, deps AdminDependencies) (*AdminServer, error) {
	if config.Server.Address == "" || deps.CoreReady == nil {
		return nil, errors.New("Admin transport dependencies are incomplete")
	}
	ingress, err := newIngressLimiter(config.Server, config.Observer, "admin")
	if err != nil {
		return nil, err
	}
	publisher := newConcurrencyContractPublisher()
	publisher.publish(config.Observer, "admin")
	return &AdminServer{config: config, deps: deps, ingress: ingress, contracts: publisher}, nil
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
	mounts := make(map[string]http.Handler)
	mount := func(pattern string, handler http.Handler) error {
		if _, exists := mounts[pattern]; exists {
			return fmt.Errorf("duplicate Admin route pattern %s", pattern)
		}
		mounts[pattern] = handler
		return nil
	}
	guard := func(handler http.Handler) http.Handler { return s.requireCoreRuntime(handler) }
	ctx := HandlerContext{AuthorizeWorkflowSession: s.deps.AuthorizeWorkflowSession}
	if s.deps.HandlerMounts != nil {
		declared, err := s.deps.HandlerMounts(ctx)
		if err != nil {
			return nil, err
		}
		for _, declaration := range declared {
			if declaration.Handler == nil || len(declaration.Patterns) == 0 {
				return nil, errors.New("Admin handler mount is incomplete")
			}
			handler := declaration.Handler
			if declaration.Guarded {
				handler = guard(handler)
			}
			if declaration.Synthetic {
				handler = syntheticDataMode(handler)
			}
			for _, pattern := range declaration.Patterns {
				if err := mount(pattern, handler); err != nil {
					return nil, err
				}
			}
		}
	}
	if err := mount("GET /healthz", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if s.config.BuildIdentity != "" {
			w.Header().Set("X-Zubrium-Build-Identity", s.config.BuildIdentity)
		}
		w.WriteHeader(http.StatusNoContent)
	})); err != nil {
		return nil, err
	}
	if err := mount("GET /readyz", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if !s.readyForRequests() {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Retry-After", "1")
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNoContent)
	})); err != nil {
		return nil, err
	}
	if s.config.UIHandler != nil {
		for _, pattern := range []string{"/admin", "/admin/"} {
			if err := mount(pattern, s.config.UIHandler); err != nil {
				return nil, err
			}
		}
	}
	// Exact anchors prevent ServeMux from redirecting subtree roots such as
	// /api/admin/content to /api/admin/content/. Those roots are not routes.
	for _, pattern := range []string{"/api/admin/content", "/api/admin/forms"} {
		if err := mount(pattern, http.NotFoundHandler()); err != nil {
			return nil, err
		}
	}
	// Register in a deterministic order so route ownership is reviewable;
	// ServeMux still resolves by pattern specificity, not by this order.
	patterns := make([]string, 0, len(mounts))
	for pattern := range mounts {
		patterns = append(patterns, pattern)
	}
	sort.Strings(patterns)
	for _, pattern := range patterns {
		mux.Handle(pattern, mounts[pattern])
	}
	// Unknown Admin paths belong to no module. Keep the ownership boundary
	// explicit instead of letting one adapter serve another adapter's namespace.
	mux.Handle("/api/admin/", http.NotFoundHandler())
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
	config    PublicConfig
	api       routerhttp.API
	readyFn   func() bool
	ingress   *ingressLimiter
	contracts *concurrencyContractPublisher
	mu        sync.RWMutex
	server    *http.Server
	listener  net.Listener
	done      chan struct{}
	failures  chan error
	ready     bool
}

func NewPublicServer(config PublicConfig, api routerhttp.API, ready func() bool) (*PublicServer, error) {
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
	publisher := newConcurrencyContractPublisher()
	publisher.publish(config.Observer, "public-router")
	return &PublicServer{config: config, api: api, readyFn: ready, ingress: ingress, contracts: publisher}, nil
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

func closedErrors() <-chan error {
	channel := make(chan error)
	close(channel)
	return channel
}
