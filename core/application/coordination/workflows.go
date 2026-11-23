package coordinator

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"regexp"
	"time"

	corecoord "github.com/Zubrium/cdn/core/domain/coordination"
	"github.com/google/uuid"
)

var taskClassPattern = regexp.MustCompile(`^[a-z][a-z0-9.-]{0,63}$`)
var operationIDPattern = regexp.MustCompile(`^[a-z][a-z0-9._/-]{0,127}$`)

type WorkflowStore = corecoord.WorkflowRepository
type TaskResultStore = corecoord.TaskResultRepository

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
	Store       WorkflowStore
	TaskClass   string
	TaskClasses []string
	InstanceID  uuid.UUID
	ConfigEpoch int64
	MaxAttempts int
	Retention   time.Duration
}

// WorkflowClient is scoped at construction to one registered component. It
// cannot submit work under another engine identity or an undeclared operation.
type WorkflowClient struct {
	store       WorkflowStore
	results     TaskResultStore
	taskClass   string
	instanceID  uuid.UUID
	configEpoch int64
	maxAttempts int
	retention   time.Duration
}

func NewWorkflowClient(config WorkflowClientConfig) (*WorkflowClient, error) {
	if isNilInterface(config.Store) || !taskClassPattern.MatchString(config.TaskClass) || !containsTaskClass(config.TaskClasses, config.TaskClass) ||
		config.InstanceID == uuid.Nil || config.ConfigEpoch <= 0 || config.MaxAttempts < 1 || config.MaxAttempts > 100 ||
		config.Retention <= 0 || config.Retention > 365*24*time.Hour {
		return nil, ErrInvalid
	}
	// The result read is a separate port. Deriving it from the same Core store
	// keeps existing WorkflowRepository implementations unchanged.
	results, _ := config.Store.(TaskResultStore)
	return &WorkflowClient{store: config.Store, results: results, taskClass: config.TaskClass,
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
		ID: workflowID, Owner: c.instanceID, OwnerClassID: c.taskClass, IdempotencyKey: request.IdempotencyKey,
		InputHash: inputHash[:], ConfigEpoch: c.configEpoch, Retention: c.retention,
		Root: corecoord.TaskRequest{ID: rootID, WorkflowID: workflowID, Kind: corecoord.TaskKindOperation,
			Operation: &corecoord.OperationRef{ClassID: c.taskClass, ID: request.OperationID,
				Version: request.ContractVersion}, Payload: append(json.RawMessage(nil), request.Payload...),
			MaxAttempts: c.maxAttempts},
	})
	if err != nil {
		return WorkflowReceipt{}, err
	}
	if workflow.ID == uuid.Nil || workflow.RootTaskID == uuid.Nil || workflow.OwnerClassID != c.taskClass {
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
	return c.store.GetWorkflow(ctx, c.taskClass, c.instanceID, c.configEpoch, id)
}

// GetTaskResult reads one durable task result for a workflow owned by this
// client's statically registered component, instance and config epoch. The
// caller supplies only workflow and task IDs; no owner or operation identity
// can be passed in. Administrative WorkflowControl deliberately has no such
// method.
func (c *WorkflowClient) GetTaskResult(ctx context.Context, workflowID, taskID uuid.UUID) (corecoord.TaskResult, error) {
	if c == nil || isNilInterface(c.store) || isNilInterface(c.results) || ctx == nil || workflowID == uuid.Nil || taskID == uuid.Nil {
		return corecoord.TaskResult{}, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return corecoord.TaskResult{}, err
	}
	result, err := c.results.GetTaskResult(ctx, c.taskClass, c.instanceID, c.configEpoch, workflowID, taskID)
	if err != nil {
		return corecoord.TaskResult{}, err
	}
	if len(result.Result) > MaxTaskResultBytes {
		return corecoord.TaskResult{}, ErrLimit
	}
	return result, nil
}

func (c *WorkflowClient) Cancel(ctx context.Context, id uuid.UUID) error {
	if c == nil || isNilInterface(c.store) || ctx == nil || id == uuid.Nil {
		return ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.store.CancelWorkflow(ctx, c.taskClass, c.instanceID, c.configEpoch, id, c.retention)
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
