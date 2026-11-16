package runtime

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type PrerenderOptions struct {
	NodePath         string
	WorkerPath       string
	PlaywrightModule string
	BrowserPath      string
	Timeout          time.Duration
	AttemptTimeout   time.Duration
	MaxAttempts      int
	QuietInterval    time.Duration
	CacheTTL         time.Duration
	Concurrency      int
	QueueSize        int
	CacheEntries     int
	MaxHTMLBytes     int
	HeapMB           int
	ViewportWidth    int
	ViewportHeight   int
	MaxRequests      int
	MaxNetworkBytes  int64
}

func (c PrerenderOptions) Validate() error {
	if c.NodePath == "" || c.WorkerPath == "" || c.PlaywrightModule == "" || c.BrowserPath == "" {
		return errors.New("prerender executable, worker, Playwright module and browser paths are required")
	}
	if c.Timeout <= 0 || c.AttemptTimeout <= 0 || c.AttemptTimeout > c.Timeout || c.MaxAttempts < 1 || c.QuietInterval <= 0 || c.QuietInterval >= c.AttemptTimeout || c.CacheTTL <= 0 {
		return errors.New("prerender timeout, quiet interval and cache TTL must be positive; quiet interval must be shorter than timeout")
	}
	if c.Concurrency < 1 || c.QueueSize < 1 || c.CacheEntries < 1 || c.MaxHTMLBytes < 1024 || c.HeapMB < 32 || c.ViewportWidth < 320 || c.ViewportHeight < 240 || c.MaxRequests < 1 || c.MaxNetworkBytes < 1024 {
		return errors.New("prerender resource limits and viewport must be explicitly configured and valid")
	}
	return nil
}

type renderedPage struct {
	data       []byte
	expires    time.Time
	lastAccess time.Time
}
type pendingPage struct {
	done      chan struct{}
	data      []byte
	err       error
	errorCode string
}

// prerenderMeasurements contains only bounded operational data. It must never
// retain a route, cache identity, request query, page body, or worker stderr.
type prerenderMeasurements struct {
	cacheResult    string
	missReason     string
	queueWait      time.Duration
	workerDuration time.Duration
	renderDuration time.Duration
	workerTimings  map[string]int
	workerAttempts int
	workerStage    string
	readiness      *prerenderReadiness
	errorCode      string
	htmlBytes      int
}

// prerenderReadiness carries only normalized booleans and bounded element
// counts from the isolated browser. It must never contain page-provided text.
type prerenderReadiness struct {
	snapshotMatch             bool
	revisionMatch             bool
	engineReady               bool
	placements                int
	ready                     int
	errors                    int
	requestFailures           int
	failedResponses           int
	pageErrors                int
	policyBlockedRequests     int
	originBlocks              int
	methodBlocks              int
	pathBlocks                int
	requestLimitBlocks        int
	requestFailureAborted     int
	requestFailureFailed      int
	requestFailureBlocked     int
	requestFailureConnection  int
	requestFailureOther       int
	requestFailurePolicyAbort int
	abortedScripts            int
	abortedStylesheets        int
	abortedFetchXHR           int
	abortedImages             int
	abortedOther              int
}

type workerDiagnostics struct {
	stage     string
	readiness *prerenderReadiness
	timings   map[string]int
}

var readinessDiagnosticPattern = regexp.MustCompile(`^readiness snapshot_match=(true|false) revision_match=(true|false) engine_ready=(true|false) placements=([0-9]{1,5}) ready=([0-9]{1,5}) errors=([0-9]{1,5}) request_failures=([0-9]{1,5}) failed_responses=([0-9]{1,5}) page_errors=([0-9]{1,5}) policy_blocked_requests=([0-9]{1,5}) origin_blocks=([0-9]{1,5}) method_blocks=([0-9]{1,5}) path_blocks=([0-9]{1,5}) request_limit_blocks=([0-9]{1,5}) request_failure_aborted=([0-9]{1,5}) request_failure_failed=([0-9]{1,5}) request_failure_blocked_by_client=([0-9]{1,5}) request_failure_connection=([0-9]{1,5}) request_failure_other=([0-9]{1,5}) request_failure_policy_abort=([0-9]{1,5}) aborted_script=([0-9]{1,5}) aborted_stylesheet=([0-9]{1,5}) aborted_fetch_xhr=([0-9]{1,5}) aborted_image=([0-9]{1,5}) aborted_other=([0-9]{1,5})$`)
var workerTimingDiagnosticPattern = regexp.MustCompile(`^timing phase=(browser_launch|context_creation|navigation|runtime_data|readiness|serialization) ms=([0-9]{1,6})$`)

type Prerenderer struct {
	options PrerenderOptions
	baseURL string
	nonce   string
	slots   chan struct{}
	queue   chan struct{}
	mu      sync.Mutex
	cache   map[string]renderedPage
	pending map[string]*pendingPage
	logger  *slog.Logger
}

// SetLogger is called during startup before requests are served.
func (p *Prerenderer) SetLogger(logger *slog.Logger) { p.logger = logger }

func (p *Prerenderer) logDocumentResolveDuration(ctx context.Context, duration time.Duration) {
	if p == nil || p.logger == nil || duration < 0 {
		return
	}
	p.logger.LogAttrs(ctx, slog.LevelDebug, "prerender phase timing",
		slog.String("phase", "document_resolve"),
		slog.Int64("duration_ms", int64(duration/time.Millisecond)),
	)
}

// parseWorkerDiagnostics accepts fixed worker milestones, bounded phase names,
// and normalized readiness data only. Arbitrary stderr can contain page data
// or credentials.
func parseWorkerDiagnostics(diagnostic string) workerDiagnostics {
	result := workerDiagnostics{stage: "startup"}
	for _, line := range strings.Split(diagnostic, "\n") {
		switch line {
		case "stage=module", "stage=browser", "stage=context", "stage=navigation", "stage=navigation_status_mismatch", "stage=readiness_wait", "stage=readiness_budget_exhausted", "stage=readiness_timeout", "stage=ready", "stage=settled", "stage=serialized":
			result.stage = strings.TrimPrefix(line, "stage=")
		}
		if match := workerTimingDiagnosticPattern.FindStringSubmatch(line); len(match) == 3 {
			if duration, err := strconv.Atoi(match[2]); err == nil && duration <= 300000 {
				if result.timings == nil {
					result.timings = make(map[string]int, 6)
				}
				result.timings[match[1]] = duration
			}
		}
		match := readinessDiagnosticPattern.FindStringSubmatch(line)
		if len(match) != 26 {
			continue
		}
		counts := [22]int{}
		valid := true
		for index, value := range match[4:] {
			count, err := strconv.Atoi(value)
			if err != nil || count > 10000 {
				valid = false
				break
			}
			counts[index] = count
		}
		if !valid {
			continue
		}
		result.readiness = &prerenderReadiness{
			snapshotMatch:             match[1] == "true",
			revisionMatch:             match[2] == "true",
			engineReady:               match[3] == "true",
			placements:                counts[0],
			ready:                     counts[1],
			errors:                    counts[2],
			requestFailures:           counts[3],
			failedResponses:           counts[4],
			pageErrors:                counts[5],
			policyBlockedRequests:     counts[6],
			originBlocks:              counts[7],
			methodBlocks:              counts[8],
			pathBlocks:                counts[9],
			requestLimitBlocks:        counts[10],
			requestFailureAborted:     counts[11],
			requestFailureFailed:      counts[12],
			requestFailureBlocked:     counts[13],
			requestFailureConnection:  counts[14],
			requestFailureOther:       counts[15],
			requestFailurePolicyAbort: counts[16],
			abortedScripts:            counts[17],
			abortedStylesheets:        counts[18],
			abortedFetchXHR:           counts[19],
			abortedImages:             counts[20],
			abortedOther:              counts[21],
		}
	}
	return result
}

func workerStage(diagnostic string) string {
	return parseWorkerDiagnostics(diagnostic).stage
}

func NewPrerenderer(baseURL string, options PrerenderOptions) (*Prerenderer, error) {
	if err := options.Validate(); err != nil {
		return nil, err
	}
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("prerender base URL must be an internal http(s) origin")
	}
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return &Prerenderer{options: options, baseURL: strings.TrimRight(baseURL, "/"), nonce: hex.EncodeToString(nonce), slots: make(chan struct{}, options.Concurrency), queue: make(chan struct{}, options.QueueSize), cache: map[string]renderedPage{}, pending: map[string]*pendingPage{}}, nil
}

func (p *Prerenderer) IsWorkerRequest(r *http.Request) bool {
	return r.Header.Get("X-Engine-Prerender-Worker") == p.nonce
}

func (p *Prerenderer) Render(ctx context.Context, document Document, requestPath, query string) ([]byte, error) {
	started := time.Now()
	measurements := prerenderMeasurements{cacheResult: "miss", missReason: "absent"}
	if document.CacheKey == "" || document.SnapshotID == "" {
		err := errors.New("prerender requires a snapshot/content identity")
		measurements.errorCode = "invalid_identity"
		measurements.renderDuration = time.Since(started)
		p.logRender(ctx, measurements, err)
		return nil, err
	}
	// Non-cacheable documents can depend on request query parameters or carry
	// non-indexable/private page state. The worker still renders them, but they
	// must not share a cache or in-flight generation keyed only by the resolved
	// document identity.
	if !document.Cacheable {
		measurements.cacheResult = "bypass"
		measurements.missReason = "document_uncacheable"
		data, generated, err := p.generate(ctx, document, requestPath, query)
		measurements.queueWait = generated.queueWait
		measurements.workerDuration = generated.workerDuration
		measurements.workerTimings = generated.workerTimings
		measurements.renderDuration = time.Since(started)
		measurements.workerAttempts = generated.workerAttempts
		measurements.workerStage = generated.workerStage
		measurements.readiness = generated.readiness
		measurements.errorCode = generated.errorCode
		measurements.htmlBytes = len(data)
		p.logRender(ctx, measurements, err)
		return data, err
	}
	// Document.CacheKey is the complete resolved render identity. Keep the
	// original query for the worker on a miss, but do not let irrelevant query
	// parameters create a new cache entry.
	keyBytes := sha256.Sum256([]byte(document.CacheKey + "\x00" + requestPath))
	key := hex.EncodeToString(keyBytes[:])
	expiredEntries := 0
	p.mu.Lock()
	now := time.Now()
	if item, ok := p.cache[key]; ok {
		if now.Before(item.expires) {
			item.lastAccess = now
			p.cache[key] = item
			p.mu.Unlock()
			measurements.cacheResult = "hit"
			measurements.htmlBytes = len(item.data)
			measurements.renderDuration = time.Since(started)
			p.logRender(ctx, measurements, nil)
			return item.data, nil
		}
		delete(p.cache, key)
		expiredEntries++
		measurements.missReason = "expired"
	}
	if pending := p.pending[key]; pending != nil {
		p.mu.Unlock()
		measurements.cacheResult = "coalesced"
		select {
		case <-pending.done:
			measurements.htmlBytes = len(pending.data)
			measurements.errorCode = pending.errorCode
			measurements.renderDuration = time.Since(started)
			p.logRender(ctx, measurements, pending.err)
			return pending.data, pending.err
		case <-ctx.Done():
			measurements.errorCode = prerenderErrorCode(ctx.Err())
			measurements.renderDuration = time.Since(started)
			p.logRender(ctx, measurements, ctx.Err())
			return nil, ctx.Err()
		}
	}
	pending := &pendingPage{done: make(chan struct{})}
	p.pending[key] = pending
	p.mu.Unlock()
	data, generated, err := p.generate(ctx, document, requestPath, query)
	measurements.queueWait = generated.queueWait
	measurements.workerDuration = generated.workerDuration
	measurements.workerTimings = generated.workerTimings
	measurements.workerAttempts = generated.workerAttempts
	measurements.workerStage = generated.workerStage
	measurements.readiness = generated.readiness
	measurements.errorCode = generated.errorCode
	measurements.htmlBytes = len(data)
	p.mu.Lock()
	lruEvicted := false
	if err == nil {
		now := time.Now()
		for oldKey, page := range p.cache {
			if !now.Before(page.expires) {
				delete(p.cache, oldKey)
				expiredEntries++
			}
		}
		if len(p.cache) >= p.options.CacheEntries {
			var oldestKey string
			var oldestAccess time.Time
			for oldKey, page := range p.cache {
				if oldestKey == "" || page.lastAccess.Before(oldestAccess) {
					oldestKey, oldestAccess = oldKey, page.lastAccess
				}
			}
			if oldestKey != "" {
				delete(p.cache, oldestKey)
				lruEvicted = true
			}
		}
		p.cache[key] = renderedPage{data: data, expires: now.Add(p.options.CacheTTL), lastAccess: now}
	}
	pending.data, pending.err = data, err
	pending.errorCode = generated.errorCode
	delete(p.pending, key)
	close(pending.done)
	p.mu.Unlock()
	measurements.renderDuration = time.Since(started)
	if expiredEntries > 0 {
		p.logCacheEviction(ctx, "expired", expiredEntries)
	}
	if lruEvicted {
		p.logCacheEviction(ctx, "lru", 1)
	}
	p.logRender(ctx, measurements, err)
	return data, err
}

func (p *Prerenderer) generate(parent context.Context, document Document, requestPath, query string) ([]byte, prerenderMeasurements, error) {
	measurements := prerenderMeasurements{}
	ctx, cancel := context.WithTimeout(parent, p.options.Timeout)
	defer cancel()
	queuedAt := time.Now()
	select {
	case p.queue <- struct{}{}:
	default:
		measurements.errorCode = "queue_full"
		return nil, measurements, errors.New("prerender queue is full")
	}
	defer func() { <-p.queue }()
	select {
	case p.slots <- struct{}{}:
		measurements.queueWait = time.Since(queuedAt)
	case <-ctx.Done():
		measurements.queueWait = time.Since(queuedAt)
		measurements.errorCode = prerenderErrorCode(ctx.Err())
		return nil, measurements, ctx.Err()
	}
	defer func() { <-p.slots }()
	target, _ := url.Parse(p.baseURL)
	target.Path, target.RawQuery = requestPath, query
	input, err := json.Marshal(map[string]any{"url": target.String(), "origin": p.baseURL, "nonce": p.nonce, "status": document.Status, "snapshotId": document.SnapshotID, "contentRevision": document.ContentRevision, "playwrightModule": p.options.PlaywrightModule, "browserPath": p.options.BrowserPath, "timeoutMs": p.options.AttemptTimeout.Milliseconds(), "quietMs": p.options.QuietInterval.Milliseconds(), "maxHTMLBytes": p.options.MaxHTMLBytes, "width": p.options.ViewportWidth, "height": p.options.ViewportHeight, "maxRequests": p.options.MaxRequests, "maxNetworkBytes": p.options.MaxNetworkBytes, "heapMB": p.options.HeapMB})
	if err != nil {
		measurements.errorCode = "input_error"
		return nil, measurements, err
	}
	for attempt := 1; attempt <= p.options.MaxAttempts; attempt++ {
		workerStarted := time.Now()
		data, diagnostics, timedOut, err := p.generateAttempt(ctx, input)
		measurements.workerDuration += time.Since(workerStarted)
		measurements.workerAttempts++
		measurements.workerStage = diagnostics.stage
		measurements.readiness = diagnostics.readiness
		for phase, duration := range diagnostics.timings {
			if measurements.workerTimings == nil {
				measurements.workerTimings = make(map[string]int, 6)
			}
			measurements.workerTimings[phase] += duration
		}
		if err == nil {
			return data, measurements, nil
		}
		measurements.errorCode = prerenderAttemptErrorCode(err, timedOut)
		if p.logger != nil && !errors.Is(parent.Err(), context.Canceled) {
			reason := "worker_error"
			if timedOut {
				reason = "timeout"
			}
			p.logger.Warn("prerender worker unavailable", "stage", diagnostics.stage, "reason", reason, "attempt", attempt)
		}
		// Repeating a page after navigation could repeat side effects in a
		// faulty feature. Only retry a hung browser/context startup.
		if !timedOut || ctx.Err() != nil || (diagnostics.stage != "startup" && diagnostics.stage != "module" && diagnostics.stage != "browser") || attempt == p.options.MaxAttempts {
			return nil, measurements, err
		}
	}
	measurements.errorCode = "attempts_exhausted"
	return nil, measurements, errors.New("prerender attempts exhausted")
}

func prerenderErrorCode(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "cancelled"
	default:
		return "render_failed"
	}
}

func prerenderAttemptErrorCode(err error, timedOut bool) string {
	if timedOut {
		return "timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	if strings.Contains(err.Error(), "invalid HTML document") {
		return "invalid_output"
	}
	return "worker_error"
}

func (p *Prerenderer) logRender(ctx context.Context, measurements prerenderMeasurements, err error) {
	if p.logger == nil {
		return
	}
	outcome := "success"
	if err != nil {
		outcome = "error"
		if measurements.errorCode == "" {
			measurements.errorCode = prerenderErrorCode(err)
		}
	}
	attrs := []slog.Attr{
		slog.String("cache_result", measurements.cacheResult),
		slog.String("outcome", outcome),
		slog.Float64("render_duration_ms", milliseconds(measurements.renderDuration)),
	}
	if measurements.missReason != "" {
		attrs = append(attrs, slog.String("cache_miss_reason", measurements.missReason))
	}
	if measurements.queueWait > 0 || measurements.cacheResult == "miss" || measurements.cacheResult == "bypass" {
		attrs = append(attrs, slog.Float64("queue_wait_ms", milliseconds(measurements.queueWait)))
	}
	if measurements.workerDuration > 0 || measurements.workerAttempts > 0 {
		attrs = append(attrs,
			slog.Float64("worker_duration_ms", milliseconds(measurements.workerDuration)),
			slog.Int("worker_attempts", measurements.workerAttempts),
		)
	}
	if measurements.workerStage != "" {
		attrs = append(attrs, slog.String("worker_stage", measurements.workerStage))
	}
	for _, phase := range []string{"browser_launch", "context_creation", "navigation", "runtime_data", "readiness", "serialization"} {
		if duration, ok := measurements.workerTimings[phase]; ok {
			attrs = append(attrs, slog.Int(phase+"_ms", duration))
		}
	}
	if measurements.readiness != nil {
		attrs = append(attrs,
			slog.Bool("readiness_snapshot_match", measurements.readiness.snapshotMatch),
			slog.Bool("readiness_revision_match", measurements.readiness.revisionMatch),
			slog.Bool("readiness_engine_ready", measurements.readiness.engineReady),
			slog.Int("readiness_placements", measurements.readiness.placements),
			slog.Int("readiness_ready", measurements.readiness.ready),
			slog.Int("readiness_errors", measurements.readiness.errors),
			slog.Int("readiness_request_failures", measurements.readiness.requestFailures),
			slog.Int("readiness_failed_responses", measurements.readiness.failedResponses),
			slog.Int("readiness_page_errors", measurements.readiness.pageErrors),
			slog.Int("readiness_policy_blocked_requests", measurements.readiness.policyBlockedRequests),
			slog.Int("readiness_origin_blocks", measurements.readiness.originBlocks),
			slog.Int("readiness_method_blocks", measurements.readiness.methodBlocks),
			slog.Int("readiness_path_blocks", measurements.readiness.pathBlocks),
			slog.Int("readiness_request_limit_blocks", measurements.readiness.requestLimitBlocks),
			slog.Int("readiness_request_failure_aborted", measurements.readiness.requestFailureAborted),
			slog.Int("readiness_request_failure_failed", measurements.readiness.requestFailureFailed),
			slog.Int("readiness_request_failure_blocked_by_client", measurements.readiness.requestFailureBlocked),
			slog.Int("readiness_request_failure_connection", measurements.readiness.requestFailureConnection),
			slog.Int("readiness_request_failure_other", measurements.readiness.requestFailureOther),
			slog.Int("readiness_request_failure_policy_abort", measurements.readiness.requestFailurePolicyAbort),
			slog.Int("readiness_aborted_script", measurements.readiness.abortedScripts),
			slog.Int("readiness_aborted_stylesheet", measurements.readiness.abortedStylesheets),
			slog.Int("readiness_aborted_fetch_xhr", measurements.readiness.abortedFetchXHR),
			slog.Int("readiness_aborted_image", measurements.readiness.abortedImages),
			slog.Int("readiness_aborted_other", measurements.readiness.abortedOther),
		)
	}
	if measurements.htmlBytes > 0 && err == nil {
		attrs = append(attrs, slog.Int("html_bytes", measurements.htmlBytes))
	}
	if measurements.errorCode != "" {
		attrs = append(attrs, slog.String("error_code", measurements.errorCode))
	}
	p.logger.LogAttrs(ctx, slog.LevelDebug, "prerender render", attrs...)
}

func (p *Prerenderer) logCacheEviction(ctx context.Context, reason string, entries int) {
	if p.logger == nil {
		return
	}
	p.logger.LogAttrs(ctx, slog.LevelDebug, "prerender cache eviction", slog.String("reason", reason), slog.Int("entries", entries))
}

func milliseconds(duration time.Duration) float64 {
	return float64(duration) / float64(time.Millisecond)
}

func (p *Prerenderer) generateAttempt(parent context.Context, input []byte) ([]byte, workerDiagnostics, bool, error) {
	ctx, cancel := context.WithTimeout(parent, p.options.AttemptTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, p.options.NodePath, fmt.Sprintf("--max-old-space-size=%d", p.options.HeapMB), p.options.WorkerPath)
	// Node owns a Chrome process tree. Killing only Node on timeout would leave
	// orphaned browsers, so the complete process group is cancelled together.
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error { return syscall.Kill(-command.Process.Pid, syscall.SIGKILL) }
	command.Stdin = bytes.NewReader(input)
	output := &boundedBuffer{remaining: p.options.MaxHTMLBytes}
	diagnostic := &boundedBuffer{remaining: 4096}
	command.Stdout, command.Stderr = output, diagnostic
	if err := command.Run(); err != nil {
		worker := parseWorkerDiagnostics(diagnostic.buffer.String())
		if ctx.Err() != nil {
			return nil, worker, errors.Is(ctx.Err(), context.DeadlineExceeded), ctx.Err()
		}
		return nil, worker, false, errors.New("prerender worker failed")
	}
	if !bytes.HasPrefix(bytes.TrimSpace(output.buffer.Bytes()), []byte("<!DOCTYPE html>")) {
		return nil, parseWorkerDiagnostics(diagnostic.buffer.String()), false, errors.New("prerender worker returned an invalid HTML document")
	}
	return output.buffer.Bytes(), parseWorkerDiagnostics(diagnostic.buffer.String()), false, nil
}

type boundedBuffer struct {
	buffer    bytes.Buffer
	remaining int
}

func (b *boundedBuffer) Write(data []byte) (int, error) {
	if len(data) > b.remaining {
		return 0, io.ErrShortBuffer
	}
	b.remaining -= len(data)
	return b.buffer.Write(data)
}
