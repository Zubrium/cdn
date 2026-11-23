package cache

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// EpochSource is the internal Core port used to verify that a cached entry is
// still backed by the authoritative database state.
type EpochSource interface {
	// AccessState reads epoch and revision in one database snapshot so a grant
	// change between two separate reads cannot validate a stale authorization.
	AccessState(context.Context, string, string) (int64, int64, error)
	// Revision reads only the authoritative file revision for metadata checks.
	Revision(context.Context, string) (int64, error)
}

// Authorizer is the existing Core authorization operation. Cached values are
// hints only; every miss and every stale hit must call it again.
type Authorizer interface {
	Authorize(ctx context.Context, principalID, fileID, action string) (Authorization, error)
}

// Authorization is the bounded authorization-cache value. It is usable only
// while both revision and access_epoch still match the database.
type Authorization struct {
	PrincipalID string
	FileID      string
	Action      string
	Revision    int64
	AccessEpoch int64
	Groups      []string
}

// Metadata is the small value-copy metadata-cache entry.
type Metadata struct {
	FileID      string
	Key         string
	Revision    int64
	Size        int
	Ready       bool
	Groups      []string
	Metadata    map[string]string
	SHA256      string
	ContentType string
	State       string
}

// StoreConfig contains every runtime bound explicitly. Missing values are
// rejected; no hidden default is substituted.
type StoreConfig struct {
	BytesMaxBytes           int
	MetadataMaxBytes        int
	MaxObjectBytes          int
	BytesTTL                time.Duration
	MetadataTTL             time.Duration
	Shards                  int
	PromotionInterval       int
	CoalesceEnabled         bool
	CoalesceMaxWaiters      int
	CoalesceTimeout         time.Duration
	AccessMaxEntries        int
	AccessMaxBytes          int
	AccessTTL               time.Duration
	InvalidationBatchSize   int
	InvalidationBatchWindow time.Duration
	Epoch                   EpochSource
	Observer                Observer
	CacheFactory            CacheFactory
}

// ValidateBounds checks operator-supplied limits before any listener starts.
// Internal dependencies are injected later by the owning module and are
// checked by Validate when the store is constructed.
func (c StoreConfig) ValidateBounds() error {
	if c.BytesMaxBytes <= 0 || c.MetadataMaxBytes <= 0 || c.MaxObjectBytes <= 0 {
		return errors.New("cache byte budgets must be positive")
	}
	if c.MaxObjectBytes > c.BytesMaxBytes {
		return errors.New("max_object_bytes cannot exceed bytes cache budget")
	}
	if c.BytesTTL <= 0 || c.MetadataTTL <= 0 || c.AccessTTL <= 0 {
		return errors.New("cache TTLs must be positive")
	}
	if c.Shards <= 0 || c.PromotionInterval <= 0 {
		return errors.New("cache sharding bounds must be positive")
	}
	if c.CoalesceMaxWaiters <= 0 || c.CoalesceTimeout <= 0 {
		return errors.New("cache coalescing bounds must be explicit")
	}
	if c.AccessMaxEntries <= 0 || c.AccessMaxBytes <= 0 {
		return errors.New("authorization cache bounds must be positive")
	}
	if c.InvalidationBatchSize <= 0 || c.InvalidationBatchWindow <= 0 {
		return errors.New("invalidation batch bounds must be explicit")
	}
	return nil
}

func (c StoreConfig) Validate() error {
	if err := c.ValidateBounds(); err != nil {
		return err
	}
	if c.Epoch == nil || c.Observer == nil || c.CacheFactory == nil {
		return errors.New("cache epoch source, observer and cache factory are required")
	}
	return nil
}

// StoreStats is the low-cardinality cache inventory used by health checks and
// warm-up tests.
type StoreStats struct {
	BytesEntries         int
	MetadataEntries      int
	AuthorizationEntries int
	AuthIndexEntries     int
	BytesSize            int
	MetadataSize         int
	AuthorizationSize    int
}

// Store owns Core's three bounded process-local caches.
type Store struct {
	cfg                 StoreConfig
	bytes               BoundedCache
	metadata            BoundedCache
	authorization       BoundedCache
	observer            Observer
	mu                  sync.Mutex
	authKeysByFile      map[string]map[string]struct{}
	authKeyCount        int
	coalescingPublished coalescingCounts
}

type coalescingCounts struct {
	leader atomic.Int64
	joined atomic.Int64
	failed atomic.Int64
}

// NewStore constructs the bounded caches from fully specified configuration.
func NewStore(cfg StoreConfig) (*Store, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	store := &Store{
		cfg:            cfg,
		observer:       cfg.Observer,
		authKeysByFile: make(map[string]map[string]struct{}),
	}
	common := CacheBounds{
		Shards: cfg.Shards, PromotionInterval: cfg.PromotionInterval,
		CoalesceEnabled: cfg.CoalesceEnabled, CoalesceMaxWaiters: cfg.CoalesceMaxWaiters,
		CoalesceTimeout: cfg.CoalesceTimeout,
	}
	byteBounds := common
	byteBounds.MaxBytes = cfg.BytesMaxBytes
	metadataBounds := common
	metadataBounds.MaxBytes = cfg.MetadataMaxBytes
	authorizationBounds := common
	authorizationBounds.MaxEntries = cfg.AccessMaxEntries
	authorizationBounds.MaxBytes = cfg.AccessMaxBytes
	store.bytes = cfg.CacheFactory.NewBoundedCache(byteBounds)
	store.metadata = cfg.CacheFactory.NewBoundedCache(metadataBounds)
	store.authorization = cfg.CacheFactory.NewBoundedCache(authorizationBounds)
	if store.bytes == nil || store.metadata == nil || store.authorization == nil {
		return nil, errors.New("cache factory returned a nil cache")
	}
	store.initMetrics()
	return store, nil
}

// MaxObjectBytes exposes the explicit byte-object ceiling to the warm-up
// runner so oversized objects are never buffered into the byte cache.
func (s *Store) MaxObjectBytes() int { return s.cfg.MaxObjectBytes }

// Stats reports the current bounded cache inventory.
func (s *Store) Stats() StoreStats {
	bytes := s.bytes.Stats()
	metadata := s.metadata.Stats()
	authorization := s.authorization.Stats()
	s.mu.Lock()
	indexEntries := s.authKeyCount
	s.mu.Unlock()
	return StoreStats{
		BytesEntries:         bytes.Entries,
		MetadataEntries:      metadata.Entries,
		AuthorizationEntries: authorization.Entries,
		AuthIndexEntries:     indexEntries,
		BytesSize:            bytes.Bytes,
		MetadataSize:         metadata.Bytes,
		AuthorizationSize:    authorization.Bytes,
	}
}

// Authorize returns a cached authorization only after revision and epoch are
// verified against PostgreSQL. Any verification error is a miss followed by a
// fresh authoritative check; it is never treated as permission.
func (s *Store) Authorize(ctx context.Context, principalID, fileID, action string, authorizer Authorizer) (Authorization, error) {
	if authorizer == nil {
		return Authorization{}, errors.New("nil authorizer")
	}
	key := authorizationKey(principalID, fileID, action)
	if view, ok := s.authorization.GetView(key); ok {
		var cached Authorization
		valid := json.Unmarshal(view.Bytes(), &cached) == nil
		if valid {
			valid = s.authorizationFresh(ctx, cached, principalID, fileID, action)
		}
		if valid {
			s.operation(cacheAuthorization, "HIT")
			s.publishCoalescing()
			return cached, nil
		}
		s.authorization.Delete(key)
		s.removeAuthorizationKey(fileID, key)
		s.operation(cacheAuthorization, "INVALIDATED")
	}
	s.operation(cacheAuthorization, "MISS")
	value, err := s.authorization.GetOrLoad(key, s.cfg.AccessTTL, func() ([]byte, error) {
		auth, err := authorizer.Authorize(ctx, principalID, fileID, action)
		if err != nil {
			return nil, err
		}
		if auth.PrincipalID != principalID || auth.FileID != fileID || auth.Action != action {
			return nil, errors.New("authorization identity mismatch")
		}
		epoch, revision, err := s.cfg.Epoch.AccessState(ctx, principalID, fileID)
		if err != nil {
			return nil, err
		}
		if epoch != auth.AccessEpoch || revision != auth.Revision {
			return nil, errors.New("authorization state changed during check")
		}
		return json.Marshal(auth)
	})
	s.publishCoalescing()
	if err != nil {
		return Authorization{}, err
	}
	var auth Authorization
	if err := json.Unmarshal(value, &auth); err != nil {
		return Authorization{}, err
	}
	s.addAuthorizationKey(fileID, key)
	s.refreshGauges()
	return auth, nil
}

// GetMetadata returns a metadata value copy only when its revision is still
// authoritative. A stale or unverifiable entry is removed and reported as a
// miss.
func (s *Store) GetMetadata(ctx context.Context, fileID string) (Metadata, bool) {
	view, ok := s.metadata.GetView(fileID)
	if !ok {
		s.operation(cacheMetadata, "MISS")
		return Metadata{}, false
	}
	var metadata Metadata
	if err := json.Unmarshal(view.Bytes(), &metadata); err != nil || metadata.FileID != fileID {
		s.metadata.Delete(fileID)
		s.bytes.Delete(fileID)
		s.operation(cacheMetadata, "INVALIDATED")
		return Metadata{}, false
	}
	revision, err := s.cfg.Epoch.Revision(ctx, fileID)
	if err != nil || revision != metadata.Revision {
		s.metadata.Delete(fileID)
		s.bytes.Delete(fileID)
		s.operation(cacheMetadata, "INVALIDATED")
		return Metadata{}, false
	}
	s.operation(cacheMetadata, "HIT")
	return metadata, true
}

// GetBytes returns an immutable zero-copy view after the paired metadata
// revision has been verified. Larger objects are never served from this cache.
func (s *Store) GetBytes(ctx context.Context, fileID string) (ByteView, bool) {
	metadata, ok := s.GetMetadata(ctx, fileID)
	if !ok {
		s.bytes.Delete(fileID)
		return ByteView{}, false
	}
	view, ok := s.bytes.GetView(fileID)
	if !ok {
		s.operation(cacheBytes, "MISS")
		return ByteView{}, false
	}
	if len(view.Bytes()) != metadata.Size {
		s.invalidateFile(fileID)
		return ByteView{}, false
	}
	s.operation(cacheBytes, "HIT")
	return view, true
}

// PutMetadata stores metadata only after its revision matches the database.
func (s *Store) PutMetadata(ctx context.Context, metadata Metadata) error {
	if metadata.FileID == "" || metadata.Revision <= 0 || metadata.Size < 0 || !metadata.Ready {
		return errors.New("invalid metadata cache entry")
	}
	revision, err := s.cfg.Epoch.Revision(ctx, metadata.FileID)
	if err != nil || revision != metadata.Revision {
		return errors.New("metadata revision is not authoritative")
	}
	value, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	s.metadata.Put(metadata.FileID, value, s.cfg.MetadataTTL)
	s.refreshGauges()
	return nil
}

// PutBytes stores raw object bytes only for verified, bounded metadata.
func (s *Store) PutBytes(ctx context.Context, fileID string, revision int64, data []byte) error {
	if len(data) > s.cfg.MaxObjectBytes {
		return errors.New("object exceeds byte cache limit")
	}
	metadata, ok := s.GetMetadata(ctx, fileID)
	if !ok || metadata.Revision != revision || metadata.Size != len(data) {
		return errors.New("bytes do not match verified metadata")
	}
	s.bytes.Put(fileID, data, s.cfg.BytesTTL)
	s.refreshGauges()
	return nil
}

// Notify applies one targeted invalidation from the PostgreSQL listener.
func (s *Store) Notify(fileID string) {
	s.NotifyBatch([]string{fileID})
}

// NotifyBatch applies a bounded group of notifications in one step. The
// configured window is an upper bound: applying synchronously is always within
// it. A set larger than the explicit batch size clears every cache so that a
// stale entry cannot survive dropped notifications.
func (s *Store) NotifyBatch(fileIDs []string) {
	unique := make(map[string]struct{}, len(fileIDs))
	for _, fileID := range fileIDs {
		if fileID != "" {
			unique[fileID] = struct{}{}
		}
	}
	if len(unique) == 0 {
		return
	}
	if len(unique) > s.cfg.InvalidationBatchSize {
		s.clearAll()
		s.invalidationBatch("OVERFLOW")
		return
	}
	for fileID := range unique {
		s.invalidateFile(fileID)
	}
	s.invalidationBatch("APPLIED")
}

// OnReconnect clears every local cache because notifications may have been
// missed while the LISTEN session was down.
func (s *Store) OnReconnect() {
	s.clearAll()
	s.invalidationBatch("CLEARED")
}

func (s *Store) clearAll() {
	s.bytes.Clear()
	s.metadata.Clear()
	s.authorization.Clear()
	s.mu.Lock()
	s.authKeysByFile = make(map[string]map[string]struct{})
	s.authKeyCount = 0
	s.mu.Unlock()
	for _, name := range metricCaches {
		s.operation(name, "INVALIDATED")
	}
	s.refreshGauges()
}

func (s *Store) authorizationFresh(ctx context.Context, auth Authorization, principalID, fileID, action string) bool {
	if auth.PrincipalID != principalID || auth.FileID != fileID || auth.Action != action {
		return false
	}
	epoch, revision, err := s.cfg.Epoch.AccessState(ctx, principalID, fileID)
	return err == nil && epoch == auth.AccessEpoch && revision == auth.Revision
}

func authorizationKey(principalID, fileID, action string) string {
	return strings.Join([]string{principalID, fileID, action}, "\x00")
}

func (s *Store) addAuthorizationKey(fileID, key string) {
	s.mu.Lock()
	keys := s.authKeysByFile[fileID]
	_, alreadyIndexed := keys[key]
	count := s.authKeyCount
	s.mu.Unlock()
	if alreadyIndexed {
		return
	}
	// The index cannot outlive the authorization cache's entry budget. If a
	// bounded LRU has evicted entries, reset the whole index before adding the
	// new key; a stale index entry is harmless but an unbounded one is not.
	if count >= s.cfg.AccessMaxEntries {
		s.authorization.Clear()
		s.mu.Lock()
		s.authKeysByFile = make(map[string]map[string]struct{})
		s.authKeyCount = 0
		s.mu.Unlock()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	keys = s.authKeysByFile[fileID]
	if keys == nil {
		keys = make(map[string]struct{})
		s.authKeysByFile[fileID] = keys
	}
	if _, exists := keys[key]; !exists {
		keys[key] = struct{}{}
		s.authKeyCount++
	}
}

func (s *Store) removeAuthorizationKey(fileID, key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if keys := s.authKeysByFile[fileID]; keys != nil {
		if _, exists := keys[key]; exists {
			delete(keys, key)
			s.authKeyCount--
		}
		if len(keys) == 0 {
			delete(s.authKeysByFile, fileID)
		}
	}
}

func (s *Store) invalidateFile(fileID string) {
	s.bytes.Delete(fileID)
	s.metadata.Delete(fileID)
	s.mu.Lock()
	keys := append([]string(nil), keysFromSet(s.authKeysByFile[fileID])...)
	delete(s.authKeysByFile, fileID)
	s.authKeyCount -= len(keys)
	if s.authKeyCount < 0 {
		s.authKeyCount = 0
	}
	s.mu.Unlock()
	for _, key := range keys {
		s.authorization.Delete(key)
	}
	s.operation(cacheBytes, "INVALIDATED")
	s.operation(cacheMetadata, "INVALIDATED")
	if len(keys) > 0 {
		s.operation(cacheAuthorization, "INVALIDATED")
	}
	s.refreshGauges()
}

func keysFromSet(set map[string]struct{}) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	return keys
}

// publishCoalescing publishes only counters not yet emitted by another
// concurrent caller. Taking a Stats snapshot before GetView and computing a
// per-caller delta double-counts waiters that joined an in-flight leader.
func (s *Store) publishCoalescing() {
	stats := s.authorization.Stats()
	leader := publishCounter(&s.coalescingPublished.leader, stats.Leader)
	joined := publishCounter(&s.coalescingPublished.joined, stats.Joined)
	failed := publishCounter(&s.coalescingPublished.failed, stats.Failed)
	s.coalescing(cacheAuthorization, leader, joined, failed)
}

// publishCounter advances a monotonic cursor with CAS so concurrent callers
// cannot publish the same increment twice or roll the cursor backwards.
func publishCounter(cursor *atomic.Int64, observed int64) int64 {
	for {
		current := cursor.Load()
		if observed <= current {
			return 0
		}
		if cursor.CompareAndSwap(current, observed) {
			return observed - current
		}
	}
}
