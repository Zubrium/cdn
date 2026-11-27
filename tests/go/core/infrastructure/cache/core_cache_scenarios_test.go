package cache_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	appcache "github.com/Zubrium/cdn/core/application/cache"
	corecache "github.com/Zubrium/cdn/core/infrastructure/cache"
	"github.com/Zubrium/cdn/pkg/observability"
)

const (
	scenarioFileID      = "00000000-0000-0000-0000-000000000001"
	scenarioPrincipalID = "00000000-0000-0000-0000-000000000002"
)

type scenarioEpoch struct {
	mu        sync.Mutex
	epochs    map[string]int64
	revisions map[string]int64
	fail      bool
}

func newScenarioEpoch() *scenarioEpoch {
	return &scenarioEpoch{
		epochs:    map[string]int64{scenarioPrincipalID: 1},
		revisions: map[string]int64{scenarioFileID: 1},
	}
}

func (e *scenarioEpoch) AccessState(_ context.Context, principalID, fileID string) (int64, int64, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.fail {
		return 0, 0, errors.New("epoch source unavailable")
	}
	revision, ok := e.revisions[fileID]
	if !ok {
		revision = 1
	}
	return e.epochs[principalID], revision, nil
}

func (e *scenarioEpoch) Revision(_ context.Context, fileID string) (int64, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.fail {
		return 0, errors.New("epoch source unavailable")
	}
	revision, ok := e.revisions[fileID]
	if !ok {
		return 1, nil
	}
	return revision, nil
}

func (e *scenarioEpoch) bump(principalID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.epochs[principalID]++
}

func (e *scenarioEpoch) setRevision(fileID string, revision int64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.revisions[fileID] = revision
}

func (e *scenarioEpoch) setFail(fail bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.fail = fail
}

type scenarioAuthorizer struct {
	mu             sync.Mutex
	allowed        map[string]bool
	epoch          *scenarioEpoch
	calls          int64
	inflight       int64
	maxInflight    int64
	transientLeft  int
	permanentError error
	block          func(context.Context) error
}

func newScenarioAuthorizer(epoch *scenarioEpoch) *scenarioAuthorizer {
	return &scenarioAuthorizer{
		allowed: map[string]bool{scenarioPrincipalID: true},
		epoch:   epoch,
	}
}

func (a *scenarioAuthorizer) Authorize(ctx context.Context, principalID, fileID, action string) (appcache.Authorization, error) {
	a.mu.Lock()
	a.calls++
	inflight := a.inflight + 1
	a.inflight = inflight
	if inflight > a.maxInflight {
		a.maxInflight = inflight
	}
	allowed := a.allowed[principalID]
	transient := a.transientLeft > 0
	if transient {
		a.transientLeft--
	}
	permanent := a.permanentError
	block := a.block
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.inflight--
		a.mu.Unlock()
	}()
	if block != nil {
		if err := block(ctx); err != nil {
			return appcache.Authorization{}, err
		}
	}
	if transient {
		return appcache.Authorization{}, appcache.Transient(errors.New("storage temporarily unavailable"))
	}
	if permanent != nil {
		return appcache.Authorization{}, permanent
	}
	if !allowed {
		return appcache.Authorization{}, errors.New("access denied")
	}
	epoch, revision, err := a.epoch.AccessState(ctx, principalID, fileID)
	if err != nil {
		return appcache.Authorization{}, err
	}
	return appcache.Authorization{
		PrincipalID: principalID, FileID: fileID, Action: action,
		Revision: revision, AccessEpoch: epoch, Groups: []string{"public"},
	}, nil
}

func (a *scenarioAuthorizer) callCount() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls
}

func (a *scenarioAuthorizer) allowedFor(principalID string, allowed bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.allowed[principalID] = allowed
}

func (a *scenarioAuthorizer) failNextTransient(count int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.transientLeft = count
}

func (a *scenarioAuthorizer) setPermanentError(err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.permanentError = err
}

func (a *scenarioAuthorizer) setBlock(block func(context.Context) error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.block = block
}

type scenarioMetadataSource struct{}

func (scenarioMetadataSource) Metadata(_ context.Context, fileID string, auth appcache.Authorization) (appcache.Metadata, error) {
	return appcache.Metadata{
		FileID: fileID, Revision: auth.Revision, Size: 5, Ready: true,
		Groups: append([]string(nil), auth.Groups...),
	}, nil
}

type scenarioBytesSource struct{}

func (scenarioBytesSource) Bytes(context.Context, string, appcache.Metadata) ([]byte, error) {
	return []byte("hello"), nil
}

type scenarioObserverSink struct {
	*observability.Observer
}

func (s scenarioObserverSink) attributes(labels []appcache.Label) []observability.Attribute {
	attributes := make([]observability.Attribute, 0, len(labels))
	for _, label := range labels {
		attributes = append(attributes, observability.String(label.Key, label.Value))
	}
	return attributes
}

func (s scenarioObserverSink) IncCounter(ctx context.Context, name string, value float64, labels ...appcache.Label) {
	s.Observer.IncCounter(ctx, name, value, s.attributes(labels)...)
}

func (s scenarioObserverSink) SetGauge(ctx context.Context, name string, value float64, labels ...appcache.Label) {
	s.Observer.SetGauge(ctx, name, value, s.attributes(labels)...)
}

func (s scenarioObserverSink) ObserveHistogram(ctx context.Context, name string, value float64, labels ...appcache.Label) {
	s.Observer.ObserveHistogram(ctx, name, value, s.attributes(labels)...)
}

func scenarioObserver(t *testing.T) scenarioObserverSink {
	t.Helper()
	observer, err := observability.New(observability.Config{Mode: observability.ModeNoop})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = observer.Close(context.Background()) })
	return scenarioObserverSink{Observer: observer}
}

func scenarioStore(t *testing.T, epoch appcache.EpochSource, observer scenarioObserverSink) *appcache.Store {
	return scenarioStoreWithAccessTTL(t, epoch, observer, time.Minute)
}

func scenarioStoreWithBatch(t *testing.T, epoch appcache.EpochSource, observer scenarioObserverSink, batchSize int) *appcache.Store {
	t.Helper()
	store, err := appcache.NewStore(appcache.StoreConfig{
		BytesMaxBytes: 1 << 20, MetadataMaxBytes: 1 << 20, MaxObjectBytes: 1 << 16,
		BytesTTL: time.Minute, MetadataTTL: time.Minute,
		Shards: 4, PromotionInterval: 1,
		CoalesceEnabled: true, CoalesceMaxWaiters: 8, CoalesceTimeout: time.Second,
		AccessMaxEntries: 64, AccessMaxBytes: 1 << 16, AccessTTL: time.Minute,
		InvalidationBatchSize: batchSize, InvalidationBatchWindow: 10 * time.Millisecond,
		Epoch: epoch, Observer: observer, CacheFactory: corecache.Factory{},
	})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

// scenarioStoreWithAccessTTL keeps refresh-loop scenarios honest: a warmed
// authorization entry that is still fresh is a cache hit, so it cannot prove
// that a second warm-up cycle actually ran.
func scenarioStoreWithAccessTTL(t *testing.T, epoch appcache.EpochSource, observer scenarioObserverSink, accessTTL time.Duration) *appcache.Store {
	t.Helper()
	store, err := appcache.NewStore(appcache.StoreConfig{
		BytesMaxBytes: 1 << 20, MetadataMaxBytes: 1 << 20, MaxObjectBytes: 1 << 16,
		BytesTTL: time.Minute, MetadataTTL: time.Minute,
		Shards: 4, PromotionInterval: 1,
		CoalesceEnabled: true, CoalesceMaxWaiters: 8, CoalesceTimeout: time.Second,
		AccessMaxEntries: 64, AccessMaxBytes: 1 << 16, AccessTTL: accessTTL,
		InvalidationBatchSize: 8, InvalidationBatchWindow: 10 * time.Millisecond,
		Epoch: epoch, Observer: observer, CacheFactory: corecache.Factory{},
	})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func scenarioConfig() appcache.Config {
	return appcache.Config{
		Enabled: true, Mode: appcache.ModeStrict,
		Timeout: 200 * time.Millisecond, PerKeyTimeout: 50 * time.Millisecond,
		RefreshInterval: 0, Concurrency: 2, MaxKeys: 8,
		Retry:   appcache.Retry{Attempts: 2, Backoff: time.Millisecond},
		Sources: appcache.Sources{Inline: true},
		Targets: appcache.Targets{Authorization: true, Metadata: true, Bytes: true},
		Keys: []appcache.Key{{
			FileID: scenarioFileID, Tier: appcache.TierRequired,
			Authorizations: []appcache.PrincipalAction{{PrincipalID: scenarioPrincipalID, Action: "read"}},
		}},
	}
}

func scenarioRunner(t *testing.T, cfg appcache.Config, store *appcache.Store, authorizer appcache.Authorizer, epoch *scenarioEpoch, observer scenarioObserverSink) *appcache.Runner {
	t.Helper()
	runner, err := appcache.NewRunner(cfg, appcache.Dependencies{
		Store: store, Observer: observer, Authorizer: authorizer,
		Metadata: scenarioMetadataSource{}, Bytes: scenarioBytesSource{},
		Epoch: epoch,
	})
	if err != nil {
		t.Fatal(err)
	}
	return runner
}

func scenarioMetrics(t *testing.T, observer scenarioObserverSink) string {
	t.Helper()
	recorder := httptest.NewRecorder()
	observer.Observer.MetricsHandler().ServeHTTP(recorder, httptest.NewRequest("GET", "/metrics", nil))
	return recorder.Body.String()
}

func metricHasLabels(metrics, name string, pairs ...string) bool {
	for _, line := range strings.Split(metrics, "\n") {
		if !strings.HasPrefix(line, name) {
			continue
		}
		matched := true
		for _, pair := range pairs {
			if !strings.Contains(line, pair) {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

func metricValue(t *testing.T, observer scenarioObserverSink, name string, pairs ...string) float64 {
	t.Helper()
	metrics := scenarioMetrics(t, observer)
	for _, line := range strings.Split(metrics, "\n") {
		if !strings.HasPrefix(line, name) {
			continue
		}
		matched := true
		for _, pair := range pairs {
			if !strings.Contains(line, pair) {
				matched = false
				break
			}
		}
		if !matched {
			continue
		}
		fields := strings.Fields(line)
		value, err := strconv.ParseFloat(fields[len(fields)-1], 64)
		if err != nil {
			t.Fatalf("metric line %q: %v", line, err)
		}
		return value
	}
	t.Fatalf("metric %s with %v is absent:\n%s", name, pairs, metrics)
	return 0
}

func TestCoreCacheScenarios(t *testing.T) {
	t.Run("CACHE-001", func(t *testing.T) {
		observer := scenarioObserver(t)
		epoch := newScenarioEpoch()
		authorizer := newScenarioAuthorizer(epoch)
		store := scenarioStore(t, epoch, observer)
		runner := scenarioRunner(t, scenarioConfig(), store, authorizer, epoch, observer)

		result, err := runner.Run(context.Background())
		if err != nil || !result.Ready {
			t.Fatalf("strict warm-up result = %+v, err=%v; want Ready without error", result, err)
		}
		if result.Warmed < 3 {
			t.Fatalf("warmed %d targets, want at least authorization, metadata and bytes", result.Warmed)
		}
		if calls := authorizer.callCount(); calls != 1 {
			t.Fatalf("authorizer called %d times during warm-up, want 1", calls)
		}
		stats := store.Stats()
		if stats.BytesEntries == 0 || stats.MetadataEntries == 0 || stats.AuthorizationEntries == 0 {
			t.Fatalf("cache stats after warm-up = %+v; want all Core targets populated", stats)
		}
		auth, err := store.Authorize(context.Background(), scenarioPrincipalID, scenarioFileID, "read", authorizer)
		if err != nil || auth.Revision != 1 {
			t.Fatalf("cached authorization = %+v, err=%v", auth, err)
		}
		if calls := authorizer.callCount(); calls != 1 {
			t.Fatalf("cache hit triggered a fresh authorizer call: %d", calls)
		}
		view, ok := store.GetBytes(context.Background(), scenarioFileID)
		if !ok || !bytes.Equal(view.Bytes(), []byte("hello")) {
			t.Fatalf("warmed bytes unavailable: ok=%v", ok)
		}
		metrics := scenarioMetrics(t, observer)
		for _, expected := range []struct {
			name  string
			pairs []string
		}{
			{"zubrium_cache_warmup_keys_total", []string{`cache="core.authorization"`, `result="WARMED"`, `tier="REQUIRED"`}},
			{"zubrium_cache_operations_total", []string{`cache="core.authorization"`, `result="HIT"`}},
			{"zubrium_cache_operations_total", []string{`cache="core.bytes"`, `result="HIT"`}},
			{"zubrium_cache_warmup_duration_seconds_count", []string{`cache="core.authorization"`}},
		} {
			if !metricHasLabels(metrics, expected.name, expected.pairs...) {
				t.Errorf("metrics do not contain %s with %v\n%s", expected.name, expected.pairs, metrics)
			}
		}
	})

	t.Run("CACHE-002", func(t *testing.T) {
		t.Run("required-fails", func(t *testing.T) {
			observer := scenarioObserver(t)
			epoch := newScenarioEpoch()
			authorizer := newScenarioAuthorizer(epoch)
			authorizer.setPermanentError(errors.New("authorization backend unavailable"))
			store := scenarioStore(t, epoch, observer)
			result, err := scenarioRunner(t, scenarioConfig(), store, authorizer, epoch, observer).Run(context.Background())
			if err == nil || result.Ready {
				t.Fatalf("required key failure result = %+v, err=%v; want not Ready and error", result, err)
			}
			metrics := scenarioMetrics(t, observer)
			if !metricHasLabels(metrics, "zubrium_cache_warmup_keys_total", `cache="core.authorization"`, `result="FAILED"`, `tier="REQUIRED"`) {
				t.Fatalf("required failure metric missing:\n%s", metrics)
			}
		})
		t.Run("optional-fails", func(t *testing.T) {
			observer := scenarioObserver(t)
			epoch := newScenarioEpoch()
			authorizer := newScenarioAuthorizer(epoch)
			authorizer.setPermanentError(errors.New("authorization backend unavailable"))
			cfg := scenarioConfig()
			cfg.Keys[0].Tier = appcache.TierOptional
			store := scenarioStore(t, epoch, observer)
			result, err := scenarioRunner(t, cfg, store, authorizer, epoch, observer).Run(context.Background())
			if err != nil || !result.Ready {
				t.Fatalf("optional key failure result = %+v, err=%v; want Ready", result, err)
			}
			if result.Failed == 0 {
				t.Fatal("optional failure was not counted")
			}
			metrics := scenarioMetrics(t, observer)
			if !metricHasLabels(metrics, "zubrium_cache_warmup_keys_total", `cache="core.authorization"`, `result="FAILED"`, `tier="OPTIONAL"`) {
				t.Fatalf("optional failure metric missing:\n%s", metrics)
			}
		})
	})

	t.Run("CACHE-003", func(t *testing.T) {
		t.Run("no-hidden-defaults", func(t *testing.T) {
			mutations := []struct {
				name   string
				change func(*appcache.Config)
			}{
				{"timeout", func(c *appcache.Config) { c.Timeout = 0 }},
				{"per_key_timeout", func(c *appcache.Config) { c.PerKeyTimeout = 0 }},
				{"concurrency", func(c *appcache.Config) { c.Concurrency = 0 }},
				{"max_keys", func(c *appcache.Config) { c.MaxKeys = 0 }},
				{"retry_attempts", func(c *appcache.Config) { c.Retry.Attempts = 0 }},
				{"retry_backoff", func(c *appcache.Config) { c.Retry.Backoff = 0 }},
				{"mode", func(c *appcache.Config) { c.Mode = "fast" }},
			}
			for _, mutation := range mutations {
				cfg := scenarioConfig()
				mutation.change(&cfg)
				if err := cfg.Validate(); err == nil {
					t.Errorf("config with %s was accepted; hidden defaults are forbidden", mutation.name)
				}
			}
			observer := scenarioObserver(t)
			epoch := newScenarioEpoch()
			cfg := appcache.StoreConfig{
				BytesMaxBytes: 1 << 20, MetadataMaxBytes: 1 << 20, MaxObjectBytes: 1 << 16,
				BytesTTL: time.Minute, MetadataTTL: time.Minute, Shards: 4, PromotionInterval: 1,
				AccessMaxEntries: 8, AccessMaxBytes: 1 << 16, AccessTTL: time.Minute,
				InvalidationBatchSize: 8, InvalidationBatchWindow: 10 * time.Millisecond,
				Epoch: epoch, Observer: observer, CacheFactory: corecache.Factory{},
			}
			if _, err := appcache.NewStore(cfg); err == nil {
				t.Fatal("store with omitted coalescing bounds was accepted; hidden defaults are forbidden")
			}
			withoutFactory := cfg
			withoutFactory.CacheFactory = nil
			if _, err := appcache.NewStore(withoutFactory); err == nil {
				t.Fatal("store without an injected bounded-cache factory was accepted")
			}
		})
		t.Run("runtime-limits", func(t *testing.T) {
			observer := scenarioObserver(t)
			epoch := newScenarioEpoch()
			authorizer := newScenarioAuthorizer(epoch)
			authorizer.setBlock(func(context.Context) error {
				time.Sleep(time.Millisecond)
				return nil
			})
			store := scenarioStore(t, epoch, observer)
			cfg := scenarioConfig()
			cfg.Concurrency = 2
			cfg.MaxKeys = 3
			cfg.Keys = nil
			for index := 0; index < 6; index++ {
				cfg.Keys = append(cfg.Keys, appcache.Key{
					FileID: fmt.Sprintf("00000000-0000-0000-0000-%012d", index+10),
					Tier:   appcache.TierRequired,
					Authorizations: []appcache.PrincipalAction{
						{PrincipalID: scenarioPrincipalID, Action: "read"},
					},
				})
			}
			result, err := scenarioRunner(t, cfg, store, authorizer, epoch, observer).Run(context.Background())
			if err != nil || !result.Ready {
				t.Fatalf("bounded run result = %+v, err=%v", result, err)
			}
			if result.Keys != 3 {
				t.Fatalf("processed %d keys, want max_keys=3", result.Keys)
			}
			authorizer.mu.Lock()
			maxInflight := authorizer.maxInflight
			authorizer.mu.Unlock()
			if maxInflight > 2 {
				t.Fatalf("warm-up reached concurrency %d, want at most 2", maxInflight)
			}
		})
		t.Run("timeouts-and-retry", func(t *testing.T) {
			observer := scenarioObserver(t)
			epoch := newScenarioEpoch()
			authorizer := newScenarioAuthorizer(epoch)
			authorizer.setBlock(func(ctx context.Context) error {
				<-ctx.Done()
				return ctx.Err()
			})
			store := scenarioStore(t, epoch, observer)
			cfg := scenarioConfig()
			cfg.Timeout = 60 * time.Millisecond
			cfg.PerKeyTimeout = 10 * time.Millisecond
			started := time.Now()
			result, err := scenarioRunner(t, cfg, store, authorizer, epoch, observer).Run(context.Background())
			elapsed := time.Since(started)
			if err == nil || result.Ready {
				t.Fatalf("per-key timeout result = %+v, err=%v; want not Ready", result, err)
			}
			if elapsed > 500*time.Millisecond {
				t.Fatalf("timeout was not enforced: elapsed %s", elapsed)
			}

			retryObserver := scenarioObserver(t)
			retryEpoch := newScenarioEpoch()
			retryAuthorizer := newScenarioAuthorizer(retryEpoch)
			retryAuthorizer.failNextTransient(2)
			retryStore := scenarioStore(t, retryEpoch, retryObserver)
			retryCfg := scenarioConfig()
			retryCfg.Retry.Attempts = 3
			result, err = scenarioRunner(t, retryCfg, retryStore, retryAuthorizer, retryEpoch, retryObserver).Run(context.Background())
			if err != nil || !result.Ready {
				t.Fatalf("transient retry result = %+v, err=%v", result, err)
			}
			if calls := retryAuthorizer.callCount(); calls != 3 {
				t.Fatalf("transient failure retried %d times, want 3 attempts", calls)
			}
			nonTransientObserver := scenarioObserver(t)
			nonTransientEpoch := newScenarioEpoch()
			nonTransientAuthorizer := newScenarioAuthorizer(nonTransientEpoch)
			nonTransientAuthorizer.setPermanentError(errors.New("invalid grant"))
			nonTransientStore := scenarioStore(t, nonTransientEpoch, nonTransientObserver)
			_, _ = scenarioRunner(t, scenarioConfig(), nonTransientStore, nonTransientAuthorizer, nonTransientEpoch, nonTransientObserver).Run(context.Background())
			if calls := nonTransientAuthorizer.callCount(); calls != 1 {
				t.Fatalf("non-transient error retried %d times, want 1 attempt", calls)
			}
		})
	})

	t.Run("CACHE-004", func(t *testing.T) {
		observer := scenarioObserver(t)
		epoch := newScenarioEpoch()
		authorizer := newScenarioAuthorizer(epoch)
		store := scenarioStore(t, epoch, observer)
		if _, err := store.Authorize(context.Background(), scenarioPrincipalID, scenarioFileID, "read", authorizer); err != nil {
			t.Fatal(err)
		}
		epoch.bump(scenarioPrincipalID)
		authorizer.allowedFor(scenarioPrincipalID, false)
		if _, err := store.Authorize(context.Background(), scenarioPrincipalID, scenarioFileID, "read", authorizer); err == nil {
			t.Fatal("warmed authorization entry was reused after access_epoch changed")
		}
		if calls := authorizer.callCount(); calls != 2 {
			t.Fatalf("stale entry skipped %d authorizer calls, want a fresh check", calls)
		}
		epoch.setFail(true)
		authorizer.allowedFor(scenarioPrincipalID, true)
		if _, err := store.Authorize(context.Background(), scenarioPrincipalID, scenarioFileID, "read", authorizer); err == nil {
			t.Fatal("authorization succeeded while epoch verification was unavailable")
		}

		root := filepath.Join("..", "postgres")
		migrationDir := filepath.Join(root, "migrations")
		entries, err := os.ReadDir(migrationDir)
		if err != nil {
			t.Fatal(err)
		}
		var migrations strings.Builder
		for _, entry := range entries {
			if !strings.HasSuffix(entry.Name(), ".sql") {
				continue
			}
			contents, err := os.ReadFile(filepath.Join(migrationDir, entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			migrations.Write(contents)
		}
		if !regexp.MustCompile(`(?i)access_epoch\s+bigint\s+not\s+null\s+default\s+1`).MatchString(migrations.String()) {
			t.Fatal("Core migrations do not define a monotonic access_epoch column")
		}
		mutations := map[string][]string{
			"direct_groups.go": {"func SetGroupAccess(", "func reconcileAccessGroupsTx("},
			"credentials.go":   {"func IssueCredential(", "func RevokeCredential(", "func CreatePrincipalWithAccess(", "func RecoverBootstrapCredential("},
			"principals.go":    {"func SetPrincipalManagement(", "func DeactivatePrincipal("},
			"identity.go":      {"func BootstrapWithGroups("},
		}
		for file, headers := range mutations {
			source, err := os.ReadFile(filepath.Join(root, file))
			if err != nil {
				t.Fatal(err)
			}
			for _, header := range headers {
				start := strings.Index(string(source), header)
				if start < 0 {
					t.Fatalf("%s does not define %s", file, header)
				}
				body := string(source)[start:]
				if next := strings.Index(body[1:], "\nfunc "); next >= 0 {
					body = body[:next+1]
				}
				bump := strings.Index(body, "bumpAccessEpoch")
				notify := strings.Index(body, "pg_notify('core_cache_invalidation'")
				if bump < 0 || notify < 0 || bump > notify {
					t.Errorf("%s %s must bump access_epoch before invalidation in the same transaction", file, header)
				}
			}
		}
	})

	t.Run("CACHE-005", func(t *testing.T) {
		observer := scenarioObserver(t)
		epoch := newScenarioEpoch()
		authorizer := newScenarioAuthorizer(epoch)
		store := scenarioStore(t, epoch, observer)
		runner := scenarioRunner(t, scenarioConfig(), store, authorizer, epoch, observer)
		if _, err := runner.Run(context.Background()); err != nil {
			t.Fatal(err)
		}
		store.Notify(scenarioFileID)
		if _, ok := store.GetBytes(context.Background(), scenarioFileID); ok {
			t.Fatal("byte cache entry survived targeted invalidation")
		}
		if _, ok := store.GetMetadata(context.Background(), scenarioFileID); ok {
			t.Fatal("metadata cache entry survived targeted invalidation")
		}
		metrics := scenarioMetrics(t, observer)
		if !metricHasLabels(metrics, "zubrium_cache_operations_total", `cache="core.bytes"`, `result="INVALIDATED"`) ||
			!metricHasLabels(metrics, "zubrium_cache_operations_total", `cache="core.metadata"`, `result="INVALIDATED"`) {
			t.Fatalf("invalidation metrics missing:\n%s", metrics)
		}

		if _, err := runner.Run(context.Background()); err != nil {
			t.Fatal(err)
		}
		epoch.setRevision(scenarioFileID, 2)
		if _, ok := store.GetBytes(context.Background(), scenarioFileID); ok {
			t.Fatal("stale bytes were served after a revision change")
		}
		if _, err := runner.Run(context.Background()); err != nil {
			t.Fatal(err)
		}
		store.OnReconnect()
		if stats := store.Stats(); stats.BytesEntries != 0 || stats.MetadataEntries != 0 || stats.AuthorizationEntries != 0 {
			t.Fatalf("reconnect left cached entries: %+v", stats)
		}
		if _, ok := store.GetBytes(context.Background(), scenarioFileID); ok {
			t.Fatal("byte cache survived reconnect")
		}
	})

	t.Run("CACHE-010", func(t *testing.T) {
		observer := scenarioObserver(t)
		epoch := newScenarioEpoch()
		authorizer := newScenarioAuthorizer(epoch)
		store := scenarioStore(t, epoch, observer)
		runner := scenarioRunner(t, scenarioConfig(), store, authorizer, epoch, observer)
		if _, err := runner.Run(context.Background()); err != nil {
			t.Fatal(err)
		}
		store.Notify(scenarioFileID)
		metrics := scenarioMetrics(t, observer)
		for _, name := range []string{
			"zubrium_cache_operations_total", "zubrium_cache_entries", "zubrium_cache_bytes",
			"zubrium_cache_warmup_keys_total", "zubrium_cache_warmup_duration_seconds",
			"zubrium_cache_invalidation_lag_seconds",
		} {
			if !strings.Contains(metrics, name) {
				t.Errorf("cache metric %s is absent:\n%s", name, metrics)
			}
		}
		allowedLabels := map[string]bool{
			`cache="core.bytes"`: true, `cache="core.metadata"`: true, `cache="core.authorization"`: true,
			`result="HIT"`: true, `result="MISS"`: true, `result="INVALIDATED"`: true,
			`result="EVICTION"`: true, `result="WARMED"`: true, `result="FAILED"`: true,
			`result="SKIPPED"`: true, `result="LEADER"`: true, `result="JOINED"`: true,
			`result="APPLIED"`: true, `result="OVERFLOW"`: true, `result="CLEARED"`: true,
			`tier="REQUIRED"`: true, `tier="OPTIONAL"`: true,
		}
		allowedPrefixes := []string{"le=", "service.name=", "service.version=", "service.instance.id=", "deployment.profile=", "module.kind=", "module.id=", "component.id=", "operation.id="}
		for _, line := range strings.Split(metrics, "\n") {
			if !strings.HasPrefix(line, "zubrium_cache_") || !strings.Contains(line, "{") {
				continue
			}
			block := line[strings.Index(line, "{")+1 : strings.Index(line, "}")]
			for _, label := range strings.Split(block, ",") {
				if allowedLabels[label] {
					continue
				}
				prefixed := false
				for _, prefix := range allowedPrefixes {
					if strings.HasPrefix(label, prefix) {
						prefixed = true
						break
					}
				}
				if !prefixed {
					t.Errorf("metric line has unexpected label %q: %s", label, line)
				}
			}
			for _, forbidden := range []string{scenarioFileID, scenarioPrincipalID, "http://", "?", "locale="} {
				if strings.Contains(line, forbidden) {
					t.Errorf("metric line leaked high-cardinality data %q: %s", forbidden, line)
				}
			}
		}
	})

	t.Run("CACHE-011", func(t *testing.T) {
		observer := scenarioObserver(t)
		epoch := newScenarioEpoch()
		authorizer := newScenarioAuthorizer(epoch)
		store := scenarioStoreWithAccessTTL(t, epoch, observer, time.Millisecond)
		cfg := scenarioConfig()
		cfg.RefreshInterval = 10 * time.Millisecond
		cfg.Concurrency = 1
		cfg.MaxKeys = 3
		cfg.Keys = nil
		for index := 0; index < 3; index++ {
			cfg.Keys = append(cfg.Keys, appcache.Key{
				FileID: fmt.Sprintf("00000000-0000-0000-0000-%012d", index+20),
				Tier:   appcache.TierRequired,
				Authorizations: []appcache.PrincipalAction{
					{PrincipalID: scenarioPrincipalID, Action: "read"},
				},
			})
		}
		authorizer.setBlock(func(context.Context) error {
			time.Sleep(time.Millisecond)
			return nil
		})
		runner := scenarioRunner(t, cfg, store, authorizer, epoch, observer)
		if _, err := runner.Run(context.Background()); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
		defer cancel()
		if err := runner.RefreshLoop(ctx); err != nil {
			t.Fatal(err)
		}
		authorizer.mu.Lock()
		calls, maxInflight := authorizer.calls, authorizer.maxInflight
		authorizer.mu.Unlock()
		if calls < 6 {
			t.Fatalf("refresh loop produced %d authorizer calls, want at least two cycles over three keys", calls)
		}
		if maxInflight != 1 {
			t.Fatalf("warm-up cycles overlapped: max in-flight authorizations = %d", maxInflight)
		}
		if entries := store.Stats().BytesEntries; entries > cfg.MaxKeys {
			t.Fatalf("refresh grew byte cache to %d entries, budget is %d", entries, cfg.MaxKeys)
		}
	})

	t.Run("CONC-005", func(t *testing.T) {
		t.Run("coalesced_success", func(t *testing.T) {
			observer := scenarioObserver(t)
			epoch := newScenarioEpoch()
			authorizer := newScenarioAuthorizer(epoch)
			release := make(chan struct{})
			authorizer.setBlock(func(ctx context.Context) error {
				select {
				case <-release:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			})
			store := scenarioStore(t, epoch, observer)
			const waiters = 8
			results := make(chan error, waiters)
			authorize := func() {
				_, err := store.Authorize(context.Background(), scenarioPrincipalID, scenarioFileID, "read", authorizer)
				results <- err
			}
			go authorize()
			deadline := time.Now().Add(time.Second)
			for authorizer.callCount() == 0 && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			if authorizer.callCount() != 1 {
				t.Fatal("leader did not start before waiters were released")
			}
			for index := 1; index < waiters; index++ {
				go authorize()
			}
			time.Sleep(30 * time.Millisecond)
			close(release)
			for index := 0; index < waiters; index++ {
				if err := <-results; err != nil {
					t.Fatalf("coalesced authorization %d failed: %v", index, err)
				}
			}
			if calls := authorizer.callCount(); calls != 1 {
				t.Fatalf("coalescing called the authorizer %d times, want 1", calls)
			}
			if got := metricValue(t, observer, "zubrium_cache_coalesced_total", `cache="core.authorization"`, `result="LEADER"`); got != 1 {
				t.Fatalf("LEADER metric = %v, want 1", got)
			}
			if got := metricValue(t, observer, "zubrium_cache_coalesced_total", `cache="core.authorization"`, `result="JOINED"`); got != waiters-1 {
				t.Fatalf("JOINED metric = %v, want %d", got, waiters-1)
			}
		})
		t.Run("leader_error_is_shared", func(t *testing.T) {
			observer := scenarioObserver(t)
			epoch := newScenarioEpoch()
			authorizer := newScenarioAuthorizer(epoch)
			release := make(chan struct{})
			expected := errors.New("authoritative source unavailable")
			authorizer.setBlock(func(ctx context.Context) error {
				select {
				case <-release:
					return expected
				case <-ctx.Done():
					return ctx.Err()
				}
			})
			store := scenarioStore(t, epoch, observer)
			const waiters = 4
			results := make(chan error, waiters)
			authorize := func() {
				_, err := store.Authorize(context.Background(), scenarioPrincipalID, scenarioFileID, "read", authorizer)
				results <- err
			}
			go authorize()
			deadline := time.Now().Add(time.Second)
			for authorizer.callCount() == 0 && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			for index := 1; index < waiters; index++ {
				go authorize()
			}
			time.Sleep(30 * time.Millisecond)
			close(release)
			for index := 0; index < waiters; index++ {
				if err := <-results; !errors.Is(err, expected) {
					t.Fatalf("coalesced result %d = %v, want shared leader error", index, err)
				}
			}
			if calls := authorizer.callCount(); calls != 1 {
				t.Fatalf("failed leader retried the authorizer %d times", calls)
			}
			if _, ok := store.GetBytes(context.Background(), scenarioFileID); ok {
				t.Fatal("failed leader left stale bytes in the cache")
			}
		})
	})

	t.Run("CONC-006", func(t *testing.T) {
		observer := scenarioObserver(t)
		epoch := newScenarioEpoch()
		authorizer := newScenarioAuthorizer(epoch)
		store := scenarioStore(t, epoch, observer)
		runner := scenarioRunner(t, scenarioConfig(), store, authorizer, epoch, observer)
		if _, err := runner.Run(context.Background()); err != nil {
			t.Fatal(err)
		}
		first, ok := store.GetBytes(context.Background(), scenarioFileID)
		if !ok || !first.Intact() {
			t.Fatal("warmed byte view is unavailable or already mutated")
		}
		second, ok := store.GetBytes(context.Background(), scenarioFileID)
		if !ok {
			t.Fatal("second byte view is unavailable")
		}
		if &first.Bytes()[0] != &second.Bytes()[0] {
			t.Fatal("cache hit allocated a new byte buffer instead of returning a view")
		}
		first.Bytes()[0] ^= 0xff
		if first.Intact() || second.Intact() {
			t.Fatal("illegal mutation of a cache view was not detected")
		}
	})

	t.Run("CONC-007", func(t *testing.T) {
		observer := scenarioObserver(t)
		epoch := newScenarioEpoch()
		authorizer := newScenarioAuthorizer(epoch)
		store := scenarioStoreWithBatch(t, epoch, observer, 2)
		runner := scenarioRunner(t, scenarioConfig(), store, authorizer, epoch, observer)
		if _, err := runner.Run(context.Background()); err != nil {
			t.Fatal(err)
		}
		store.NotifyBatch([]string{scenarioFileID, "00000000-0000-0000-0000-000000000099"})
		if _, ok := store.GetBytes(context.Background(), scenarioFileID); ok {
			t.Fatal("applied batch left stale bytes")
		}
		if got := metricValue(t, observer, "zubrium_cache_invalidation_batch_total", `result="APPLIED"`); got != 1 {
			t.Fatalf("APPLIED batch metric = %v, want 1", got)
		}
		if _, err := runner.Run(context.Background()); err != nil {
			t.Fatal(err)
		}
		if stats := store.Stats(); stats.BytesEntries == 0 || stats.MetadataEntries == 0 {
			t.Fatalf("cache was not refilled before overflow: %+v", stats)
		}
		store.NotifyBatch([]string{
			scenarioFileID,
			"00000000-0000-0000-0000-000000000099",
			"00000000-0000-0000-0000-000000000098",
		})
		if stats := store.Stats(); stats.BytesEntries != 0 || stats.MetadataEntries != 0 || stats.AuthorizationEntries != 0 {
			t.Fatalf("overflow did not clear all caches: %+v", stats)
		}
		if got := metricValue(t, observer, "zubrium_cache_invalidation_batch_total", `result="OVERFLOW"`); got != 1 {
			t.Fatalf("OVERFLOW batch metric = %v, want 1", got)
		}
	})

	t.Run("CONC-010", func(t *testing.T) {
		observer := scenarioObserver(t)
		epoch := newScenarioEpoch()
		authorizer := newScenarioAuthorizer(epoch)
		store := scenarioStore(t, epoch, observer)
		runner := scenarioRunner(t, scenarioConfig(), store, authorizer, epoch, observer)
		if _, err := runner.Run(context.Background()); err != nil {
			t.Fatal(err)
		}
		store.Notify(scenarioFileID)
		metrics := scenarioMetrics(t, observer)
		for _, name := range []string{
			"zubrium_cache_coalesced_total", "zubrium_cache_invalidation_batch_total",
		} {
			if !strings.Contains(metrics, name) {
				t.Errorf("concurrency/cache metric %s is absent:\n%s", name, metrics)
			}
		}
		for _, forbidden := range []string{"scope=\"HTTP\"", "route_id", "principal", "file.id", "query="} {
			if strings.Contains(metrics, forbidden) {
				t.Errorf("concurrency metrics contain forbidden label %q", forbidden)
			}
		}
	})
}
