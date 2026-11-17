package coordinator

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	coreplatform "github.com/Zubrium/cdn/core/platform"
	"github.com/google/uuid"
)

const maxCoordinatorJobPayloadBytes = 1 << 20

var ErrTaskTypeUnavailable = errors.New("Coordinator task type is not registered")

// JobEnqueueStore is the only Core operation needed by trusted engine adapters
// to submit background work. The Coordinator supplies the active instance and
// epoch so callers cannot choose another system or impersonate a worker.
type JobEnqueueStore interface {
	Enqueue(context.Context, coreplatform.CoordinatorJobRequest) (coreplatform.CoordinatorJob, bool, error)
}

type JobSchedulerConfig struct {
	Store       JobEnqueueStore
	SystemID    string
	Owner       uuid.UUID
	ConfigEpoch int64
	MaxAttempts int
	Catalog     *TaskCatalog
}

// JobScheduler is a per-composition capability. It accepts only task types
// registered by the selected build and always binds submissions to the
// currently leased Coordinator instance.
type JobScheduler struct {
	store       JobEnqueueStore
	systemID    string
	owner       uuid.UUID
	configEpoch int64
	maxAttempts int
	jobTypes    map[string]struct{}
}

type JobReceipt struct {
	ID      uuid.UUID
	State   string
	Created bool
}

func NewJobScheduler(config JobSchedulerConfig) (*JobScheduler, error) {
	if isNilInterface(config.Store) || !coordinatorSystemIDPattern.MatchString(config.SystemID) || config.Owner == uuid.Nil ||
		config.ConfigEpoch <= 0 || config.MaxAttempts < 1 || config.MaxAttempts > 100 || config.Catalog == nil {
		return nil, ErrInvalid
	}
	jobTypes := make(map[string]struct{}, len(config.Catalog.handlers))
	for _, jobType := range config.Catalog.SubmissionTypes() {
		jobTypes[jobType] = struct{}{}
	}
	if len(jobTypes) == 0 {
		return nil, ErrInvalid
	}
	return &JobScheduler{store: config.Store, systemID: config.SystemID, owner: config.Owner,
		configEpoch: config.ConfigEpoch, maxAttempts: config.MaxAttempts, jobTypes: jobTypes}, nil
}

// Schedule creates an idempotent durable task. Payloads are bounded JSON
// objects; credentials and other secret values must remain in their owning
// secret store and be referenced by opaque identifiers instead.
func (s *JobScheduler) Schedule(ctx context.Context, jobType, idempotencyKey string, payload json.RawMessage) (JobReceipt, error) {
	if s == nil || isNilInterface(s.store) || ctx == nil || !coordinatorSystemIDPattern.MatchString(s.systemID) || s.owner == uuid.Nil || s.configEpoch <= 0 ||
		s.maxAttempts < 1 || s.maxAttempts > 100 || len(s.jobTypes) == 0 {
		return JobReceipt{}, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return JobReceipt{}, err
	}
	if _, ok := s.jobTypes[jobType]; !ok {
		return JobReceipt{}, ErrTaskTypeUnavailable
	}
	if !validCoordinatorIdempotencyKey(idempotencyKey) || !validCoordinatorJobPayload(payload) {
		return JobReceipt{}, ErrInvalid
	}
	jobID, err := uuid.NewRandom()
	if err != nil {
		return JobReceipt{}, err
	}
	job, created, err := s.store.Enqueue(ctx, coreplatform.CoordinatorJobRequest{
		ID: jobID, SystemID: s.systemID, Owner: s.owner, ConfigEpoch: s.configEpoch,
		Type: jobType, IdempotencyKey: idempotencyKey, Payload: append(json.RawMessage(nil), payload...), MaxAttempts: s.maxAttempts,
	})
	if err != nil {
		return JobReceipt{}, err
	}
	if job.ID == uuid.Nil || job.SystemID != s.systemID || job.Type != jobType || !validCoordinatorJobState(job.State) {
		return JobReceipt{}, ErrInvalid
	}
	return JobReceipt{ID: job.ID, State: job.State, Created: created}, nil
}

func validCoordinatorIdempotencyKey(value string) bool {
	if value == "" || len(value) > 256 || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return false
	}
	for _, char := range value {
		if unicode.IsControl(char) {
			return false
		}
	}
	return true
}

func validCoordinatorJobPayload(payload json.RawMessage) bool {
	if len(payload) == 0 || len(payload) > maxCoordinatorJobPayloadBytes || !utf8.Valid(payload) || !json.Valid(payload) {
		return false
	}
	var object map[string]json.RawMessage
	return json.Unmarshal(payload, &object) == nil && object != nil
}
