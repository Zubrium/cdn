package coordinator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	coreplatform "github.com/Zubrium/cdn/core/platform"
	"github.com/google/uuid"
)

const SitePublicationJobType = "site-publication/v1"

var ErrAutomaticPublicationDisabled = errors.New("automatic publication is disabled for this binding")
var ErrPublicationBindingMissing = errors.New("publication route is not configured")

type PublicationBinding struct {
	SiteID      uuid.UUID
	ContentID   uuid.UUID
	RouteID     uuid.UUID
	AutoPublish bool
}

type PublicationInput struct {
	SnapshotID       uuid.UUID
	SnapshotRevision int64
	ContentRevision  int64
	ProviderVersion  string
}

type PublicationStore interface {
	SchedulePublication(context.Context, coreplatform.CoordinatorPublicationRequest) (int64, coreplatform.CoordinatorJob, bool, error)
	ActivatePublication(context.Context, string, uuid.UUID, uuid.UUID, uuid.UUID, int64, int64, int64, uuid.UUID) (coreplatform.CoordinatorRouteTarget, error)
}

type PublicationSchedulerConfig struct {
	Store       PublicationStore
	SystemID    string
	Owner       uuid.UUID
	ConfigEpoch int64
	MaxAttempts int
	Bindings    []PublicationBinding
}

// PublicationScheduler snapshots publication bindings from validated startup
// configuration. Trigger calls provide a route ID, never caller-defined site,
// content, or policy fields.
type PublicationScheduler struct {
	store       PublicationStore
	systemID    string
	owner       uuid.UUID
	configEpoch int64
	maxAttempts int
	bindings    map[uuid.UUID]PublicationBinding
}

func NewPublicationScheduler(config PublicationSchedulerConfig) (*PublicationScheduler, error) {
	if isNilInterface(config.Store) || !coordinatorSystemIDPattern.MatchString(config.SystemID) || config.Owner == uuid.Nil ||
		config.ConfigEpoch <= 0 || config.MaxAttempts < 1 || config.MaxAttempts > 100 {
		return nil, ErrInvalid
	}
	bindings, err := indexPublicationBindings(config.Bindings)
	if err != nil {
		return nil, err
	}
	return &PublicationScheduler{store: config.Store, systemID: config.SystemID, owner: config.Owner,
		configEpoch: config.ConfigEpoch, maxAttempts: config.MaxAttempts, bindings: bindings}, nil
}

func indexPublicationBindings(configured []PublicationBinding) (map[uuid.UUID]PublicationBinding, error) {
	bindings := make(map[uuid.UUID]PublicationBinding, len(configured))
	for _, binding := range configured {
		if binding.SiteID == uuid.Nil || binding.ContentID == uuid.Nil || binding.RouteID == uuid.Nil {
			return nil, ErrInvalid
		}
		if _, exists := bindings[binding.RouteID]; exists {
			return nil, ErrInvalid
		}
		bindings[binding.RouteID] = binding
	}
	return bindings, nil
}

// ScheduleAutomatic is the entry point for Website/Content publication events.
// The YAML binding is the only source of the site/content IDs and auto-publish
// policy, so callers cannot widen the configured route set or bypass its flag.
func (s *PublicationScheduler) ScheduleAutomatic(ctx context.Context, routeID uuid.UUID, input PublicationInput) (int64, coreplatform.CoordinatorJob, bool, error) {
	if ctx == nil {
		return 0, coreplatform.CoordinatorJob{}, false, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return 0, coreplatform.CoordinatorJob{}, false, err
	}
	binding, ok := s.binding(routeID)
	if !ok {
		return 0, coreplatform.CoordinatorJob{}, false, ErrPublicationBindingMissing
	}
	if !binding.AutoPublish {
		return 0, coreplatform.CoordinatorJob{}, false, ErrAutomaticPublicationDisabled
	}
	return s.schedule(ctx, binding, input)
}

// ScheduleRebuild is reserved for an already-authorized Coordinator workflow,
// such as an explicit provider refresh. It still requires a route declared in
// startup YAML. Admin HTTP handlers must authorize before reaching this method.
func (s *PublicationScheduler) ScheduleRebuild(ctx context.Context, routeID uuid.UUID, input PublicationInput) (int64, coreplatform.CoordinatorJob, bool, error) {
	if ctx == nil {
		return 0, coreplatform.CoordinatorJob{}, false, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return 0, coreplatform.CoordinatorJob{}, false, err
	}
	binding, ok := s.binding(routeID)
	if !ok {
		return 0, coreplatform.CoordinatorJob{}, false, ErrPublicationBindingMissing
	}
	return s.schedule(ctx, binding, input)
}

func (s *PublicationScheduler) binding(routeID uuid.UUID) (PublicationBinding, bool) {
	if s == nil || routeID == uuid.Nil {
		return PublicationBinding{}, false
	}
	binding, ok := s.bindings[routeID]
	return binding, ok
}

func (s *PublicationScheduler) schedule(ctx context.Context, binding PublicationBinding, input PublicationInput) (int64, coreplatform.CoordinatorJob, bool, error) {
	if ctx == nil || s == nil || s.store == nil || !coordinatorSystemIDPattern.MatchString(s.systemID) || s.owner == uuid.Nil || s.configEpoch <= 0 || s.maxAttempts < 1 || s.maxAttempts > 100 ||
		binding.SiteID == uuid.Nil || binding.ContentID == uuid.Nil || binding.RouteID == uuid.Nil || input.SnapshotID == uuid.Nil ||
		input.SnapshotRevision <= 0 || input.ContentRevision <= 0 || len(input.ProviderVersion) > 256 || !utf8.ValidString(input.ProviderVersion) || strings.ContainsRune(input.ProviderVersion, '\x00') {
		return 0, coreplatform.CoordinatorJob{}, false, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return 0, coreplatform.CoordinatorJob{}, false, err
	}
	fingerprintInput := struct {
		ConfigEpoch      int64     `json:"config_epoch"`
		SiteID           uuid.UUID `json:"site_id"`
		ContentID        uuid.UUID `json:"content_id"`
		RouteID          uuid.UUID `json:"route_id"`
		SnapshotID       uuid.UUID `json:"snapshot_id"`
		SnapshotRevision int64     `json:"snapshot_revision"`
		ContentRevision  int64     `json:"content_revision"`
		ProviderVersion  string    `json:"provider_version"`
	}{s.configEpoch, binding.SiteID, binding.ContentID, binding.RouteID, input.SnapshotID, input.SnapshotRevision, input.ContentRevision, input.ProviderVersion}
	encoded, err := json.Marshal(fingerprintInput)
	if err != nil {
		return 0, coreplatform.CoordinatorJob{}, false, ErrInvalid
	}
	digest := sha256.Sum256(encoded)
	fingerprint := hex.EncodeToString(digest[:])
	jobID, err := uuid.NewRandom()
	if err != nil {
		return 0, coreplatform.CoordinatorJob{}, false, err
	}
	payload := struct {
		RouteID          string `json:"route_id"`
		SiteID           string `json:"site_id"`
		ContentID        string `json:"content_id"`
		SnapshotID       string `json:"snapshot_id"`
		SnapshotRevision int64  `json:"snapshot_revision"`
		ContentRevision  int64  `json:"content_revision"`
		ProviderVersion  string `json:"provider_version"`
	}{RouteID: binding.RouteID.String(), SiteID: binding.SiteID.String(), ContentID: binding.ContentID.String(),
		SnapshotID: input.SnapshotID.String(), SnapshotRevision: input.SnapshotRevision,
		ContentRevision: input.ContentRevision, ProviderVersion: input.ProviderVersion}
	jobPayload, err := json.Marshal(payload)
	if err != nil {
		return 0, coreplatform.CoordinatorJob{}, false, ErrInvalid
	}
	key := binding.RouteID.String() + ":" + fingerprint
	return s.store.SchedulePublication(ctx, coreplatform.CoordinatorPublicationRequest{
		SystemID: s.systemID, RouteID: binding.RouteID, Owner: s.owner, ConfigEpoch: s.configEpoch,
		Fingerprint: fingerprint, JobID: jobID, JobType: SitePublicationJobType, IdempotencyKey: key,
		Payload: jobPayload, MaxAttempts: s.maxAttempts,
	})
}

// PublicationBuilder is the typed bridge to the selected Website, Content,
// and Prerender implementations. Idempotency keys remain stable across job
// retries, so an external effect can be reconciled after a lost response.
type PublicationBuilder interface {
	BuildSiteBase(context.Context, string, uuid.UUID, uuid.UUID, int64, string) (uuid.UUID, error)
	RenderPublishedContent(context.Context, string, uuid.UUID, uuid.UUID, uuid.UUID, int64) (uuid.UUID, error)
}

type PublicationHandlerConfig struct {
	Store    PublicationStore
	Builder  PublicationBuilder
	SystemID string
	Bindings []PublicationBinding
}

type PublicationHandler struct {
	store    PublicationStore
	builder  PublicationBuilder
	systemID string
	bindings map[uuid.UUID]PublicationBinding
}

func NewPublicationHandler(config PublicationHandlerConfig) (*PublicationHandler, error) {
	if isNilInterface(config.Store) || isNilInterface(config.Builder) || !coordinatorSystemIDPattern.MatchString(config.SystemID) || len(config.Bindings) == 0 {
		return nil, ErrInvalid
	}
	bindings, err := indexPublicationBindings(config.Bindings)
	if err != nil {
		return nil, err
	}
	return &PublicationHandler{store: config.Store, builder: config.Builder, systemID: config.SystemID, bindings: bindings}, nil
}

type publicationJobPayload struct {
	RouteID          string `json:"route_id"`
	SiteID           string `json:"site_id"`
	ContentID        string `json:"content_id"`
	SnapshotID       string `json:"snapshot_id"`
	SnapshotRevision int64  `json:"snapshot_revision"`
	ContentRevision  int64  `json:"content_revision"`
	ProviderVersion  string `json:"provider_version"`
	Generation       int64  `json:"generation"`
}

func (h *PublicationHandler) Handle(ctx context.Context, job CoreJob) (json.RawMessage, error) {
	if ctx == nil || h == nil || isNilInterface(h.store) || isNilInterface(h.builder) || !coordinatorSystemIDPattern.MatchString(h.systemID) ||
		job.Type != SitePublicationJobType || job.SystemID != h.systemID || job.ID == uuid.Nil || job.LeaseOwner == uuid.Nil || job.ConfigEpoch <= 0 || job.FencingToken <= 0 || !utf8.Valid(job.Payload) {
		return nil, NewTaskFailure("invalid_publication_job", false)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(strings.NewReader(string(job.Payload)))
	decoder.DisallowUnknownFields()
	var input publicationJobPayload
	if err := decoder.Decode(&input); err != nil {
		return nil, NewTaskFailure("invalid_publication_job", false)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, NewTaskFailure("invalid_publication_job", false)
	}
	routeID, routeErr := uuid.Parse(input.RouteID)
	siteID, siteErr := uuid.Parse(input.SiteID)
	contentID, contentErr := uuid.Parse(input.ContentID)
	snapshotID, snapshotErr := uuid.Parse(input.SnapshotID)
	if routeErr != nil || siteErr != nil || contentErr != nil || snapshotErr != nil || routeID == uuid.Nil || siteID == uuid.Nil || contentID == uuid.Nil || snapshotID == uuid.Nil ||
		input.SnapshotRevision <= 0 || input.ContentRevision <= 0 || input.Generation <= 0 ||
		len(input.ProviderVersion) > 256 || !utf8.ValidString(input.ProviderVersion) || strings.ContainsRune(input.ProviderVersion, '\x00') {
		return nil, NewTaskFailure("invalid_publication_job", false)
	}
	binding, configured := h.bindings[routeID]
	if !configured || binding.SiteID != siteID || binding.ContentID != contentID {
		return nil, NewTaskFailure("invalid_publication_job", false)
	}
	base, err := h.builder.BuildSiteBase(ctx, job.ID.String()+":site-base", binding.SiteID, snapshotID, input.SnapshotRevision, input.ProviderVersion)
	if err != nil {
		return nil, publicationBuildError(err)
	}
	if base == uuid.Nil {
		return nil, NewTaskFailure("invalid_site_base", false)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	artifact, err := h.builder.RenderPublishedContent(ctx, job.ID.String()+":content-artifact", binding.SiteID, base, binding.ContentID, input.ContentRevision)
	if err != nil {
		return nil, publicationBuildError(err)
	}
	if artifact == uuid.Nil {
		return nil, NewTaskFailure("invalid_publication_artifact", false)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	_, err = h.store.ActivatePublication(ctx, h.systemID, routeID, job.ID, job.LeaseOwner, job.ConfigEpoch, job.FencingToken, input.Generation, artifact)
	if errors.Is(err, coreplatform.ErrCoordinatorStale) {
		result, _ := json.Marshal(map[string]string{"status": "stale", "route_id": routeID.String()})
		return result, nil
	}
	if err != nil {
		return nil, err
	}
	result, _ := json.Marshal(map[string]string{"status": "published", "route_id": routeID.String(), "artifact_file_id": artifact.String()})
	return result, nil
}

func publicationBuildError(err error) error {
	var failure *TaskFailure
	if errors.As(err, &failure) && failure != nil && taskErrorCodePattern.MatchString(failure.Code) {
		return failure
	}
	return fmt.Errorf("%w", NewTaskFailure("publication_build_failed", true))
}
