package observability

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// backend is implemented by the cheap file writer, the OTLP exporter and the
// no-op sink. All signal construction and sanitisation happens in Observer.
type backend interface {
	Log(ctx context.Context, identity Identity, level Level, message string, attrs []Attribute, trace TraceContext)
	LogSpan(trace TraceContext, identity Identity, name string, attrs []Attribute)
	Start(ctx context.Context, identity Identity, operation string, attrs []Attribute, parent TraceContext) (context.Context, TraceContext, func(error))
	Counter(ctx context.Context, name string, attrs []Attribute, value float64)
	Histogram(ctx context.Context, name string, attrs []Attribute, value float64)
	Gauge(ctx context.Context, name string, attrs []Attribute, value float64)
	Close(ctx context.Context) error
}

// Observer is the process-wide telemetry entry point. It is safe for
// concurrent use.
type Observer struct {
	cfg     Config
	base    Identity
	backend backend
	metrics *metricStore
}

func (c Config) withDefaults() Config {
	if c.Mode == "" {
		c.Mode = ModeNoop
	}
	if c.ServiceName == "" {
		c.ServiceName = "zubrium"
	}
	if c.MinLevel == "" {
		c.MinLevel = LevelInfo
	}
	if c.MaxFileBytes == 0 {
		c.MaxFileBytes = 16 << 20
	}
	if c.MaxFiles == 0 {
		c.MaxFiles = 4
	}
	if c.Timeout == 0 {
		c.Timeout = 5 * time.Second
	}
	if c.MetricFlush == 0 {
		c.MetricFlush = 30 * time.Second
	}
	return c
}

// New builds an Observer from a validated configuration.
func New(cfg Config) (*Observer, error) {
	cfg = cfg.withDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	observer := &Observer{
		cfg: cfg,
		base: Identity{
			ServiceName: cfg.ServiceName, ServiceVersion: cfg.ServiceVersion,
			InstanceID: cfg.InstanceID, Profile: cfg.Profile, BranchID: cfg.BranchID,
		},
		metrics: newMetricStore(),
	}
	var err error
	switch cfg.Mode {
	case ModeFile:
		observer.backend, err = newFileBackend(cfg.Directory, cfg.MaxFileBytes, cfg.MaxFiles)
	case ModeOTLP:
		observer.backend, err = newOTLPBackend(cfg, observer.base)
	default:
		observer.backend = noopBackend{}
	}
	if err != nil {
		return nil, fmt.Errorf("observability %s backend: %w", cfg.Mode, err)
	}
	return observer, nil
}

// Close flushes and releases backend resources.
func (o *Observer) Close(ctx context.Context) error {
	if o == nil || o.backend == nil {
		return nil
	}
	return o.backend.Close(ctx)
}

// MetricsHandler serves a Prometheus-compatible text snapshot of the local
// metric aggregation. It is intended for local debugging in both backends.
func (o *Observer) MetricsHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(o.metrics.render()))
	})
}

// identityAttributes returns the low-cardinality mandatory attribution.
func identityAttributes(identity Identity) []Attribute {
	attrs := []Attribute{
		String("service.name", identity.ServiceName),
		String("service.version", identity.ServiceVersion),
		String("service.instance.id", identity.InstanceID),
		String("deployment.profile", identity.Profile),
		String("router.branch", identity.BranchID),
		String("module.kind", string(identity.ModuleKind)),
		String("module.id", identity.ModuleID),
		String("component.id", identity.ComponentID),
		String("operation.id", identity.OperationID),
	}
	return nonEmpty(attrs)
}

func traceAttributes(trace TraceContext) []Attribute {
	return nonEmpty([]Attribute{
		String("trace_id", trace.TraceID),
		String("span_id", trace.SpanID),
		String("parent_span_id", trace.ParentID),
	})
}

func (o *Observer) signalAttributes(ctx context.Context) []Attribute {
	attrs := identityAttributes(o.CurrentIdentity(ctx))
	attrs = append(attrs, traceAttributes(o.CurrentTrace(ctx))...)
	return attrs
}

// Log emits one structured log record. Raw request data must never be passed
// as message or attributes.
func (o *Observer) Log(ctx context.Context, level Level, message string, attrs ...Attribute) {
	if o == nil || o.backend == nil {
		return
	}
	if levelRank(level) < levelRank(o.cfg.MinLevel) {
		return
	}
	all := append(o.signalAttributes(ctx), nonEmpty(attrs)...)
	o.backend.Log(ctx, o.CurrentIdentity(ctx), level, sanitiseValue(message), all, o.CurrentTrace(ctx))
}

// Start opens a span, propagating the active context. The returned context
// must be used by all nested calls.
func (o *Observer) Start(ctx context.Context, name string, attrs ...Attribute) (context.Context, Span) {
	if ctx == nil {
		ctx = context.Background()
	}
	parent := o.CurrentTrace(ctx)
	identity := o.CurrentIdentity(ctx)
	ctx, trace, end := o.backend.Start(ctx, identity, name, nonEmpty(attrs), parent)
	if trace.TraceID == "" || trace.SpanID == "" {
		trace = newTraceContext(parent)
	}
	ctx = withTrace(ctx, trace)
	state := &contextSpan{trace: trace, traceName: sanitiseValue(name), backendEnd: end, observer: o}
	state.traceAttrs = append(identityAttributes(identity), nonEmpty(attrs)...)
	state.traceAttrs = append(state.traceAttrs, traceAttributes(trace)...)
	return ctx, &spanHandle{state: state}
}

func withTrace(ctx context.Context, trace TraceContext) context.Context {
	return context.WithValue(ctx, spanKey, trace)
}

// Operation wraps a module action with an identity, span, log record and
// duration metric. It never swallows the underlying error.
func (o *Observer) Operation(ctx context.Context, identity Identity, name string, attrs []Attribute, fn func(context.Context) error) error {
	ctx = o.WithIdentity(ctx, identity)
	ctx, span := o.Start(ctx, name, attrs...)
	started := time.Now()
	err := fn(ctx)
	elapsed := time.Since(started)
	span.End(err)

	// Metric labels stay low-cardinality: caller attributes may contain task or
	// workflow IDs and belong on the span/log record, not on a metric series.
	metricLabels := append(identityAttributes(identity), String("outcome", outcome(err)))
	o.IncCounter(ctx, "zubrium_operation_total", 1, metricLabels...)
	o.ObserveHistogram(ctx, "zubrium_operation_duration_seconds", elapsed.Seconds(), metricLabels...)

	logAttrs := append(append([]Attribute(nil), metricLabels...), nonEmpty(attrs)...)
	logAttrs = append(logAttrs, Float("duration_ms", float64(elapsed.Microseconds())/1000))
	if err != nil {
		logAttrs = append(logAttrs, String("error.type", errorClass(err)), String("error.message", safeError(err)))
		o.Log(ctx, LevelError, "operation failed", logAttrs...)
	} else {
		o.Log(ctx, LevelDebug, "operation completed", logAttrs...)
	}
	return err
}

func outcome(err error) string {
	if err != nil {
		return "error"
	}
	return "ok"
}

// withTelemetryIdentity fills missing module attribution from ctx. Explicit
// caller attributes win, so a trusted boundary can override inherited identity.
func (o *Observer) withTelemetryIdentity(ctx context.Context, attrs []Attribute) []Attribute {
	keys := make(map[string]struct{}, len(attrs))
	for _, attr := range attrs {
		keys[attr.Key] = struct{}{}
	}
	result := append([]Attribute(nil), attrs...)
	for _, attr := range identityAttributes(o.CurrentIdentity(ctx)) {
		if _, exists := keys[attr.Key]; !exists {
			result = append(result, attr)
			keys[attr.Key] = struct{}{}
		}
	}
	return result
}

// IncCounter increments a counter in the local snapshot and the backend.
func (o *Observer) IncCounter(ctx context.Context, name string, value float64, attrs ...Attribute) {
	if o == nil {
		return
	}
	clean := sanitiseKey(name)
	if clean == "" {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	all := o.withTelemetryIdentity(ctx, nonEmpty(attrs))
	o.metrics.inc(clean, sortedAttributes(all), value)
	o.backend.Counter(ctx, clean, all, value)
}

// IncCounterExact emits a counter with exactly the supplied attributes. It is
// reserved for fixed contract series whose label set is defined by SPEC;
// inherited telemetry identity is not appended.
func (o *Observer) IncCounterExact(ctx context.Context, name string, value float64, attrs ...Attribute) {
	if o == nil {
		return
	}
	clean := sanitiseKey(name)
	if clean == "" {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	all := nonEmpty(attrs)
	o.metrics.inc(clean, sortedAttributes(all), value)
	o.backend.Counter(ctx, clean, all, value)
}

// SetGaugeExact emits a gauge with exactly the supplied attributes.
func (o *Observer) SetGaugeExact(ctx context.Context, name string, value float64, attrs ...Attribute) {
	if o == nil {
		return
	}
	clean := sanitiseKey(name)
	if clean == "" {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	all := nonEmpty(attrs)
	o.metrics.setGauge(clean, sortedAttributes(all), value)
	o.backend.Gauge(ctx, clean, all, value)
}

// ObserveHistogramExact emits a histogram with exactly the supplied attributes.
func (o *Observer) ObserveHistogramExact(ctx context.Context, name string, value float64, attrs ...Attribute) {
	if o == nil {
		return
	}
	clean := sanitiseKey(name)
	if clean == "" {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	all := nonEmpty(attrs)
	o.metrics.observe(clean, sortedAttributes(all), value)
	o.backend.Histogram(ctx, clean, all, value)
}

// ObserveHistogram records a duration or size observation in both sinks.
func (o *Observer) ObserveHistogram(ctx context.Context, name string, value float64, attrs ...Attribute) {
	if o == nil {
		return
	}
	clean := sanitiseKey(name)
	if clean == "" {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	all := o.withTelemetryIdentity(ctx, nonEmpty(attrs))
	o.metrics.observe(clean, sortedAttributes(all), value)
	o.backend.Histogram(ctx, clean, all, value)
}

// SetGauge records an instantaneous value such as cache entries or bytes.
// Repeated writes for the same label set replace the previous value.
func (o *Observer) SetGauge(ctx context.Context, name string, value float64, attrs ...Attribute) {
	if o == nil {
		return
	}
	clean := sanitiseKey(name)
	if clean == "" {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	all := o.withTelemetryIdentity(ctx, nonEmpty(attrs))
	o.metrics.setGauge(clean, sortedAttributes(all), value)
	o.backend.Gauge(ctx, clean, all, value)
}

type noopBackend struct{}

func (noopBackend) Log(context.Context, Identity, Level, string, []Attribute, TraceContext) {}
func (noopBackend) LogSpan(TraceContext, Identity, string, []Attribute)                     {}
func (noopBackend) Start(ctx context.Context, _ Identity, _ string, _ []Attribute, parent TraceContext) (context.Context, TraceContext, func(error)) {
	return ctx, newTraceContext(parent), nil
}
func (noopBackend) Counter(context.Context, string, []Attribute, float64)   {}
func (noopBackend) Histogram(context.Context, string, []Attribute, float64) {}
func (noopBackend) Gauge(context.Context, string, []Attribute, float64)     {}
func (noopBackend) Close(context.Context) error                             { return nil }

var _ backend = noopBackend{}

type statusRecorder struct {
	http.ResponseWriter
	status int
	mu     sync.Mutex
}

func (w *statusRecorder) WriteHeader(status int) {
	w.mu.Lock()
	if w.status == 0 {
		w.status = status
	}
	w.mu.Unlock()
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusRecorder) Write(body []byte) (int, error) {
	w.mu.Lock()
	if w.status == 0 {
		w.status = http.StatusOK
	}
	w.mu.Unlock()
	return w.ResponseWriter.Write(body)
}

func (w *statusRecorder) Status() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

func (w *statusRecorder) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *statusRecorder) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	return hijacker.Hijack()
}

// Handler is an alias for HTTP, kept for call sites that prefer a noun.
func (o *Observer) Handler(identity Identity, next http.Handler) http.Handler {
	return o.HTTP(identity, next)
}

// HTTP wraps a handler with request attribution, trace propagation, one
// completion log record and request metrics. Request bodies, headers, cookies
// and raw query strings are never inspected.
func (o *Observer) HTTP(identity Identity, next http.Handler) http.Handler {
	if next == nil {
		return nil
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/metrics" {
			next.ServeHTTP(w, r)
			return
		}
		parent := ExtractTrace(r.Header)
		ctx := o.WithIdentity(r.Context(), identity)
		ctx = withTrace(ctx, parent)
		ctx, span := o.Start(ctx, "http.server", String("http.method", r.Method))
		var requestErr error
		defer func() { span.End(requestErr) }()

		trace := span.Trace()
		requestID := trace.SpanID
		w.Header().Set("X-Request-ID", requestID)
		w.Header().Set("X-Trace-ID", trace.TraceID)
		recorder := &statusRecorder{ResponseWriter: w}
		request := r.WithContext(ctx)
		started := time.Now()
		next.ServeHTTP(recorder, request)
		elapsed := time.Since(started)
		// ServeMux fills Pattern on the request it receives, so keep and inspect
		// that copy instead of the original request.
		route := routePattern(request)

		status := recorder.Status()
		if status >= 400 {
			requestErr = fmt.Errorf("http status %d", status)
		}
		labels := []Attribute{
			String("http.method", r.Method),
			String("http.route", route),
			Int("http.status_code", int64(status)),
			String("outcome", outcome(requestErr)),
		}
		span.Add(labels...)
		o.IncCounter(ctx, "zubrium_http_server_requests_total", 1, labels...)
		o.ObserveHistogram(ctx, "zubrium_http_server_duration_seconds", elapsed.Seconds(), labels...)
		if route == "/healthz" || route == "/readyz" || route == "/metrics" {
			return
		}
		level := LevelInfo
		if status >= 500 {
			level = LevelError
		} else if status >= 400 {
			level = LevelWarn
		}
		o.Log(ctx, level, "http request completed", labels...)
	})
}

func routePattern(r *http.Request) string {
	pattern := strings.TrimSpace(r.Pattern)
	if pattern == "" {
		return "unmatched"
	}
	if index := strings.LastIndexByte(pattern, ' '); index >= 0 {
		pattern = pattern[index+1:]
	}
	pattern = sanitiseValue(pattern)
	if pattern == "" || strings.ContainsAny(pattern, "\r\n") {
		return "unmatched"
	}
	return pattern
}
