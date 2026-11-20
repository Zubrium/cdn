package coordinator

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"regexp"
	"time"

	corecoord "github.com/Zubrium/cdn/core/domain/coordination"
	"github.com/google/uuid"
)

var componentIDPattern = regexp.MustCompile(`^[a-z][a-z0-9.-]{0,63}$`)
var operationIDPattern = regexp.MustCompile(`^[a-z][a-z0-9._/-]{0,127}$`)

var ErrWorkflowOperationUnavailable = errors.New("workflow operation is not registered")

type WorkflowStore = corecoord.WorkflowRepository

type WorkflowStartRequest struct {
	OperationID     string
	ContractVersion int
	IdempotencyKey  string
	Payload         json.RawMessage
}

type WorkflowReceipt struct {
	WorkflowID uuid.UUID
	RootTaskID uuid.UUID
	State      string
	Created    bool
}

type WorkflowClientConfig struct {
	Store         WorkflowStore
	Workers       *WorkerCatalog
	OwnerEngineID string
	InstanceID    uuid.UUID
	ConfigEpoch   int64
	MaxAttempts   int
	Retention     time.Duration
}

// WorkflowClient is scoped at construction to one registered component. It
// cannot submit work under another engine identity or an undeclared operation.
type WorkflowClient struct {
	store         WorkflowStore
	workers       *WorkerCatalog
	ownerEngineID string
	instanceID    uuid.UUID
	configEpoch   int64
	maxAttempts   int
	retention     time.Duration
}

func NewWorkflowClient(config WorkflowClientConfig) (*WorkflowClient, error) {
	if isNilInterface(config.Store) || config.Workers == nil || !componentIDPattern.MatchString(config.OwnerEngineID) ||
		config.InstanceID == uuid.Nil || config.ConfigEpoch <= 0 || config.MaxAttempts < 1 || config.MaxAttempts > 100 ||
		config.Retention <= 0 || config.Retention > 365*24*time.Hour {
		return nil, ErrInvalid
	}
	if !config.Workers.hasComponent(config.OwnerEngineID) {
		return nil, ErrComponentUnavailable
	}
	return &WorkflowClient{store: config.Store, workers: config.Workers, ownerEngineID: config.OwnerEngineID,
		instanceID: config.InstanceID, configEpoch: config.ConfigEpoch, maxAttempts: config.MaxAttempts,
		retention: config.Retention}, nil
}

func (c *WorkflowClient) Start(ctx context.Context, request WorkflowStartRequest) (WorkflowReceipt, error) {
	if c == nil || isNilInterface(c.store) || ctx == nil || request.ContractVersion < 1 || request.ContractVersion > 1<<16 ||
		!operationIDPattern.MatchString(request.OperationID) || !validCoordinatorIdempotencyKey(request.IdempotencyKey) ||
		!validWorkflowPayload(request.Payload) {
		return WorkflowReceipt{}, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return WorkflowReceipt{}, err
	}
	if _, exists := c.workers.definition(c.ownerEngineID, request.OperationID, request.ContractVersion); !exists {
		return WorkflowReceipt{}, ErrWorkflowOperationUnavailable
	}
	workflowID, err := uuid.NewRandom()
	if err != nil {
		return WorkflowReceipt{}, err
	}
	rootID, err := uuid.NewRandom()
	if err != nil {
		return WorkflowReceipt{}, err
	}
	input := make([]byte, 0, len(request.Payload)+len(request.OperationID)+16)
	input = append(input, request.OperationID...)
	input = append(input, 0)
	input = append(input, itoa(request.ContractVersion)...)
	input = append(input, 0)
	input = append(input, request.Payload...)
	inputHash := sha256.Sum256(input)
	workflow, created, err := c.store.StartWorkflow(ctx, corecoord.WorkflowRequest{
		ID: workflowID, Owner: c.instanceID, OwnerEngineID: c.ownerEngineID, IdempotencyKey: request.IdempotencyKey,
		InputHash: inputHash[:], ConfigEpoch: c.configEpoch, Retention: c.retention,
		Root: corecoord.TaskRequest{ID: rootID, WorkflowID: workflowID, Kind: corecoord.TaskKindWorker,
			Worker: &corecoord.WorkerRef{ComponentID: c.ownerEngineID, OperationID: request.OperationID,
				ContractVersion: request.ContractVersion}, Payload: append(json.RawMessage(nil), request.Payload...),
			MaxAttempts: c.maxAttempts},
	})
	if err != nil {
		return WorkflowReceipt{}, err
	}
	if workflow.ID == uuid.Nil || workflow.RootTaskID == uuid.Nil || workflow.OwnerEngineID != c.ownerEngineID {
		return WorkflowReceipt{}, ErrInvalid
	}
	return WorkflowReceipt{WorkflowID: workflow.ID, RootTaskID: workflow.RootTaskID, State: workflow.State, Created: created}, nil
}

func (c *WorkflowClient) Get(ctx context.Context, id uuid.UUID) (corecoord.Workflow, error) {
	if c == nil || isNilInterface(c.store) || ctx == nil || id == uuid.Nil {
		return corecoord.Workflow{}, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return corecoord.Workflow{}, err
	}
	return c.store.GetWorkflow(ctx, c.ownerEngineID, c.instanceID, c.configEpoch, id)
}

func (c *WorkflowClient) Cancel(ctx context.Context, id uuid.UUID) error {
	if c == nil || isNilInterface(c.store) || ctx == nil || id == uuid.Nil {
		return ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.store.CancelWorkflow(ctx, c.ownerEngineID, c.instanceID, c.configEpoch, id, c.retention)
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	var buffer [20]byte
	index := len(buffer)
	for value > 0 {
		index--
		buffer[index] = byte('0' + value%10)
		value /= 10
	}
	return string(buffer[index:])
}
