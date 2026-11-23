package cache

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"
)

// MetadataSource returns metadata for an already authorized file.
type MetadataSource interface {
	Metadata(ctx context.Context, fileID string, auth Authorization) (Metadata, error)
}

// BytesSource returns file bytes for verified metadata. Larger objects are
// streamed by the caller and never enter this bounded cache.
type BytesSource interface {
	Bytes(ctx context.Context, fileID string, metadata Metadata) ([]byte, error)
}

// Dependencies injects existing Core operations. Composition owns the wiring;
// this package never opens PostgreSQL or accepts visitor input.
type Dependencies struct {
	Store      *Store
	Observer   Observer
	Authorizer Authorizer
	Metadata   MetadataSource
	Bytes      BytesSource
	Epoch      EpochSource
}

// Result reports startup readiness and the bounded work performed.
type Result struct {
	Ready  bool
	Keys   int
	Warmed int
	Failed int
}

// Runner executes the validated warm-up pipeline.
type Runner struct {
	cfg  Config
	deps Dependencies
	keys []Key
}

type keyOutcome struct {
	warmed int
	failed int
	err    error
}

// NewRunner validates configuration and prepares the deduplicated, sorted and
// trimmed key set before any component is started.
func NewRunner(cfg Config, deps Dependencies) (*Runner, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if deps.Store == nil || deps.Observer == nil || deps.Authorizer == nil ||
		deps.Metadata == nil || deps.Bytes == nil || deps.Epoch == nil {
		return nil, errors.New("warm-up dependencies are incomplete")
	}
	return &Runner{cfg: cfg, deps: deps, keys: prepareKeys(cfg)}, nil
}

func prepareKeys(cfg Config) []Key {
	seen := make(map[string]Key, len(cfg.Keys))
	for _, key := range cfg.Keys {
		if _, exists := seen[key.FileID]; !exists {
			seen[key.FileID] = key
		}
	}
	keys := make([]Key, 0, len(seen))
	for _, key := range seen {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].FileID < keys[j].FileID })
	if cfg.MaxKeys > 0 && len(keys) > cfg.MaxKeys {
		keys = keys[:cfg.MaxKeys]
	}
	return keys
}

// Run performs one bounded warm-up cycle. In strict mode a required-key error
// prevents readiness; optional failures are counted but do not block startup.
func (r *Runner) Run(ctx context.Context) (Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if !r.cfg.Enabled || r.cfg.Mode == ModeOff {
		return Result{Ready: true}, nil
	}
	runCtx := ctx
	if r.cfg.Timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, r.cfg.Timeout)
		defer cancel()
	}

	semaphore := make(chan struct{}, r.cfg.Concurrency)
	outcomes := make([]keyOutcome, len(r.keys))
	var wait sync.WaitGroup
	for index, key := range r.keys {
		wait.Add(1)
		go func(index int, key Key) {
			defer wait.Done()
			select {
			case semaphore <- struct{}{}:
			case <-runCtx.Done():
				r.warmupKey(key.Tier, cacheAuthorization, "FAILED")
				outcomes[index].err = runCtx.Err()
				return
			}
			defer func() { <-semaphore }()
			started := time.Now()
			warmed, failed, err := r.processKey(runCtx, key)
			outcomes[index] = keyOutcome{warmed: warmed, failed: failed, err: err}
			elapsed := time.Since(started)
			if r.cfg.Targets.Authorization {
				r.warmupDuration(cacheAuthorization, elapsed)
			}
			if r.cfg.Targets.Metadata {
				r.warmupDuration(cacheMetadata, elapsed)
			}
			if r.cfg.Targets.Bytes {
				r.warmupDuration(cacheBytes, elapsed)
			}
		}(index, key)
	}
	wait.Wait()

	result := Result{Ready: true, Keys: len(r.keys)}
	var failures []error
	for _, outcome := range outcomes {
		result.Warmed += outcome.warmed
		result.Failed += outcome.failed
		if outcome.err != nil {
			failures = append(failures, outcome.err)
		}
	}
	if r.cfg.Mode == ModeStrict && len(failures) > 0 {
		result.Ready = false
		return result, errors.Join(failures...)
	}
	return result, nil
}

// RefreshLoop repeats the same bounded pipeline without overlapping cycles.
// Context cancellation ends the loop cleanly.
func (r *Runner) RefreshLoop(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if r.cfg.RefreshInterval <= 0 || !r.cfg.Enabled || r.cfg.Mode == ModeOff {
		return nil
	}
	ticker := time.NewTicker(r.cfg.RefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			_, _ = r.Run(ctx)
		}
	}
}

func (r *Runner) processKey(ctx context.Context, key Key) (int, int, error) {
	keyCtx, cancel := context.WithTimeout(ctx, r.cfg.PerKeyTimeout)
	defer cancel()
	warmed, failed := 0, 0
	fail := func(cacheName string, cause error) (int, int, error) {
		r.warmupKey(key.Tier, cacheName, "FAILED")
		if key.Tier == TierRequired {
			return warmed, failed, cause
		}
		return warmed, failed + 1, nil
	}

	var auth Authorization
	for _, action := range key.Authorizations {
		current, err := r.authorizeWithRetry(keyCtx, key.FileID, action)
		if err != nil {
			return fail(cacheAuthorization, err)
		}
		if current.FileID != key.FileID {
			return fail(cacheAuthorization, errors.New("authorization file identity mismatch"))
		}
		auth = current
		if r.cfg.Targets.Authorization {
			warmed++
		}
	}
	if r.cfg.Targets.Authorization {
		r.warmupKey(key.Tier, cacheAuthorization, "WARMED")
	} else {
		r.warmupKey(key.Tier, cacheAuthorization, "SKIPPED")
	}

	var metadata Metadata
	if r.cfg.Targets.Metadata {
		loaded, err := r.deps.Metadata.Metadata(keyCtx, key.FileID, auth)
		if err != nil {
			return fail(cacheMetadata, err)
		}
		if err := validateMetadata(loaded, key.FileID, auth); err != nil {
			return fail(cacheMetadata, err)
		}
		if err := r.deps.Store.PutMetadata(keyCtx, loaded); err != nil {
			return fail(cacheMetadata, err)
		}
		metadata = loaded
		warmed++
		r.warmupKey(key.Tier, cacheMetadata, "WARMED")
	} else {
		r.warmupKey(key.Tier, cacheMetadata, "SKIPPED")
	}

	if !r.cfg.Targets.Bytes {
		r.warmupKey(key.Tier, cacheBytes, "SKIPPED")
		return warmed, failed, nil
	}
	if !metadata.Ready {
		return fail(cacheBytes, errors.New("metadata is not ready for byte warm-up"))
	}
	if metadata.Size > r.deps.Store.MaxObjectBytes() {
		r.warmupKey(key.Tier, cacheBytes, "SKIPPED")
		return warmed, failed, nil
	}
	data, err := r.deps.Bytes.Bytes(keyCtx, key.FileID, metadata)
	if err != nil {
		return fail(cacheBytes, err)
	}
	if len(data) != metadata.Size {
		return fail(cacheBytes, errors.New("byte length does not match verified metadata"))
	}
	if err := r.deps.Store.PutBytes(keyCtx, key.FileID, metadata.Revision, data); err != nil {
		return fail(cacheBytes, err)
	}
	warmed++
	r.warmupKey(key.Tier, cacheBytes, "WARMED")
	return warmed, failed, nil
}

func (r *Runner) authorizeWithRetry(ctx context.Context, fileID string, action PrincipalAction) (Authorization, error) {
	var lastErr error
	for attempt := 0; attempt < r.cfg.Retry.Attempts; attempt++ {
		var auth Authorization
		var err error
		if r.cfg.Targets.Authorization {
			auth, err = r.deps.Store.Authorize(ctx, action.PrincipalID, fileID, action.Action, r.deps.Authorizer)
		} else {
			auth, err = r.authorizeDirect(ctx, fileID, action)
		}
		if err == nil {
			return auth, nil
		}
		lastErr = err
		if !isTransient(err) || attempt+1 >= r.cfg.Retry.Attempts {
			return Authorization{}, err
		}
		select {
		case <-ctx.Done():
			return Authorization{}, ctx.Err()
		case <-time.After(r.cfg.Retry.Backoff):
		}
	}
	return Authorization{}, lastErr
}

func (r *Runner) authorizeDirect(ctx context.Context, fileID string, action PrincipalAction) (Authorization, error) {
	auth, err := r.deps.Authorizer.Authorize(ctx, action.PrincipalID, fileID, action.Action)
	if err != nil {
		return Authorization{}, err
	}
	epoch, revision, err := r.deps.Epoch.AccessState(ctx, action.PrincipalID, fileID)
	if err != nil {
		return Authorization{}, err
	}
	if err := validateAuthorization(auth, action.PrincipalID, fileID, action.Action, epoch, revision); err != nil {
		return Authorization{}, err
	}
	return auth, nil
}

func validateAuthorization(auth Authorization, principalID, fileID, action string, epoch, revision int64) error {
	if auth.PrincipalID != principalID || auth.FileID != fileID || auth.Action != action {
		return errors.New("authorization identity mismatch")
	}
	if auth.AccessEpoch != epoch || auth.Revision != revision {
		return errors.New("authorization state changed during check")
	}
	return nil
}

func validateMetadata(metadata Metadata, fileID string, auth Authorization) error {
	if metadata.FileID != fileID || metadata.Revision != auth.Revision || !metadata.Ready || metadata.Size < 0 {
		return errors.New("metadata identity or revision mismatch")
	}
	for _, group := range metadata.Groups {
		visible := false
		for _, allowed := range auth.Groups {
			if group == allowed {
				visible = true
				break
			}
		}
		if !visible {
			return errors.New("metadata group is outside the authorization snapshot")
		}
	}
	return nil
}
