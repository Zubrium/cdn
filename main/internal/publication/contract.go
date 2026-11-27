// Package publication owns the trusted, server-side publication fan-out. Its
// root task only validates a server-owned payload and declares class-scoped
// children; engine work executes in isolated workers assembled by main.
package publication

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"github.com/Zubrium/cdn/pkg/contracts"
)

const (
	moduleID  = "publication"
	namespace = "main/publication"
	scopeType = "global"
	scopeID   = "global"

	recordTypeRun      = "publication_run"
	recordTypeTarget   = "publication_target"
	recordTypeArtifact = "publication_artifact"

	stateStarting = "starting"
	statePending  = "pending"
	stateRunning  = "running"
	stateBuilt    = "built"
	stateSuccess  = "success"
	stateFailed   = "failed"

	maxTargets = 256
)

var (
	ErrUnavailable = errors.New("publication fan-out capability is unavailable")
	ErrInvalid     = errors.New("invalid publication fan-out input")
	ErrConflict    = errors.New("publication fan-out revision conflict")
	ErrStale       = errors.New("stale publication build result")
	runKeyPattern  = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	keyPartPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{16,128}$`)
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

// WorkflowStarter is the narrow, module-scoped port used to start the root
// task. It carries no Core runtime, capability or engine handle.
type WorkflowStarter interface {
	Start(context.Context, contracts.WorkflowStartInput) (contracts.WorkflowStartReceipt, error)
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

// Config is the complete set of trusted capabilities required by the fan-out.
// Engine credentials and channel handles stay in the composition root.
type Config struct {
	Store       contracts.RecordDataStore
	Starter     WorkflowStarter
	Targets     []Target
	MaxAttempts int
}

// Result is the durable partial result of one publication run.
type Result struct {
	RunID           string         `json:"run_id"`
	WorkflowStarted bool           `json:"workflow_started"`
	State           string         `json:"state"`
	Targets         []TargetResult `json:"targets"`
}

type TargetResult struct {
	RouteID                string `json:"route_id"`
	Locale                 string `json:"locale"`
	State                  string `json:"state"`
	BuildID                string `json:"build_id,omitempty"`
	ArtifactManifestFileID string `json:"artifact_manifest_file_id,omitempty"`
	ErrorCode              string `json:"error_code,omitempty"`
}

// Service coordinates durable workflow starts and publication records. It
// stores only neutral record data and a module-scoped workflow starter port.
type Service struct {
	config Config
	target []Target
}

// NewService fails closed until composition supplies every trusted capability.
func NewService(config Config) (*Service, error) {
	if config.Store == nil || len(config.Targets) == 0 ||
		len(config.Targets) > maxTargets || config.MaxAttempts < 1 || config.MaxAttempts > 100 {
		return nil, ErrUnavailable
	}
	seen := make(map[string]struct{}, len(config.Targets))
	for _, target := range config.Targets {
		if contracts.ValidateWorkflowLocale(target.Locale) != nil ||
			!runKeyPattern.MatchString(target.RouteID) || !runKeyPattern.MatchString(target.PageSnapshotID) {
			return nil, ErrInvalid
		}
		key := target.RouteID + "\x00" + target.Locale
		if _, duplicate := seen[key]; duplicate {
			return nil, ErrInvalid
		}
		seen[key] = struct{}{}
	}
	return &Service{config: config, target: append([]Target(nil), config.Targets...)}, nil
}

func validateTrigger(trigger Trigger) (string, error) {
	if contracts.ValidateWorkflowLocale(trigger.Locale) != nil ||
		trigger.ContentPublicationRevision < 0 || trigger.SystemModelRevision < 0 ||
		!keyPartPattern.MatchString(trigger.IdempotencyKey) {
		return "", fmt.Errorf("%w: invalid publication trigger", ErrInvalid)
	}
	return trigger.Locale, nil
}
