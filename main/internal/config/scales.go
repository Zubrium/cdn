package appconfig

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Target configuration contracts from SPEC-caching.md §7 and
// SPEC-concurrency.md §11. They are deliberately separate from the legacy
// runtime structs: Resolve maps them to the existing internal contracts, and
// no compatibility aliases are accepted on the YAML surface.

type CacheTarget struct {
	MaxBytes       int64             `yaml:"max_bytes"`
	TTL            string            `yaml:"ttl"`
	MaxObjectBytes int64             `yaml:"max_object_bytes"`
	AccessCache    CacheAccessTarget `yaml:"access_cache"`
	Warmup         WarmupTarget      `yaml:"warmup"`
}

type CacheAccessTarget struct {
	MaxEntries int    `yaml:"max_entries"`
	MaxBytes   int64  `yaml:"max_bytes"`
	TTL        string `yaml:"ttl"`
}

type WarmupTarget struct {
	Enabled         bool                `yaml:"enabled"`
	Mode            string              `yaml:"mode"`
	Timeout         string              `yaml:"timeout"`
	PerKeyTimeout   string              `yaml:"per_key_timeout"`
	Concurrency     int                 `yaml:"concurrency"`
	MaxKeys         int                 `yaml:"max_keys"`
	Retry           WarmupRetryTarget   `yaml:"retry"`
	RefreshInterval string              `yaml:"refresh_interval"`
	Sources         WarmupSourcesTarget `yaml:"sources"`
	Targets         WarmupTargetsTarget `yaml:"targets"`
	Keys            []WarmupKeyTarget   `yaml:"keys"`
}

type WarmupRetryTarget struct {
	Attempts int    `yaml:"attempts"`
	Backoff  string `yaml:"backoff"`
}

type WarmupSourcesTarget struct {
	Inline             bool `yaml:"inline"`
	PublishedArtifacts bool `yaml:"published_artifacts"`
}

type WarmupTargetsTarget struct {
	Authorization   bool `yaml:"authorization"`
	Metadata        bool `yaml:"metadata"`
	Bytes           bool `yaml:"bytes"`
	RouterManifests bool `yaml:"router_manifests"`
	RouterRoutes    bool `yaml:"router_routes"`
}

type WarmupKeyTarget struct {
	FileID        string                 `yaml:"file_id"`
	Tier          string                 `yaml:"tier"`
	Authorization WarmupKeyAuthorization `yaml:"authorization"`
}

type WarmupKeyAuthorization struct {
	Principals []string `yaml:"principals"`
	Actions    []string `yaml:"actions"`
}

type BoundedCacheTarget struct {
	Enabled    bool   `yaml:"enabled"`
	MaxEntries int    `yaml:"max_entries"`
	MaxBytes   int64  `yaml:"max_bytes"`
	TTL        string `yaml:"ttl"`
}

type RouterCacheTarget struct {
	Manifests BoundedCacheTarget `yaml:"manifests"`
	Routes    BoundedCacheTarget `yaml:"routes"`
	HTTP      RouterHTTPCache    `yaml:"http"`
}

type RouterHTTPCache struct {
	Compression RouterCompression `yaml:"compression"`
	Vary        RouterVary        `yaml:"vary"`
}

type RouterCompression struct {
	Enabled              bool     `yaml:"enabled"`
	Algorithms           []string `yaml:"algorithms"`
	MinBytes             int64    `yaml:"min_bytes"`
	MaxUncompressedBytes int64    `yaml:"max_uncompressed_bytes"`
	MaxCompressedBytes   int64    `yaml:"max_compressed_bytes"`
	MediaTypes           []string `yaml:"media_types"`
}

type RouterVary struct {
	AcceptEncoding   bool   `yaml:"accept_encoding"`
	LocaleFromCookie string `yaml:"locale_from_cookie"`
}

type AdminConfig struct {
	Concurrency AdminConcurrency `yaml:"concurrency"`
}

type AdminConcurrency struct {
	HTTP HTTPAdmission `yaml:"http"`
}

type HTTPAdmission struct {
	MaxInFlight    int    `yaml:"max_in_flight"`
	RequestTimeout string `yaml:"request_timeout"`
}

type RouterConfig struct {
	Cache       RouterCacheTarget `yaml:"cache"`
	Concurrency RouterConcurrency `yaml:"concurrency"`
}

type RouterConcurrency struct {
	HTTP   RouterHTTPAdmission `yaml:"http"`
	Routes RouterRouteCapacity `yaml:"routes"`
}

type RouterHTTPAdmission struct {
	HTTPAdmission `yaml:",inline"`
	PerRoute      NamedAdmission `yaml:"per_route"`
}

type RouterRouteCapacity struct {
	MaxConcurrentResolutions int `yaml:"max_concurrent_resolutions"`
}

type BusConfig struct {
	Concurrency BusConcurrency `yaml:"concurrency"`
}

type BusConcurrency struct {
	MaxInFlight  int            `yaml:"max_in_flight"`
	PerOperation NamedAdmission `yaml:"per_operation"`
}

type NamedAdmission struct {
	Enabled            bool         `yaml:"enabled"`
	DefaultMaxInFlight int          `yaml:"default_max_in_flight"`
	Overrides          []NamedLimit `yaml:"overrides"`
}

type NamedLimit struct {
	ID          string `yaml:"id"`
	MaxInFlight int    `yaml:"max_in_flight"`
}

type CoreConcurrency struct {
	Storage      StorageConcurrency      `yaml:"storage"`
	Cache        CacheConcurrency        `yaml:"cache"`
	Invalidation InvalidationConcurrency `yaml:"invalidation"`
}

type StorageConcurrency struct {
	MaxConcurrentReads          int    `yaml:"max_concurrent_reads"`
	MaxConcurrentAuthorizations int    `yaml:"max_concurrent_authorizations"`
	OperationTimeout            string `yaml:"operation_timeout"`
	ReadReserve                 int    `yaml:"read_reserve"`
}

type CacheConcurrency struct {
	Shards               int           `yaml:"shards"`
	LRUPromotionInterval int           `yaml:"lru_promotion_interval"`
	Coalesce             CacheCoalesce `yaml:"coalesce"`
}

type CacheCoalesce struct {
	Enabled    bool   `yaml:"enabled"`
	MaxWaiters int    `yaml:"max_waiters"`
	Timeout    string `yaml:"timeout"`
}

type InvalidationConcurrency struct {
	BatchSize   int    `yaml:"batch_size"`
	BatchWindow string `yaml:"batch_window"`
}

type LoaderConcurrency struct {
	MaxConcurrentCommits        int `yaml:"max_concurrent_commits"`
	MaxConcurrentSessionLookups int `yaml:"max_concurrent_session_lookups"`
}

type WorkerConfig struct {
	Archive     WorkerPoolConfig `yaml:"archive"`
	Media       WorkerPoolConfig `yaml:"media"`
	Prerender   WorkerPoolConfig `yaml:"prerender"`
	Publication WorkerPoolConfig `yaml:"publication"`
}

type WorkerPoolConfig struct {
	Concurrency WorkerPoolConcurrency `yaml:"concurrency"`
}

type WorkerPoolConcurrency struct {
	Enabled       bool   `yaml:"enabled"`
	Limit         int    `yaml:"limit"`
	PollInterval  string `yaml:"poll_interval"`
	TaskTimeout   string `yaml:"task_timeout"`
	LeaseDuration string `yaml:"lease_duration"`
}

type ResolvedWorkerPool struct {
	Enabled       bool
	Limit         int
	PollInterval  time.Duration
	TaskTimeout   time.Duration
	LeaseDuration time.Duration
}

type ResolvedWorkers struct {
	Archive     ResolvedWorkerPool
	Media       ResolvedWorkerPool
	Prerender   ResolvedWorkerPool
	Publication ResolvedWorkerPool
}

// ResolvedScales carries parsed target values for runtime wiring.
type ResolvedScales struct {
	CoreCache         CacheTarget
	RouterCache       RouterCacheTarget
	AdminHTTP         HTTPAdmission
	RouterHTTP        RouterHTTPAdmission
	RouterResolutions int
	Bus               BusConcurrency
	CoreConcurrency   CoreConcurrency
	LoaderConcurrency LoaderConcurrency
	Workers           WorkerConfig
	WorkerPools       ResolvedWorkers
	WarmupTimeouts    WarmupResolvedTimeouts
}

type WarmupResolvedTimeouts struct {
	Timeout          time.Duration
	PerKeyTimeout    time.Duration
	Backoff          time.Duration
	RefreshInterval  time.Duration
	OperationTimeout time.Duration
	CoalesceTimeout  time.Duration
	BatchWindow      time.Duration
}

func validateScales(c Config) (ResolvedScales, error) {
	resolved := ResolvedScales{
		CoreCache: c.Core.Cache, RouterCache: c.Router.Cache,
		AdminHTTP: c.Admin.Concurrency.HTTP, RouterHTTP: c.Router.Concurrency.HTTP,
		RouterResolutions: c.Router.Concurrency.Routes.MaxConcurrentResolutions,
		Bus:               c.Bus.Concurrency, CoreConcurrency: c.Core.Concurrency,
		LoaderConcurrency: c.Loader.Concurrency, Workers: c.Worker,
	}
	if err := validateCacheTarget(c.Core.Cache, "core.cache"); err != nil {
		return ResolvedScales{}, err
	}
	if c.Core.Cache.Warmup.Mode != "strict" && c.Core.Cache.Warmup.Mode != "best_effort" && c.Core.Cache.Warmup.Mode != "off" {
		return ResolvedScales{}, errors.New("invalid core.cache.warmup.mode")
	}
	warmup := c.Core.Cache.Warmup
	positive := map[string]int{
		"core.cache.warmup.concurrency":    warmup.Concurrency,
		"core.cache.warmup.max_keys":       warmup.MaxKeys,
		"core.cache.warmup.retry.attempts": warmup.Retry.Attempts,
	}
	for name, value := range positive {
		if value <= 0 {
			return ResolvedScales{}, fmt.Errorf("invalid %s", name)
		}
	}
	seenKeys := make(map[string]struct{}, len(warmup.Keys))
	for _, key := range warmup.Keys {
		if key.Tier != "required" && key.Tier != "optional" {
			return ResolvedScales{}, errors.New("invalid core.cache.warmup.keys.tier")
		}
		if _, err := uuid.Parse(key.FileID); err != nil {
			return ResolvedScales{}, errors.New("invalid core.cache.warmup.keys.file_id")
		}
		if _, duplicate := seenKeys[key.FileID]; duplicate {
			return ResolvedScales{}, errors.New("duplicate core.cache.warmup key")
		}
		seenKeys[key.FileID] = struct{}{}
	}
	if !warmup.Sources.Inline && !warmup.Sources.PublishedArtifacts && len(warmup.Keys) > 0 {
		return ResolvedScales{}, errors.New("warmup keys require at least one enabled source")
	}
	for _, key := range warmup.Keys {
		for _, action := range key.Authorization.Actions {
			if action != "read" {
				return ResolvedScales{}, errors.New("invalid warmup authorization action")
			}
		}
	}

	if _, err := time.ParseDuration(c.Admin.Concurrency.HTTP.RequestTimeout); err != nil {
		return ResolvedScales{}, errors.New("invalid admin.concurrency.http.request_timeout")
	}
	if _, err := time.ParseDuration(c.Router.Concurrency.HTTP.RequestTimeout); err != nil {
		return ResolvedScales{}, errors.New("invalid router.concurrency.http.request_timeout")
	}
	shutdown, err := time.ParseDuration(c.HTTP.ShutdownTimeout)
	if err != nil || shutdown <= 0 || shutdown < resolved.AdminHTTP.RequestDuration() || shutdown < resolved.RouterHTTP.RequestDuration() {
		return ResolvedScales{}, errors.New("http.shutdown_timeout must cover both configured request timeouts")
	}

	durations := []struct {
		value     string
		allowZero bool
		target    *time.Duration
	}{
		{warmup.Timeout, false, &resolved.WarmupTimeouts.Timeout},
		{warmup.PerKeyTimeout, false, &resolved.WarmupTimeouts.PerKeyTimeout},
		{warmup.Retry.Backoff, false, &resolved.WarmupTimeouts.Backoff},
		{warmup.RefreshInterval, true, &resolved.WarmupTimeouts.RefreshInterval},
		{c.Core.Concurrency.Storage.OperationTimeout, false, &resolved.WarmupTimeouts.OperationTimeout},
		{c.Core.Concurrency.Cache.Coalesce.Timeout, false, &resolved.WarmupTimeouts.CoalesceTimeout},
		{c.Core.Concurrency.Invalidation.BatchWindow, false, &resolved.WarmupTimeouts.BatchWindow},
	}
	for _, item := range durations {
		value, err := time.ParseDuration(item.value)
		if err != nil || value < 0 || (value == 0 && !item.allowZero) {
			return ResolvedScales{}, errors.New("invalid cache/concurrency duration")
		}
		*item.target = value
	}
	if resolved.WarmupTimeouts.PerKeyTimeout >= resolved.WarmupTimeouts.Timeout || resolved.WarmupTimeouts.OperationTimeout >= resolved.AdminHTTP.RequestDuration() {
		return ResolvedScales{}, errors.New("operation timeout must leave budget for the request deadline")
	}

	if c.Router.Cache.Manifests.MaxEntries <= 0 || c.Router.Cache.Routes.MaxEntries <= 0 ||
		c.Router.Cache.Manifests.MaxBytes <= 0 || c.Router.Cache.Routes.MaxBytes <= 0 {
		return ResolvedScales{}, errors.New("invalid router cache entries")
	}
	for _, entry := range []BoundedCacheTarget{c.Router.Cache.Manifests, c.Router.Cache.Routes} {
		if _, err := time.ParseDuration(entry.TTL); err != nil {
			return ResolvedScales{}, errors.New("invalid router cache ttl")
		}
	}
	compression := c.Router.Cache.HTTP.Compression
	if compression.Enabled && (compression.MinBytes <= 0 || compression.MaxUncompressedBytes <= 0 || compression.MaxCompressedBytes <= 0 ||
		compression.MaxUncompressedBytes < compression.MinBytes || len(compression.Algorithms) == 0 || len(compression.MediaTypes) == 0) {
		return ResolvedScales{}, errors.New("invalid router compression configuration")
	}
	for _, algorithm := range compression.Algorithms {
		if algorithm != "gzip" {
			return ResolvedScales{}, errors.New("unsupported router compression algorithm")
		}
	}
	if c.Router.Cache.HTTP.Vary.LocaleFromCookie != "no_store" && c.Router.Cache.HTTP.Vary.LocaleFromCookie != "" {
		return ResolvedScales{}, errors.New("invalid router locale vary policy")
	}
	if c.Admin.Concurrency.HTTP.MaxInFlight <= 0 || c.Router.Concurrency.HTTP.MaxInFlight <= 0 || c.Router.Concurrency.Routes.MaxConcurrentResolutions <= 0 {
		return ResolvedScales{}, errors.New("invalid HTTP admission limits")
	}
	if c.Bus.Concurrency.MaxInFlight <= 0 {
		return ResolvedScales{}, errors.New("invalid bus admission limit")
	}
	if err := validateNamedAdmission(c.Router.Concurrency.HTTP.PerRoute, c.Router.Concurrency.HTTP.MaxInFlight, "router.concurrency.http.per_route"); err != nil {
		return ResolvedScales{}, err
	}
	if err := validateNamedAdmission(c.Bus.Concurrency.PerOperation, c.Bus.Concurrency.MaxInFlight, "bus.concurrency.per_operation"); err != nil {
		return ResolvedScales{}, err
	}
	cc := c.Core.Concurrency
	if cc.Storage.MaxConcurrentReads <= 0 || cc.Storage.MaxConcurrentAuthorizations <= 0 || cc.Storage.ReadReserve <= 0 ||
		cc.Cache.Shards <= 0 || cc.Cache.LRUPromotionInterval <= 0 || cc.Cache.Coalesce.MaxWaiters <= 0 ||
		cc.Invalidation.BatchSize <= 0 {
		return ResolvedScales{}, errors.New("invalid core concurrency limits")
	}
	if !cc.Cache.Coalesce.Enabled && cc.Cache.Coalesce.MaxWaiters > 0 {
		// Disabled coalescing still carries explicit, bounded configuration for
		// visibility; it is not used by the runtime path.
		_ = cc.Cache.Coalesce.MaxWaiters
	}
	if c.Loader.Concurrency.MaxConcurrentCommits <= 0 || c.Loader.Concurrency.MaxConcurrentSessionLookups <= 0 {
		return ResolvedScales{}, errors.New("invalid loader concurrency limits")
	}
	workerSum := 0
	resolvePool := func(p WorkerPoolConcurrency) (ResolvedWorkerPool, error) {
		if p.Limit <= 0 || p.Limit > 256 {
			return ResolvedWorkerPool{}, errors.New("invalid worker pool limit")
		}
		poll, err := time.ParseDuration(p.PollInterval)
		if err != nil || poll <= 0 {
			return ResolvedWorkerPool{}, errors.New("invalid worker pool poll interval")
		}
		timeout, err := time.ParseDuration(p.TaskTimeout)
		if err != nil || timeout <= 0 || timeout > 24*time.Hour {
			return ResolvedWorkerPool{}, errors.New("invalid worker pool task timeout")
		}
		lease, err := time.ParseDuration(p.LeaseDuration)
		if err != nil || lease < 3*time.Second || lease > time.Hour {
			return ResolvedWorkerPool{}, errors.New("invalid worker pool lease duration")
		}
		if poll >= lease {
			return ResolvedWorkerPool{}, errors.New("worker pool poll interval must be shorter than lease duration")
		}
		workerSum += p.Limit
		return ResolvedWorkerPool{Enabled: p.Enabled, Limit: p.Limit, PollInterval: poll, TaskTimeout: timeout, LeaseDuration: lease}, nil
	}
	archive, err := resolvePool(c.Worker.Archive.Concurrency)
	if err != nil {
		return ResolvedScales{}, err
	}
	media, err := resolvePool(c.Worker.Media.Concurrency)
	if err != nil {
		return ResolvedScales{}, err
	}
	prerender, err := resolvePool(c.Worker.Prerender.Concurrency)
	if err != nil {
		return ResolvedScales{}, err
	}
	publication, err := resolvePool(c.Worker.Publication.Concurrency)
	if err != nil {
		return ResolvedScales{}, err
	}
	resolved.WorkerPools = ResolvedWorkers{Archive: archive, Media: media, Prerender: prerender, Publication: publication}
	requiredConnections := int64(1 + 2*c.Loader.Concurrency.MaxConcurrentCommits + 2*workerSum + cc.Storage.ReadReserve)
	if int64(c.Core.MaxConnections) < requiredConnections {
		return ResolvedScales{}, fmt.Errorf("core.max_connections %d is below required budget %d", c.Core.MaxConnections, requiredConnections)
	}
	return resolved, nil
}

func validateCacheTarget(cache CacheTarget, name string) error {
	if cache.MaxBytes <= 0 || cache.TTL == "" || cache.MaxObjectBytes <= 0 || cache.MaxObjectBytes > cache.MaxBytes ||
		cache.AccessCache.MaxEntries <= 0 || cache.AccessCache.MaxBytes <= 0 || cache.AccessCache.TTL == "" {
		return fmt.Errorf("invalid %s", name)
	}
	if _, err := time.ParseDuration(cache.TTL); err != nil {
		return fmt.Errorf("invalid %s.ttl", name)
	}
	if _, err := time.ParseDuration(cache.AccessCache.TTL); err != nil {
		return fmt.Errorf("invalid %s.access_cache.ttl", name)
	}
	return nil
}

func validateNamedAdmission(setting NamedAdmission, global int, name string) error {
	if setting.DefaultMaxInFlight <= 0 || setting.DefaultMaxInFlight > global {
		return fmt.Errorf("invalid %s.default_max_in_flight", name)
	}
	seen := make(map[string]struct{}, len(setting.Overrides))
	for _, override := range setting.Overrides {
		if override.ID == "" || override.MaxInFlight <= 0 || override.MaxInFlight > global {
			return fmt.Errorf("invalid %s override", name)
		}
		if _, duplicate := seen[override.ID]; duplicate {
			return fmt.Errorf("duplicate %s override", name)
		}
		seen[override.ID] = struct{}{}
	}
	return nil
}

func resolvedScalesTimeout(scales ResolvedScales, kind string) time.Duration {
	if kind == "coalesce" {
		return scales.WarmupTimeouts.CoalesceTimeout
	}
	return scales.WarmupTimeouts.BatchWindow
}

func (a HTTPAdmission) RequestDuration() time.Duration {
	value, err := time.ParseDuration(a.RequestTimeout)
	if err != nil {
		return 0
	}
	return value
}
