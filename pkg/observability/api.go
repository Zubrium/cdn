// Package observability provides a small vendor-neutral contract for logs,
// metrics and traces. Two backends implement the same contract: a cheap
// stdlib-only JSONL writer and an OpenTelemetry OTLP exporter.
//
// The package never accepts raw request maps. Attributes are created with the
// typed constructors below, which apply key and value sanitisation and drop
// credential-like values. Cache keys, file/principal IDs, URLs, locales and
// other visitor-derived data are rejected as well. Call sites in this
// repository must not put tokens, cookies, credentials, form values or HTTP
// bodies into telemetry.
package observability

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Mode selects the telemetry backend at process start.
type Mode string

const (
	ModeNoop Mode = "noop"
	ModeFile Mode = "file"
	ModeOTLP Mode = "otel"
)

// Level is a log severity. Unknown levels are normalised to debug.
type Level string

const (
	LevelDebug Level = "debug"
	LevelInfo  Level = "info"
	LevelWarn  Level = "warn"
	LevelError Level = "error"
)

func levelRank(level Level) int {
	switch level {
	case LevelInfo:
		return 1
	case LevelWarn:
		return 2
	case LevelError:
		return 3
	default:
		return 0
	}
}

// ModuleKind identifies the architectural layer that emitted a signal.
type ModuleKind string

const (
	ModuleKindCore         ModuleKind = "core"
	ModuleKindEngine       ModuleKind = "engine"
	ModuleKindWorker       ModuleKind = "worker"
	ModuleKindPresentation ModuleKind = "presentation"
	ModuleKindComposition  ModuleKind = "composition"
	ModuleKindBus          ModuleKind = "bus"
)

// Identity is the mandatory attribution attached to every signal. Base fields
// are supplied once by the composition root; module and component fields are
// supplied at each boundary.
type Identity struct {
	ServiceName    string
	ServiceVersion string
	InstanceID     string
	Profile        string
	BranchID       string
	ModuleKind     ModuleKind
	ModuleID       string
	ComponentID    string
	OperationID    string
}

// Config describes a telemetry backend. It is deliberately free of YAML tags:
// main/internal/config owns the product configuration format and maps into it.
type Config struct {
	Mode           Mode
	Directory      string
	MaxFileBytes   int64
	MaxFiles       int
	Endpoint       string
	Headers        map[string]string
	Insecure       bool
	Timeout        time.Duration
	MinLevel       Level
	ServiceName    string
	ServiceVersion string
	InstanceID     string
	Profile        string
	BranchID       string
	MetricFlush    time.Duration
}

// Validate rejects incomplete or ambiguous backend configuration.
func (c Config) Validate() error {
	switch c.Mode {
	case ModeNoop:
		return nil
	case ModeFile:
		if strings.TrimSpace(c.Directory) == "" {
			return errors.New("observability.file.directory is required")
		}
		if c.MaxFileBytes < 1024 || c.MaxFileBytes > 1<<30 {
			return errors.New("observability.file.max_file_bytes must be between 1024 and 1073741824")
		}
		if c.MaxFiles < 1 || c.MaxFiles > 16 {
			return errors.New("observability.file.max_files must be between 1 and 16")
		}
	case ModeOTLP:
		if strings.TrimSpace(c.Endpoint) == "" {
			return errors.New("observability.otlp.endpoint is required")
		}
		if c.Timeout < time.Millisecond || c.Timeout > time.Minute {
			return errors.New("observability.otlp.timeout is out of range")
		}
	default:
		return fmt.Errorf("unknown observability mode %q", c.Mode)
	}
	if c.MinLevel != "" {
		if levelRank(c.MinLevel) == 0 && c.MinLevel != LevelDebug {
			return fmt.Errorf("unknown observability level %q", c.MinLevel)
		}
	}
	return nil
}

// AttributeKind describes the concrete value kind of an Attribute.
type AttributeKind uint8

const (
	attributeString AttributeKind = iota + 1
	attributeInt
	attributeFloat
	attributeBool
)

// Attribute is a sanitised key/value pair. Use the constructors instead of
// building this struct directly.
type Attribute struct {
	Key   string
	Kind  AttributeKind
	Text  string
	Int   int64
	Float float64
	Bool  bool
}

// Value returns the sanitised value as an ordinary Go value.
func (a Attribute) Value() any {
	switch a.Kind {
	case attributeInt:
		return a.Int
	case attributeFloat:
		return a.Float
	case attributeBool:
		return a.Bool
	default:
		return a.Text
	}
}

var sensitiveKeys = map[string]struct{}{
	"authorization": {}, "cookie": {}, "set-cookie": {}, "token": {},
	"password": {}, "secret": {}, "credential": {}, "session": {},
	"body": {}, "payload": {}, "values": {}, "query": {}, "form": {},
}

// sensitiveFragments also rejects derived keys such as user.token or
// request-body, which a plain exact-match table would let through. The cache
// group comes from SPEC-caching: keys, file/principal IDs, URLs, locale and
// user-agent are forbidden in both logs and metric labels.
var sensitiveFragments = []string{
	"authorization", "cookie", "token", "password", "passwd", "secret",
	"credential", "session", "body", "payload", "values", "query", "form",
	"response", "apikey", "privatekey", "cachekey", "fileid", "principalid",
	"url", "uri", "locale", "useragent", "email", "phone", "username",
}

var (
	bearerPattern         = regexp.MustCompile(`(?i)\b(bearer|basic)\s+[a-z0-9._~+/=-]{6,}`)
	jwtPattern            = regexp.MustCompile(`(?i)\beyJ[a-z0-9_-]{4,}\.[a-z0-9_-]{4,}\.[a-z0-9_-]{4,}\b`)
	credentialPairPattern = regexp.MustCompile(
		`(?i)\b(authorization|cookie|set-cookie|token|access[_-]?token|refresh[_-]?token|api[_-]?key|secret|password|passwd|credential|session(?:[_-]?id)?|private[_-]?key)\s*[:=]\s*("[^"]*"|'[^']*'|[^\s,;]+)`)
	cookieHeaderPattern = regexp.MustCompile(`(?i)\b(cookie|set-cookie)\s*:\s*[^\r\n]+`)
	emailPattern        = regexp.MustCompile(`(?i)\b[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}\b`)
)

const maxAttributeValue = 256

func isSensitiveKey(key string) bool {
	if _, found := sensitiveKeys[key]; found {
		return true
	}
	normalised := strings.NewReplacer(".", "", "_", "", "-", "").Replace(key)
	for _, fragment := range sensitiveFragments {
		if strings.Contains(normalised, fragment) {
			return true
		}
	}
	return false
}

// redactValue removes credential-shaped substrings even when a caller uses an
// otherwise safe key (for example an error message echoed from a dependency).
func redactValue(value string) string {
	value = cookieHeaderPattern.ReplaceAllString(value, "$1: [REDACTED]")
	value = bearerPattern.ReplaceAllString(value, "$1 [REDACTED]")
	value = credentialPairPattern.ReplaceAllString(value, "$1=[REDACTED]")
	value = jwtPattern.ReplaceAllString(value, "[REDACTED]")
	// A safe key does not make visitor PII safe: e-mail addresses are removed
	// from values just like credential-shaped substrings.
	return emailPattern.ReplaceAllString(value, "[REDACTED]")
}

func sanitiseKey(key string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(key)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
			b.WriteRune(r)
			continue
		}
		if r == ' ' {
			b.WriteByte('_')
		}
	}
	result := b.String()
	if len(result) > 96 {
		result = result[:96]
	}
	return result
}

func sanitiseValue(value string) string {
	value = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, strings.TrimSpace(value))
	value = strings.TrimSpace(redactValue(value))
	if len(value) > maxAttributeValue {
		return value[:maxAttributeValue] + "…"
	}
	return value
}

func attribute(key, value string) Attribute {
	cleanKey := sanitiseKey(key)
	if cleanKey == "" || isSensitiveKey(cleanKey) {
		return Attribute{}
	}
	return Attribute{Key: cleanKey, Kind: attributeString, Text: sanitiseValue(value)}
}

// String creates a string attribute. Sensitive keys and values are redacted.
func String(key, value string) Attribute { return attribute(key, value) }

// Int creates an integer attribute.
func Int(key string, value int64) Attribute {
	key = sanitiseKey(key)
	if key == "" || isSensitiveKey(key) {
		return Attribute{}
	}
	return Attribute{Key: key, Kind: attributeInt, Int: value}
}

// Float creates a floating-point attribute.
func Float(key string, value float64) Attribute {
	key = sanitiseKey(key)
	if key == "" || isSensitiveKey(key) {
		return Attribute{}
	}
	return Attribute{Key: key, Kind: attributeFloat, Float: value}
}

// Bool creates a boolean attribute.
func Bool(key string, value bool) Attribute {
	key = sanitiseKey(key)
	if key == "" || isSensitiveKey(key) {
		return Attribute{}
	}
	return Attribute{Key: key, Kind: attributeBool, Bool: value}
}

func nonEmpty(attrs []Attribute) []Attribute {
	result := make([]Attribute, 0, len(attrs))
	for _, item := range attrs {
		if item.Key != "" {
			result = append(result, item)
		}
	}
	return result
}

func sortedAttributes(attrs []Attribute) []Attribute {
	result := append([]Attribute(nil), attrs...)
	sort.Slice(result, func(i, j int) bool { return result[i].Key < result[j].Key })
	return result
}

func attributesMap(attrs []Attribute) map[string]any {
	result := make(map[string]any, len(attrs))
	for _, item := range attrs {
		result[item.Key] = item.Value()
	}
	return result
}

// TraceContext is the W3C trace context carried in HTTP headers and in the
// process-local context. File mode generates and propagates it with stdlib.
type TraceContext struct {
	TraceID   string `json:"trace_id,omitempty"`
	SpanID    string `json:"span_id,omitempty"`
	ParentID  string `json:"parent_span_id,omitempty"`
	Sampled   bool   `json:"sampled,omitempty"`
	Generated bool   `json:"generated,omitempty"`
}

const traceparentHeader = "Traceparent"

// ExtractTrace reads a W3C traceparent header. Malformed or absent headers
// yield an invalid context; Start will allocate fresh identifiers.
func ExtractTrace(headers http.Header) TraceContext {
	raw := strings.TrimSpace(headers.Get(traceparentHeader))
	parts := strings.Split(raw, "-")
	if len(parts) != 4 || parts[0] != "00" || len(parts[1]) != 32 || len(parts[2]) != 16 || len(parts[3]) != 2 {
		return TraceContext{}
	}
	if _, err := hex.DecodeString(parts[1]); err != nil {
		return TraceContext{}
	}
	if _, err := hex.DecodeString(parts[2]); err != nil {
		return TraceContext{}
	}
	if parts[1] == strings.Repeat("0", 32) || parts[2] == strings.Repeat("0", 16) {
		return TraceContext{}
	}
	flag, err := strconv.ParseUint(parts[3], 16, 8)
	if err != nil {
		return TraceContext{}
	}
	return TraceContext{TraceID: parts[1], SpanID: parts[2], Sampled: flag&1 == 1}
}

// Inject writes a W3C traceparent header for downstream HTTP calls.
func (t TraceContext) Inject(headers http.Header) {
	if t.TraceID == "" || t.SpanID == "" {
		return
	}
	flags := "00"
	if t.Sampled {
		flags = "01"
	}
	headers.Set(traceparentHeader, "00-"+t.TraceID+"-"+t.SpanID+"-"+flags)
}

func newTraceContext(parent TraceContext) TraceContext {
	spanID, err := randomHex(8)
	if err != nil {
		spanID = strings.Repeat("0", 16)
	}
	trace := TraceContext{SpanID: spanID, Sampled: true, Generated: true}
	if parent.TraceID != "" {
		trace.TraceID = parent.TraceID
		trace.ParentID = parent.SpanID
		trace.Sampled = parent.Sampled
		return trace
	}
	traceID, err := randomHex(16)
	if err != nil {
		traceID = strings.Repeat("0", 32)
	}
	trace.TraceID = traceID
	return trace
}

func randomHex(size int) (string, error) {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

type contextKey uint8

const (
	identityKey contextKey = iota
	spanKey
)

// WithIdentity binds a module/component identity to the context. Every nested
// log, span and metric inherits it unless explicitly overridden.
func (o *Observer) WithIdentity(ctx context.Context, identity Identity) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	merged := o.base
	if current, ok := ctx.Value(identityKey).(Identity); ok {
		merged = current
	}
	if identity.ModuleKind != "" {
		merged.ModuleKind = identity.ModuleKind
	}
	if identity.ModuleID != "" {
		merged.ModuleID = identity.ModuleID
	}
	if identity.ComponentID != "" {
		merged.ComponentID = identity.ComponentID
	}
	if identity.OperationID != "" {
		merged.OperationID = identity.OperationID
	}
	return context.WithValue(ctx, identityKey, merged)
}

// CurrentIdentity returns the identity bound to ctx, or the service identity.
func (o *Observer) CurrentIdentity(ctx context.Context) Identity {
	if ctx != nil {
		if identity, ok := ctx.Value(identityKey).(Identity); ok {
			return identity
		}
	}
	return o.base
}

// CurrentTrace returns the active span context, if any.
func (o *Observer) CurrentTrace(ctx context.Context) TraceContext { return traceFromContext(ctx) }

func traceFromContext(ctx context.Context) TraceContext {
	if ctx != nil {
		if trace, ok := ctx.Value(spanKey).(TraceContext); ok {
			return trace
		}
	}
	return TraceContext{}
}

type contextSpan struct {
	trace      TraceContext
	traceName  string
	traceAttrs []Attribute
	backendEnd func(error)
	observer   *Observer
	ended      bool
}

// End closes a span and records the outcome once.
func (s *contextSpan) End(err error) {
	if s == nil || s.ended {
		return
	}
	s.ended = true
	status := "ok"
	attrs := s.traceAttrs
	if err != nil {
		status = "error"
		attrs = append(append([]Attribute(nil), attrs...), String("error.type", errorClass(err)), String("error.message", safeError(err)))
	}
	attrs = append(attrs, String("span.status", status))
	s.observer.backend.LogSpan(s.trace, s.observer.CurrentIdentity(context.Background()), s.traceName, attrs)
	if s.backendEnd != nil {
		s.backendEnd(err)
	}
}

// Span is a request or operation scope.
type Span interface {
	End(error)
	Add(...Attribute)
	Trace() TraceContext
}

type spanHandle struct {
	state *contextSpan
}

func (s *spanHandle) End(err error) { s.state.End(err) }

func (s *spanHandle) Add(attrs ...Attribute) {
	if s != nil && !s.state.ended {
		s.state.traceAttrs = append(s.state.traceAttrs, nonEmpty(attrs)...)
	}
}

func (s *spanHandle) Trace() TraceContext { return s.state.trace }

func errorClass(err error) string {
	if err == nil {
		return ""
	}
	name := strings.TrimSpace(err.Error())
	if index := strings.IndexByte(name, ':'); index > 0 {
		name = name[:index]
	}
	if len(name) > 64 {
		name = name[:64]
	}
	return sanitiseValue(name)
}

func safeError(err error) string {
	if err == nil {
		return ""
	}
	return sanitiseValue(err.Error())
}
