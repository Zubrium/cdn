// Package cache owns Core's bounded warm-up and authorization-cache policy.
// It is internal to Core: the public credential-bound session contract is not
// changed by this package.
package cache

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Mode selects the startup warm-up policy. The values mirror the target YAML
// contract from SPEC-caching.md without accepting YAML tags in this layer.
type Mode string

const (
	ModeStrict     Mode = "strict"
	ModeBestEffort Mode = "best_effort"
	ModeOff        Mode = "off"
)

// Tier controls whether a failed key blocks readiness.
type Tier string

const (
	TierRequired Tier = "REQUIRED"
	TierOptional Tier = "OPTIONAL"
)

// Retry bounds retries to transient source errors only.
type Retry struct {
	Attempts int
	Backoff  time.Duration
}

// Sources selects the allowed key sources. Published artifact IDs are mapped
// into Keys by composition; this package never discovers keys from requests.
type Sources struct {
	Inline             bool
	PublishedArtifacts bool
}

// Targets enables the warm-up targets owned by Core. Router targets are listed
// for the composition contract but are performed by Router's own cache.
type Targets struct {
	Authorization   bool
	Metadata        bool
	Bytes           bool
	RouterManifests bool
	RouterRoutes    bool
}

// PrincipalAction is one internal authorization to warm for a file key.
type PrincipalAction struct {
	PrincipalID string
	Action      string
}

// Key is one explicitly configured warm-up entry.
type Key struct {
	FileID         string
	Tier           Tier
	Authorizations []PrincipalAction
}

// Config is the validated Core warm-up configuration. No missing value is
// filled with an implicit runtime default.
type Config struct {
	Enabled         bool
	Mode            Mode
	Timeout         time.Duration
	PerKeyTimeout   time.Duration
	RefreshInterval time.Duration
	Concurrency     int
	MaxKeys         int
	Retry           Retry
	Sources         Sources
	Targets         Targets
	Keys            []Key
}

// Validate rejects incomplete or ambiguous warm-up configuration before any
// component starts.
func (c Config) Validate() error {
	if !c.Enabled || c.Mode == ModeOff {
		return nil
	}
	switch c.Mode {
	case ModeStrict, ModeBestEffort:
	default:
		return fmt.Errorf("invalid warm-up mode %q", c.Mode)
	}
	if c.Timeout <= 0 {
		return errors.New("warm-up timeout must be positive")
	}
	if c.PerKeyTimeout <= 0 {
		return errors.New("warm-up per-key timeout must be positive")
	}
	if c.Concurrency <= 0 {
		return errors.New("warm-up concurrency must be positive")
	}
	if c.MaxKeys <= 0 {
		return errors.New("warm-up max_keys must be positive")
	}
	if c.Retry.Attempts < 1 {
		return errors.New("warm-up retry attempts must be positive")
	}
	if c.Retry.Backoff <= 0 {
		return errors.New("warm-up retry backoff must be positive")
	}
	if !c.Sources.Inline && !c.Sources.PublishedArtifacts {
		return errors.New("at least one warm-up key source must be enabled")
	}
	if c.Sources.Inline && len(c.Keys) == 0 {
		return errors.New("inline warm-up source requires explicit keys")
	}
	if !c.Targets.Authorization && !c.Targets.Metadata && !c.Targets.Bytes &&
		!c.Targets.RouterManifests && !c.Targets.RouterRoutes {
		return errors.New("at least one warm-up target must be enabled")
	}
	for index, key := range c.Keys {
		if err := validateWarmupKey(key, c.Targets.Authorization); err != nil {
			return fmt.Errorf("warm-up key %d: %w", index, err)
		}
	}
	return nil
}

func validateWarmupKey(key Key, authorizationTarget bool) error {
	id, err := uuid.Parse(key.FileID)
	if err != nil || id == uuid.Nil {
		return errors.New("invalid file_id")
	}
	switch key.Tier {
	case TierRequired, TierOptional:
	default:
		return fmt.Errorf("invalid tier %q", key.Tier)
	}
	if len(key.Authorizations) == 0 && authorizationTarget {
		return errors.New("at least one principal/action is required")
	}
	for _, authorization := range key.Authorizations {
		principalID, err := uuid.Parse(authorization.PrincipalID)
		if err != nil || principalID == uuid.Nil {
			return errors.New("invalid principal ID")
		}
		if strings.TrimSpace(authorization.Action) == "" {
			return errors.New("authorization action is required")
		}
	}
	return nil
}
