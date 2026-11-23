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

	"github.com/Zubrium/cdn/pkg/contracts"
	prerendercontract "github.com/Zubrium/cdn/pkg/contracts/prerender"
	"github.com/google/uuid"
)

const (
	ComponentID      = "engine.website"
	OperationID      = "build-page"
	ContractVersion  = 1
	RootFormat       = "website.build-root/v2"
	RenderPlanFormat = prerendercontract.PlanVersion

	prerenderOperationID     = "render-page"
	prerenderContractVersion = 1
	maxPayloadBytes          = 1 << 20
)

var (
	ErrInvalidConfiguration = errors.New("website build handler configuration is invalid")
	ErrInvalidTask          = errors.New("website build task is invalid")
	ErrInvalidPayload       = errors.New("website build payload is invalid")

	digestPattern   = regexp.MustCompile(`^[a-f0-9]{64}$`)
	emissionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,255}$`)
)

// Request is the immutable, typed workflow input created by Website
// application. It deliberately contains no route, URL, branch, record value,
// executable, command or storage path.
type Request struct {
	Version     int                            `json:"version"`
	BuildID     uuid.UUID                      `json:"build_id"`
	Input       prerendercontract.TrustedInput `json:"input"`
	Page        prerendercontract.PagePlan     `json:"page"`
	InputDigest string                         `json:"input_digest"`
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

type renderPlan struct {
	Version     string                         `json:"version"`
	InputDigest string                         `json:"input_digest"`
	Input       prerendercontract.TrustedInput `json:"input"`
	Page        prerendercontract.PagePlan     `json:"page"`
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
func (h *Handler) WorkerHandler(ctx context.Context, task contracts.OperationTask) (contracts.OperationOutcome, error) {
	if h == nil || ctx == nil || task.TaskID == "" {
		return contracts.OperationOutcome{}, ErrInvalidTask
	}
	if err := ctx.Err(); err != nil {
		return contracts.OperationOutcome{}, err
	}
	if len(task.Payload) == 0 || len(task.Payload) > maxPayloadBytes {
		return contracts.OperationOutcome{}, ErrInvalidTask
	}
	request, err := decodeRequest(task.Payload)
	if err != nil {
		return contracts.OperationOutcome{}, err
	}

	plan, err := json.Marshal(renderPlan{
		Version: RenderPlanFormat, InputDigest: request.InputDigest,
		Input: request.Input, Page: request.Page,
	})
	if err != nil || len(plan) == 0 || len(plan) > maxPayloadBytes {
		return contracts.OperationOutcome{}, ErrInvalidPayload
	}
	planDigest := PlanDigest(plan)
	emissionKey := "prerender:" + request.BuildID.String()
	// The scheduler carries the workflow idempotency key on the operation task; the
	// child render identity is the emission key. Keeping both identical lets
	// the worker bind the exact emitted task without accepting task payloads
	// that select a different identity.
	idempotencyKey := emissionKey
	if !emissionPattern.MatchString(emissionKey) || !emissionPattern.MatchString(idempotencyKey) {
		return contracts.OperationOutcome{}, ErrInvalidPayload
	}
	childPayload, err := json.Marshal(prerenderRequest{
		IdempotencyKey: idempotencyKey, Plan: plan, PlanDigest: planDigest, InputDigest: request.InputDigest,
	})
	if err != nil || len(childPayload) == 0 || len(childPayload) > maxPayloadBytes {
		return contracts.OperationOutcome{}, ErrInvalidPayload
	}

	root, err := json.Marshal(RootResult{
		Format: RootFormat, BuildID: request.BuildID,
		PrerenderEmissionKey: emissionKey, InputDigest: request.InputDigest,
	})
	if err != nil {
		return contracts.OperationOutcome{}, ErrInvalidPayload
	}
	return contracts.OperationOutcome{
		Result: root,
		Children: []contracts.OperationTaskChild{{
			OperationID: prerenderOperationID, Version: prerenderContractVersion,
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
	if request.Version != 2 || request.BuildID == uuid.Nil || !digestPattern.MatchString(request.InputDigest) ||
		prerendercontract.ValidateTrustedInput(request.Input) != nil ||
		request.Input.Locale != request.Page.Title || request.Page.BodyHTML == "" || len(request.Page.BodyHTML) > 1<<20 {
		return Request{}, ErrInvalidPayload
	}
	expected, err := prerendercontract.InputDigest(request.Input)
	if err != nil || expected != request.InputDigest {
		return Request{}, ErrInvalidPayload
	}
	if strings.ContainsAny(request.Input.Locale+request.InputDigest, "\x00\r\n") {
		return Request{}, ErrInvalidPayload
	}
	return request, nil
}
