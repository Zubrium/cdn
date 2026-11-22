package observability

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	otelattr "go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	otelLog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

const otlpInstrumentationName = "github.com/Zubrium/cdn/pkg/observability"

func init() {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))
}

// otlpBackend exports all three signals through the OpenTelemetry SDK. The
// Observer remains responsible for sanitisation; this type only translates the
// already sanitised contract values into SDK records.
type otlpBackend struct {
	logger         otelLog.Logger
	loggerProvider *sdklog.LoggerProvider
	meterProvider  *sdkmetric.MeterProvider
	tracerProvider *sdktrace.TracerProvider

	tracer   trace.Tracer
	meter    metric.Meter
	timeouts time.Duration

	mu         sync.Mutex
	counters   map[string]metric.Float64Counter
	histograms map[string]metric.Float64Histogram
	gauges     map[string]metric.Float64Gauge
}

var _ backend = (*otlpBackend)(nil)

func newOTLPBackend(cfg Config, base Identity) (backend, error) {
	if strings.TrimSpace(cfg.Endpoint) == "" {
		return nil, errors.New("observability.otlp.endpoint is required")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 5 * time.Second
	}
	if cfg.MetricFlush <= 0 {
		cfg.MetricFlush = 30 * time.Second
	}
	// The exporter keeps the map, so do not let the caller mutate it later.
	// Its values are never written to logs or error messages.
	headers := make(map[string]string, len(cfg.Headers))
	for key, value := range cfg.Headers {
		headers[key] = value
	}
	cfg.Headers = headers

	endpoint := strings.TrimRight(strings.TrimSpace(cfg.Endpoint), "/")
	ctx := context.Background()
	res := resource.NewSchemaless(otlpResourceAttributes(base)...)

	logExporter, err := otlploghttp.New(ctx, otlpLogOptions(endpoint, cfg)...)
	if err != nil {
		return nil, fmt.Errorf("otlp log exporter: %w", err)
	}
	traceExporter, err := otlptracehttp.New(ctx, otlpTraceOptions(endpoint, cfg)...)
	if err != nil {
		_ = logExporter.Shutdown(ctx)
		return nil, fmt.Errorf("otlp trace exporter: %w", err)
	}
	metricExporter, err := otlpmetrichttp.New(ctx, otlpMetricOptions(endpoint, cfg)...)
	if err != nil {
		_ = logExporter.Shutdown(ctx)
		_ = traceExporter.Shutdown(ctx)
		return nil, fmt.Errorf("otlp metric exporter: %w", err)
	}

	loggerProvider := sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewBatchProcessor(logExporter,
			sdklog.WithExportTimeout(cfg.Timeout))),
		sdklog.WithResource(res),
	)
	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExporter,
			sdktrace.WithBatchTimeout(time.Second),
			sdktrace.WithExportTimeout(cfg.Timeout)),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.AlwaysSample())),
		sdktrace.WithResource(res),
	)
	reader := sdkmetric.NewPeriodicReader(metricExporter,
		sdkmetric.WithInterval(cfg.MetricFlush),
		sdkmetric.WithTimeout(cfg.Timeout))
	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(reader),
		sdkmetric.WithResource(res),
	)

	if globalTracerProviderUnconfigured() {
		otel.SetTracerProvider(tracerProvider)
	}

	return &otlpBackend{
		logger:         loggerProvider.Logger(otlpInstrumentationName),
		loggerProvider: loggerProvider,
		meterProvider:  meterProvider,
		tracerProvider: tracerProvider,
		tracer:         tracerProvider.Tracer(otlpInstrumentationName),
		meter:          meterProvider.Meter(otlpInstrumentationName),
		timeouts:       cfg.Timeout,
		counters:       make(map[string]metric.Float64Counter),
		histograms:     make(map[string]metric.Float64Histogram),
		gauges:         make(map[string]metric.Float64Gauge),
	}, nil
}

// globalTracerProviderUnconfigured reports whether the global provider is
// still the OTel delegating no-op provider. A provider installed by the host
// application must not be replaced by this package.
func globalTracerProviderUnconfigured() bool {
	provider := otel.GetTracerProvider()
	if provider == nil {
		return true
	}
	kind := reflect.TypeOf(provider)
	for kind.Kind() == reflect.Pointer {
		kind = kind.Elem()
	}
	return kind.PkgPath() == "go.opentelemetry.io/otel/internal/global" && kind.Name() == "tracerProvider"
}

func otlpLogOptions(endpoint string, cfg Config) []otlploghttp.Option {
	opts := []otlploghttp.Option{
		otlploghttp.WithEndpointURL(endpoint + "/v1/logs"),
		otlploghttp.WithTimeout(cfg.Timeout),
	}
	if len(cfg.Headers) > 0 {
		opts = append(opts, otlploghttp.WithHeaders(cfg.Headers))
	}
	if cfg.Insecure {
		opts = append(opts, otlploghttp.WithInsecure())
	}
	return opts
}

func otlpTraceOptions(endpoint string, cfg Config) []otlptracehttp.Option {
	opts := []otlptracehttp.Option{
		otlptracehttp.WithEndpointURL(endpoint + "/v1/traces"),
		otlptracehttp.WithTimeout(cfg.Timeout),
	}
	if len(cfg.Headers) > 0 {
		opts = append(opts, otlptracehttp.WithHeaders(cfg.Headers))
	}
	if cfg.Insecure {
		opts = append(opts, otlptracehttp.WithInsecure())
	}
	return opts
}

func otlpMetricOptions(endpoint string, cfg Config) []otlpmetrichttp.Option {
	opts := []otlpmetrichttp.Option{
		otlpmetrichttp.WithEndpointURL(endpoint + "/v1/metrics"),
		otlpmetrichttp.WithTimeout(cfg.Timeout),
	}
	if len(cfg.Headers) > 0 {
		opts = append(opts, otlpmetrichttp.WithHeaders(cfg.Headers))
	}
	if cfg.Insecure {
		opts = append(opts, otlpmetrichttp.WithInsecure())
	}
	return opts
}

func otlpResourceAttributes(base Identity) []otelattr.KeyValue {
	values := []struct{ key, value string }{
		{"service.name", base.ServiceName},
		{"service.version", base.ServiceVersion},
		{"service.instance.id", base.InstanceID},
		{"deployment.profile", base.Profile},
		{"router.branch", base.BranchID},
	}
	attrs := make([]otelattr.KeyValue, 0, len(values))
	for _, item := range values {
		if strings.TrimSpace(item.value) != "" {
			attrs = append(attrs, otelattr.String(item.key, item.value))
		}
	}
	return attrs
}

func otlpAttributes(attrs []Attribute) []otelattr.KeyValue {
	out := make([]otelattr.KeyValue, 0, len(attrs))
	for _, item := range attrs {
		if item.Key == "" {
			continue
		}
		switch value := item.Value().(type) {
		case int64:
			out = append(out, otelattr.Int64(item.Key, value))
		case float64:
			out = append(out, otelattr.Float64(item.Key, value))
		case bool:
			out = append(out, otelattr.Bool(item.Key, value))
		case string:
			out = append(out, otelattr.String(item.Key, value))
		}
	}
	return out
}

func otlpSeverity(level Level) otelLog.Severity {
	switch level {
	case LevelInfo:
		return otelLog.SeverityInfo
	case LevelWarn:
		return otelLog.SeverityWarn
	case LevelError:
		return otelLog.SeverityError
	default:
		return otelLog.SeverityDebug
	}
}

func otlpSpanContext(value TraceContext) (trace.SpanContext, bool) {
	if value.TraceID == "" || value.SpanID == "" {
		return trace.SpanContext{}, false
	}
	traceID, err := trace.TraceIDFromHex(value.TraceID)
	if err != nil || !traceID.IsValid() {
		return trace.SpanContext{}, false
	}
	spanID, err := trace.SpanIDFromHex(value.SpanID)
	if err != nil || !spanID.IsValid() {
		return trace.SpanContext{}, false
	}
	flags := trace.TraceFlags(0)
	if value.Sampled {
		flags = trace.FlagsSampled
	}
	return trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: flags,
	}), true
}

func (b *otlpBackend) Log(ctx context.Context, _ Identity, level Level, message string, attrs []Attribute, value TraceContext) {
	if ctx == nil {
		ctx = context.Background()
	}
	if spanContext, ok := otlpSpanContext(value); ok {
		ctx = trace.ContextWithSpanContext(ctx, spanContext)
	}
	var record otelLog.Record
	record.SetTimestamp(time.Now())
	record.SetSeverity(otlpSeverity(level))
	record.SetSeverityText(strings.ToUpper(string(level)))
	record.SetBody(otelattr.StringValue(message))
	record.AddAttributes(otlpAttributes(attrs)...)
	b.logger.Emit(ctx, record)
}

// LogSpan is intentionally empty: the SDK trace exporter sends span records.
func (b *otlpBackend) LogSpan(TraceContext, Identity, string, []Attribute) {}

func (b *otlpBackend) Start(ctx context.Context, identity Identity, operation string, attrs []Attribute, parent TraceContext) (context.Context, TraceContext, func(error)) {
	if ctx == nil {
		ctx = context.Background()
	}
	startCtx := ctx
	parentContext, hasParent := otlpSpanContext(parent)
	if hasParent {
		startCtx = trace.ContextWithRemoteSpanContext(ctx, parentContext)
	}
	spanAttrs := otlpAttributes(append(identityAttributes(identity), nonEmpty(attrs)...))
	ctx, span := b.tracer.Start(startCtx, operation, trace.WithAttributes(spanAttrs...))

	spanContext := span.SpanContext()
	result := TraceContext{
		TraceID:   spanContext.TraceID().String(),
		SpanID:    spanContext.SpanID().String(),
		Sampled:   spanContext.IsSampled(),
		Generated: true,
	}
	if hasParent {
		// Always keep the incoming trace ID, even if the sampler changed.
		result.TraceID = parent.TraceID
		result.ParentID = parent.SpanID
	}

	var once sync.Once
	end := func(err error) {
		once.Do(func() {
			if err != nil {
				span.SetStatus(codes.Error, safeError(err))
				span.SetAttributes(
					otelattr.String("error.type", errorClass(err)),
					otelattr.String("error.message", safeError(err)),
					otelattr.String("span.status", "error"),
				)
			} else {
				span.SetAttributes(otelattr.String("span.status", "ok"))
			}
			span.End()
		})
	}
	return ctx, result, end
}

func (b *otlpBackend) Counter(ctx context.Context, name string, attrs []Attribute, value float64) {
	b.mu.Lock()
	counter, ok := b.counters[name]
	if !ok {
		created, err := b.meter.Float64Counter(name)
		if err != nil {
			b.mu.Unlock()
			otel.Handle(err)
			return
		}
		counter = created
		b.counters[name] = counter
	}
	b.mu.Unlock()
	// The active span context in ctx lets the SDK attach trace-based exemplars
	// without adding high-cardinality trace attributes to the metric series.
	counter.Add(ctx, value, metric.WithAttributes(otlpAttributes(attrs)...))
}

func (b *otlpBackend) Histogram(ctx context.Context, name string, attrs []Attribute, value float64) {
	b.mu.Lock()
	histogram, ok := b.histograms[name]
	if !ok {
		created, err := b.meter.Float64Histogram(name)
		if err != nil {
			b.mu.Unlock()
			otel.Handle(err)
			return
		}
		histogram = created
		b.histograms[name] = histogram
	}
	b.mu.Unlock()
	histogram.Record(ctx, value, metric.WithAttributes(otlpAttributes(attrs)...))
}

func (b *otlpBackend) Gauge(ctx context.Context, name string, attrs []Attribute, value float64) {
	b.mu.Lock()
	gauge, ok := b.gauges[name]
	if !ok {
		created, err := b.meter.Float64Gauge(name)
		if err != nil {
			b.mu.Unlock()
			otel.Handle(err)
			return
		}
		gauge = created
		b.gauges[name] = gauge
	}
	b.mu.Unlock()
	gauge.Record(ctx, value, metric.WithAttributes(otlpAttributes(attrs)...))
}

type otlpLifecycle interface {
	ForceFlush(context.Context) error
	Shutdown(context.Context) error
}

func (b *otlpBackend) Close(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	var errs []error
	for _, component := range []otlpLifecycle{b.loggerProvider, b.meterProvider, b.tracerProvider} {
		if component == nil {
			continue
		}
		if err := flushAndShutdown(ctx, b.timeouts, component); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func flushAndShutdown(ctx context.Context, timeout time.Duration, component otlpLifecycle) error {
	var errs []error
	operations := []struct {
		name string
		run  func(context.Context) error
	}{
		{"force flush", component.ForceFlush},
		{"shutdown", component.Shutdown},
	}
	for _, operation := range operations {
		opCtx, cancel := context.WithTimeout(ctx, timeout)
		err := operation.run(opCtx)
		cancel()
		if err != nil {
			errs = append(errs, fmt.Errorf("otlp %s: %w", operation.name, err))
		}
	}
	return errors.Join(errs...)
}
