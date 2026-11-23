package router

import (
	"container/list"
	"context"
	"sync"
	"time"

	engine "github.com/Zubrium/cdn/engines/router/presentation"
	"github.com/Zubrium/cdn/pkg/contracts"
)

// CacheSettings is an explicit runtime bound. No value is inferred when a
// cache is enabled: composition must supply all three from startup YAML.
type CacheSettings struct {
	Enabled    bool
	MaxEntries int
	MaxBytes   int64
	TTL        time.Duration
}

type RouterCacheConfig struct {
	Manifests CacheSettings
	Routes    CacheSettings
}

func validCacheSettings(settings CacheSettings) bool {
	return settings.Enabled && settings.MaxEntries > 0 && settings.MaxBytes > 0 && settings.TTL > 0
}

type cacheRecord[K comparable, V any] struct {
	key     K
	value   V
	size    int64
	expires time.Time
	element *list.Element
}

type boundedCache[K comparable, V any] struct {
	mu            sync.Mutex
	settings      CacheSettings
	entries       map[K]*cacheRecord[K, V]
	order         *list.List
	bytes         int64
	hits          uint64
	misses        uint64
	invalidations uint64
	evictions     uint64
}

func newBoundedCache[K comparable, V any](settings CacheSettings) *boundedCache[K, V] {
	return &boundedCache[K, V]{settings: settings, entries: make(map[K]*cacheRecord[K, V]), order: list.New()}
}

func (cache *boundedCache[K, V]) get(key K) (V, bool) {
	var zero V
	cache.mu.Lock()
	defer cache.mu.Unlock()
	record, found := cache.entries[key]
	if !found {
		cache.misses++
		return zero, false
	}
	if !time.Now().Before(record.expires) {
		cache.remove(record)
		cache.invalidations++
		cache.misses++
		return zero, false
	}
	cache.order.MoveToFront(record.element)
	cache.hits++
	return record.value, true
}

func (cache *boundedCache[K, V]) put(key K, value V, size int64) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if prior, found := cache.entries[key]; found {
		cache.remove(prior)
	}
	for (cache.order.Len() >= cache.settings.MaxEntries || cache.bytes+size > cache.settings.MaxBytes) && cache.order.Len() > 0 {
		cache.remove(cache.order.Back().Value.(*cacheRecord[K, V]))
		cache.evictions++
	}
	if size > cache.settings.MaxBytes {
		return
	}
	record := &cacheRecord[K, V]{key: key, value: value, size: size, expires: time.Now().Add(cache.settings.TTL)}
	record.element = cache.order.PushFront(record)
	cache.entries[key] = record
	cache.bytes += size
}

func (cache *boundedCache[K, V]) invalidate(key K) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if record, found := cache.entries[key]; found {
		cache.remove(record)
		cache.invalidations++
	}
}

func (cache *boundedCache[K, V]) clear() {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	for key := range cache.entries {
		cache.remove(cache.entries[key])
	}
	cache.invalidations++
}

func (cache *boundedCache[K, V]) remove(record *cacheRecord[K, V]) {
	delete(cache.entries, record.key)
	cache.order.Remove(record.element)
	cache.bytes -= record.size
}

// stats are low-cardinality and deliberately omit keys and representation data.
func (cache *boundedCache[K, V]) stats() (entries, bytes int, hits, misses, invalidations, evictions uint64) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.order.Len(), int(cache.bytes), cache.hits, cache.misses, cache.invalidations, cache.evictions
}

type manifestCacheKey struct {
	fileID string
	digest string
}

type cachedManifest struct {
	parsed      parsedArtifact
	revision    int64
	sha256      string
	size        int64
	contentType string
}

type manifestCache struct {
	files CoreFiles
	cache *boundedCache[manifestCacheKey, cachedManifest]
}

type routeCacheKey struct {
	branch string
	host   string
	path   string
	method string
}

type cachedRoute struct {
	route       engine.Route
	fingerprint string
}

// RouteFingerprintSource is satisfied by the Router engine's authoritative
// route record lookup. A failure to check the revision closes the cache path.
type RouteFingerprintSource interface {
	ResolveRouteFingerprint(context.Context, string, string) (string, error)
}

type routeCache struct {
	branch       string
	routes       RouteResolver
	fingerprints RouteFingerprintSource
	cache        *boundedCache[routeCacheKey, cachedRoute]
}

func newManifestCache(files CoreFiles, settings CacheSettings) *manifestCache {
	return &manifestCache{files: files, cache: newBoundedCache[manifestCacheKey, cachedManifest](settings)}
}

// read validates Core file identity on every hit. A mismatch is a plain miss;
// an unavailable authoritative check is an error and therefore fail-closed.
func (cache *manifestCache) read(ctx context.Context, fileID string) (parsedArtifact, contracts.File, error) {
	stat, err := cache.files.StatFile(ctx, fileID)
	if err != nil {
		return parsedArtifact{}, contracts.File{}, mapDeliveryFileError(err)
	}
	key := manifestCacheKey{fileID: fileID, digest: stat.SHA256}
	entry, found := cache.cache.get(key)
	if found && entry.revision == stat.Revision && entry.sha256 == stat.SHA256 && entry.size == stat.Size && entry.contentType == stat.ContentType {
		return entry.parsed, stat, nil
	}
	if found {
		cache.cache.invalidate(key)
	}
	parsed, file, err := readArtifactManifest(ctx, cache.files, fileID)
	if err != nil {
		return parsedArtifact{}, contracts.File{}, err
	}
	cache.cache.put(key, cachedManifest{
		parsed: parsed, revision: file.Revision, sha256: file.SHA256,
		size: file.Size, contentType: file.ContentType,
	}, manifestCacheSize(key, parsed))
	return parsed, file, nil
}

func manifestCacheSize(key manifestCacheKey, parsed parsedArtifact) int64 {
	size := int64(len(key.fileID) + len(key.digest) + 128)
	size += int64(len(parsed.document.FileID) + len(parsed.document.SHA256) + len(parsed.document.ContentType) + 32)
	for path, asset := range parsed.assets {
		size += int64(len(path) + len(asset.FileID) + len(asset.SHA256) + len(asset.ContentType) + len(asset.CacheClass) + 64)
	}
	return size
}

func newRouteCache(branch string, routes RouteResolver, fingerprints RouteFingerprintSource, settings CacheSettings) *routeCache {
	return &routeCache{
		branch: branch, routes: routes, fingerprints: fingerprints,
		cache: newBoundedCache[routeCacheKey, cachedRoute](settings),
	}
}

func (cache *routeCache) resolve(ctx context.Context, key routeCacheKey, host, path string) (engine.Route, error) {
	if entry, found := cache.cache.get(key); found {
		current, err := cache.fingerprints.ResolveRouteFingerprint(ctx, cache.branch, entry.route.ID)
		if err != nil {
			return engine.Route{}, err
		}
		if current == entry.fingerprint {
			return entry.route, nil
		}
		cache.cache.invalidate(key)
	}
	route, err := cache.routes.ResolveRoute(ctx, host, path)
	if err != nil {
		return engine.Route{}, err
	}
	if route.BranchID != cache.branch {
		return engine.Route{}, engine.ErrNotFound
	}
	fingerprint, err := cache.fingerprints.ResolveRouteFingerprint(ctx, cache.branch, route.ID)
	if err != nil {
		return engine.Route{}, err
	}
	cache.cache.put(key, cachedRoute{route: route, fingerprint: fingerprint}, routeCacheSize(key, route))
	return route, nil
}

func routeCacheSize(key routeCacheKey, route engine.Route) int64 {
	size := int64(len(key.branch) + len(key.host) + len(key.path) + len(key.method) + len(route.ID) + 128)
	size += int64(len(route.Target.DefaultLocale))
	for _, locale := range route.Target.LocaleTargets {
		size += int64(len(locale.Locale) + len(locale.ArtifactManifestFileID) + 32)
	}
	return size
}
