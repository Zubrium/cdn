// Package publication owns the trusted, server-side publication fan-out that
// connects a ready Website locale artifact with independently activated Router
// (route, locale) targets. No visitor input enters this package.
package publication

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	core "github.com/Zubrium/cdn/core/presentation"
	contentdomain "github.com/Zubrium/cdn/engines/content/domain"
	routerapi "github.com/Zubrium/cdn/engines/router/presentation"
	websiteapp "github.com/Zubrium/cdn/engines/website/application"
	websiteapi "github.com/Zubrium/cdn/engines/website/presentation"
	"github.com/Zubrium/cdn/pkg/contracts"
)

const (
	moduleID  = "publication"
	namespace = "main/publication"
	scopeType = "branch"

	recordTypeRun    = "publication_run"
	recordTypeTarget = "publication_target"

	statePending = "pending"
	stateRunning = "running"
	stateSuccess = "success"
	stateFailed  = "failed"

	maxTargets = 256
)

var (
	ErrUnavailable = errors.New("publication fan-out capability is unavailable")
	ErrInvalid     = errors.New("invalid publication fan-out input")
	ErrConflict    = errors.New("publication fan-out revision conflict")
	ErrStale       = errors.New("stale publication build result")
	runKeyPattern  = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	keyPartPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)
)

// Target is one trusted (route, page snapshot, locale) publication slot. It is
// declared by startup configuration in the composition root; Website never
// selects a route and Router never sees a page or snapshot identity.
type Target struct {
	RouteID        string `json:"route_id"`
	PageSnapshotID string `json:"page_snapshot_id"`
	Locale         string `json:"locale"`
}

// Trigger is the opaque, server-owned Content publication signal. Callers may
// supply only an idempotency key and authoritative revision values; route and
// target selection remains in the trusted registry.
type Trigger struct {
	Locale                     string `json:"locale"`
	ContentPublicationRevision int64  `json:"content_publication_revision"`
	SystemModelRevision        int64  `json:"system_model_revision,omitempty"`
	IdempotencyKey             string `json:"-"`
}

func ModuleDataDescriptor() contracts.RecordDescriptor {
	return contracts.RecordDescriptor{
		OwnerID: moduleID,
		Namespaces: []contracts.RecordNamespace{{
			Name:       namespace,
			ScopeTypes: []string{scopeType},
			Fields: map[string]contracts.RecordSelectorField{
				"record_type": {Type: contracts.RecordSelectorString},
				"run_id":      {Type: contracts.RecordSelectorString},
				"state":       {Type: contracts.RecordSelectorString},
				"locale":      {Type: contracts.RecordSelectorString},
			},
		}},
	}
}

// Module satisfies Core's generic component registration without exposing any
// engine-specific operation to other modules.
type Module struct{}

func (Module) ComponentDescriptor() contracts.ComponentDescriptor {
	return contracts.ComponentDescriptor{ID: moduleID, Kind: "composition", Version: 1}
}

// LocaleCatalog supplies the authoritative locale-catalog revision used by the
// versioned WEB-020 trusted build input.
type LocaleCatalog interface {
	LocaleCatalogRevision(context.Context, string) (int64, error)
}

// Website is the narrow, authenticated Website facade used by fan-out. It
// still resolves the complete trusted build input internally.
type Website interface {
	Authenticate(context.Context, string) (context.Context, error)
	GetSnapshot(context.Context, string) (websiteapi.Snapshot, error)
	BuildSnapshot(context.Context, websiteapi.BuildSnapshotRequest) (websiteapi.BuildJob, error)
	GetBuild(context.Context, string) (websiteapi.BuildJob, error)
}

// Router is the narrow, authenticated Router target-CAS facade. Locale values
// are catalog slots from the trusted registry, never visitor input.
type Router interface {
	Authenticate(context.Context, string) (context.Context, error)
	GetRoute(context.Context, string) (routerapi.Route, error)
	ReplaceLocaleTarget(context.Context, routerapi.ReplaceLocaleTargetRequest) (routerapi.Route, error)
}

// Config is the complete set of trusted capabilities required by the fan-out.
// Every field must be supplied by the static composition root.
type Config struct {
	BranchID          string
	Store             contracts.RecordDataStore
	Targets           []Target
	Website           Website
	WebsiteCredential string
	Router            Router
	RouterCredential  string
	FileIdentity      core.IdentityCapability
	Files             core.FilesCapability
	FileCredential    string
	LocaleCatalog     LocaleCatalog
	InputResolver     websiteapp.BuildInputResolver
	BuildTimeout      time.Duration
	PollInterval      time.Duration
}

// Result is the durable partial result of one publication run.
type Result struct {
	RunID   string         `json:"run_id"`
	State   string         `json:"state"`
	Targets []TargetResult `json:"targets"`
}

type TargetResult struct {
	RouteID                string `json:"route_id"`
	Locale                 string `json:"locale"`
	State                  string `json:"state"`
	BuildID                string `json:"build_id,omitempty"`
	ArtifactManifestFileID string `json:"artifact_manifest_file_id,omitempty"`
	ErrorCode              string `json:"error_code,omitempty"`
}

// Service coordinates durable rebuild and Router activation records.
type Service struct {
	config Config
	target []Target
}

// NewService fails closed until composition supplies every trusted capability.
func NewService(config Config) (*Service, error) {
	if config.BranchID == "" || config.Store == nil || config.Website == nil || config.Router == nil ||
		config.FileIdentity == nil || config.Files == nil || config.FileCredential == "" ||
		config.LocaleCatalog == nil || config.InputResolver == nil || config.WebsiteCredential == "" ||
		config.RouterCredential == "" || config.BuildTimeout <= 0 || config.PollInterval <= 0 ||
		len(config.Targets) == 0 || len(config.Targets) > maxTargets {
		return nil, ErrUnavailable
	}
	seen := make(map[string]struct{}, len(config.Targets))
	for _, target := range config.Targets {
		locale, err := contentdomain.CanonicalLocale(target.Locale)
		if err != nil || locale != target.Locale || target.RouteID == "" || target.PageSnapshotID == "" ||
			!runKeyPattern.MatchString(target.RouteID) || !runKeyPattern.MatchString(target.PageSnapshotID) {
			return nil, ErrInvalid
		}
		key := target.RouteID + "\x00" + locale
		if _, duplicate := seen[key]; duplicate {
			return nil, ErrInvalid
		}
		seen[key] = struct{}{}
	}
	return &Service{config: config, target: append([]Target(nil), config.Targets...)}, nil
}

func validateTrigger(trigger Trigger) (string, error) {
	locale, err := contentdomain.CanonicalLocale(trigger.Locale)
	if err != nil || locale != trigger.Locale || trigger.ContentPublicationRevision < 0 ||
		trigger.SystemModelRevision < 0 || !keyPartPattern.MatchString(trigger.IdempotencyKey) {
		return "", fmt.Errorf("%w: invalid publication trigger", ErrInvalid)
	}
	return locale, nil
}
