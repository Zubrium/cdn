package workflow

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"

	core "github.com/Zubrium/cdn/core/presentation"
	"github.com/google/uuid"
)

const (
	ComponentID      = "engine.website"
	OperationID      = "build-page"
	ContractVersion  = 1
	RootFormat       = "website.build-root/v1"
	RenderPlanFormat = "render-plan/v1"

	prerenderComponentID     = "worker.prerender"
	prerenderOperationID     = "render-page"
	prerenderContractVersion = 1
	maxPayloadBytes          = 1 << 20
)

var (
	ErrInvalidConfiguration = errors.New("website build handler configuration is invalid")
	ErrInvalidTask          = errors.New("website build task is invalid")
	ErrInvalidPayload       = errors.New("website build payload is invalid")

	digestPattern        = regexp.MustCompile(`^[a-f0-9]{64}$`)
	localePattern        = regexp.MustCompile(`^[A-Za-z]{2,3}(?:-[A-Za-z0-9]{2,8})*$`)
	emissionPattern      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,255}$`)
	idempotencyKeyPrefix = "build:"
)

// Request is the immutable, typed workflow input created by Website
// application. It deliberately contains no route, URL, branch, record value,
// executable, command or storage path.
type Request struct {
	Version               int       `json:"version"`
	BuildID               uuid.UUID `json:"build_id"`
	SnapshotID            uuid.UUID `json:"snapshot_id"`
	SnapshotRevision      int64     `json:"snapshot_revision"`
	SnapshotDigest        string    `json:"snapshot_digest"`
	ContentRevision       int64     `json:"content_revision"`
	Locale                string    `json:"locale"`
	LocaleCatalogRevision int64     `json:"locale_catalog_revision"`
	InputDigest           string    `json:"input_digest"`
}

// RootResult carries only the stable emission identity needed to reconcile the
// child prerender result. Core generates the child task ID after this result is
// committed, so a task ID cannot be known here.
type RootResult struct {
	Format               string    `json:"format"`
	BuildID              uuid.UUID `json:"build_id"`
	PrerenderEmissionKey string    `json:"prerender_emission_key"`
	InputDigest          string    `json:"input_digest"`
}

type Config struct {
	MaxAttempts int
}

type Handler struct {
	config Config
}

func NewHandler(config Config) (*Handler, error) {
	if config.MaxAttempts < 1 || config.MaxAttempts > 100 {
		return nil, ErrInvalidConfiguration
	}
	return &Handler{config: config}, nil
}

type renderPlanPayload struct {
	BuildID               uuid.UUID `json:"build_id"`
	SnapshotID            uuid.UUID `json:"snapshot_id"`
	SnapshotRevision      int64     `json:"snapshot_revision"`
	SnapshotDigest        string    `json:"snapshot_digest"`
	ContentRevision       int64     `json:"content_revision"`
	Locale                string    `json:"locale"`
	LocaleCatalogRevision int64     `json:"locale_catalog_revision"`
}

type renderPlan struct {
	Version     string            `json:"version"`
	InputDigest string            `json:"input_digest"`
	Payload     renderPlanPayload `json:"payload"`
}

type prerenderRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	Plan           []byte `json:"plan"`
	PlanDigest     string `json:"plan_digest"`
	InputDigest    string `json:"input_digest"`
}

// PlanDigest is the exact SHA-256 digest of the serialized render plan.
func PlanDigest(plan []byte) string {
	sum := sha256.Sum256(plan)
	return hex.EncodeToString(sum[:])
}

// WorkerHandler is registered only by static composition. It validates the
// pinned input and emits one child prerender task; it never renders or writes
// files, and it does not call another engine.
func (h *Handler) WorkerHandler(ctx context.Context, task core.Task) (core.TaskOutcome, error) {
	if h == nil || ctx == nil {
		return core.TaskOutcome{}, ErrInvalidTask
	}
	if err := ctx.Err(); err != nil {
		return core.TaskOutcome{}, err
	}
	if task.Kind != core.TaskKindWorker || task.Worker == nil ||
		task.Worker.ComponentID != ComponentID || task.Worker.OperationID != OperationID ||
		task.Worker.ContractVersion != ContractVersion || len(task.Payload) == 0 || len(task.Payload) > maxPayloadBytes {
		return core.TaskOutcome{}, ErrInvalidTask
	}
	request, err := decodeRequest(task.Payload)
	if err != nil {
		return core.TaskOutcome{}, err
	}

	plan, err := json.Marshal(renderPlan{
		Version: RenderPlanFormat, InputDigest: request.InputDigest,
		Payload: renderPlanPayload{
			BuildID: request.BuildID, SnapshotID: request.SnapshotID, SnapshotRevision: request.SnapshotRevision,
			SnapshotDigest: request.SnapshotDigest, ContentRevision: request.ContentRevision,
			Locale: request.Locale, LocaleCatalogRevision: request.LocaleCatalogRevision,
		},
	})
	if err != nil || len(plan) == 0 || len(plan) > maxPayloadBytes {
		return core.TaskOutcome{}, ErrInvalidPayload
	}
	planDigest := PlanDigest(plan)
	emissionKey := "prerender:" + request.BuildID.String()
	idempotencyKey := idempotencyKeyPrefix + request.BuildID.String() + ":prerender"
	if !emissionPattern.MatchString(emissionKey) || !emissionPattern.MatchString(idempotencyKey) {
		return core.TaskOutcome{}, ErrInvalidPayload
	}
	childPayload, err := json.Marshal(prerenderRequest{
		IdempotencyKey: idempotencyKey, Plan: plan, PlanDigest: planDigest, InputDigest: request.InputDigest,
	})
	if err != nil || len(childPayload) == 0 || len(childPayload) > maxPayloadBytes {
		return core.TaskOutcome{}, ErrInvalidPayload
	}

	root, err := json.Marshal(RootResult{
		Format: RootFormat, BuildID: request.BuildID,
		PrerenderEmissionKey: emissionKey, InputDigest: request.InputDigest,
	})
	if err != nil {
		return core.TaskOutcome{}, ErrInvalidPayload
	}
	return core.TaskOutcome{
		Result: root,
		Children: []core.TaskChild{{
			Kind: core.TaskKindWorker,
			Worker: &core.WorkerRef{ComponentID: prerenderComponentID, OperationID: prerenderOperationID,
				ContractVersion: prerenderContractVersion},
			EmissionKey: emissionKey, Payload: childPayload, MaxAttempts: h.config.MaxAttempts,
		}},
	}, nil
}

func decodeRequest(raw json.RawMessage) (Request, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	var request Request
	if err := decoder.Decode(&request); err != nil {
		return Request{}, ErrInvalidPayload
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Request{}, ErrInvalidPayload
	}
	if request.Version != 1 || request.BuildID == uuid.Nil || request.SnapshotID == uuid.Nil ||
		request.SnapshotRevision < 1 || request.ContentRevision < 0 ||
		!digestPattern.MatchString(request.SnapshotDigest) || !digestPattern.MatchString(request.InputDigest) ||
		!localePattern.MatchString(request.Locale) || request.LocaleCatalogRevision < 1 {
		return Request{}, ErrInvalidPayload
	}
	if strings.ContainsAny(request.Locale, "\x00\r\n") || strings.ContainsAny(request.SnapshotDigest+request.InputDigest, "\x00\r\n") {
		return Request{}, ErrInvalidPayload
	}
	return request, nil
}
