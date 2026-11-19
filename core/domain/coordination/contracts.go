// Package coordination defines the storage-neutral coordinator vocabulary.
package coordination

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Zubrium/cdn/core/domain/access"
	"github.com/google/uuid"
)

var (
	ErrConflict     = errors.New("coordinator state conflict")
	ErrLeaseLost    = errors.New("coordinator lease lost")
	ErrConfigDiff   = errors.New("coordinator configuration differs from active instances")
	ErrTaskMissing  = errors.New("coordinator task not found")
	ErrTaskNotOwned = errors.New("coordinator task lease is not owned")
	ErrStale        = errors.New("coordinator fencing token is stale")
	ErrInvalid      = errors.New("invalid coordinator operation")
	ErrLimit        = errors.New("coordinator workflow limit exceeded")
)

// Compatibility names retained for the stable Core task-control API while
// its persistence contract is represented by workflows and tasks.
var (
	ErrCoordinatorConflict    = ErrConflict
	ErrCoordinatorLeaseLost   = ErrLeaseLost
	ErrCoordinatorConfigDiff  = ErrConfigDiff
	ErrCoordinatorJobMissing  = ErrTaskMissing
	ErrCoordinatorJobNotOwned = ErrTaskNotOwned
	ErrCoordinatorStale       = ErrStale
	ErrCoordinatorInvalid     = ErrInvalid
)

// ComponentOperation is a public, engine-neutral operation registration.
type ComponentOperation struct {
	ID              string
	ContractVersion int
}

// ComponentDescriptor contains only generic registration metadata. Engine
// implementations keep their concrete contracts outside Core.
type ComponentDescriptor struct {
	ID           string
	Kind         string
	Version      int
	Dependencies []string
	Operations   []ComponentOperation
	AccessGroups []string
}

// ComponentDescriptorProvider lets an outer module describe itself to the
// generic Core workflow registry without importing another component's model.
type ComponentDescriptorProvider interface {
	ComponentDescriptor() ComponentDescriptor
}

// Operation is used by the Core registry for capability metadata.
type Operation struct {
	Name         string
	Action       access.Action
	ResourceType string
}

type Module interface{ Operations() []Operation }

// Repository ports are part of the coordination domain. Application services
// compose these narrow interfaces; infrastructure implements them without
// becoming visible to engines or other Core packages.
type InstanceRepository interface {
	RegisterInstance(context.Context, uuid.UUID, string, time.Duration, time.Duration) (int64, error)
	RenewInstance(context.Context, uuid.UUID, int64, time.Duration) error
	ReleaseInstance(context.Context, uuid.UUID, int64) error
}

type TaskRepository interface {
	Claim(context.Context, uuid.UUID, int64, int, time.Duration, time.Duration) ([]CoordinatorJob, error)
	Extend(context.Context, uuid.UUID, uuid.UUID, int64, int64, time.Duration) error
	Complete(context.Context, uuid.UUID, uuid.UUID, int64, int64, int, int, time.Duration, CoordinatorTaskOutcome) error
	Fail(context.Context, uuid.UUID, uuid.UUID, int64, int64, string, bool, time.Duration, time.Duration) error
}

type JobControlRepository interface {
	ListJobs(context.Context, CoordinatorJobListRequest) (CoordinatorJobPage, error)
	Retry(context.Context, uuid.UUID, uuid.UUID, int64) error
}

type WorkflowRepository interface {
	StartWorkflow(context.Context, WorkflowRequest) (Workflow, bool, error)
	GetWorkflow(context.Context, string, uuid.UUID, int64, uuid.UUID) (Workflow, error)
	CancelWorkflow(context.Context, string, uuid.UUID, int64, uuid.UUID, time.Duration) error
}

// Task is the durable work unit observed by a registered operation handler.
type Task struct {
	ID              uuid.UUID
	WorkflowID      uuid.UUID
	ComponentID     string
	OperationID     string
	ContractVersion int
	ParentTaskID    uuid.UUID
	EmissionKey     string
	Payload         json.RawMessage
	State           string
	Attempts        int
	MaxAttempts     int
	AvailableAt     time.Time
	LeaseOwner      uuid.UUID
	LeaseUntil      *time.Time
	FencingToken    int64
	ConfigEpoch     int64
	Result          json.RawMessage
	ErrorCode       string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// TaskRequest is either a workflow root or a child emitted by a successful
// task. Dependencies are references within the same workflow.
type TaskRequest struct {
	ID              uuid.UUID
	WorkflowID      uuid.UUID
	ComponentID     string
	OperationID     string
	ContractVersion int
	ParentTaskID    uuid.UUID
	EmissionKey     string
	Payload         json.RawMessage
	MaxAttempts     int
	AvailableAt     time.Time
	DependsOn       []uuid.UUID
}

type TaskLease struct {
	InstanceID   uuid.UUID
	ConfigEpoch  int64
	TaskID       uuid.UUID
	FencingToken int64
}

type WorkflowRequest struct {
	ID             uuid.UUID
	Owner          uuid.UUID
	OwnerEngineID  string
	IdempotencyKey string
	InputHash      []byte
	ConfigEpoch    int64
	Retention      time.Duration
	Root           TaskRequest
}

type WorkflowPageRequest struct {
	OwnerEngineID string
	Limit         int
	Before        *WorkflowCursor
}

type WorkflowCursor struct {
	CreatedAt time.Time
	ID        uuid.UUID
}

type WorkflowPage struct {
	Items []Workflow
	Next  *WorkflowCursor
}

type Workflow struct {
	ID            uuid.UUID
	OwnerEngineID string
	State         string
	RootTaskID    uuid.UUID
	Result        json.RawMessage
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type TaskCursor struct {
	CreatedAt time.Time
	ID        uuid.UUID
}

type TaskSummary struct {
	ID          uuid.UUID
	WorkflowID  uuid.UUID
	ComponentID string
	OperationID string
	State       string
	Attempts    int
	MaxAttempts int
	AvailableAt time.Time
	LeaseUntil  *time.Time
	ErrorCode   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type TaskListRequest struct {
	Owner       uuid.UUID
	ConfigEpoch int64
	ComponentID string
	OperationID string
	State       string
	Limit       int
	Before      *TaskCursor
}

type TaskPage struct {
	Items []TaskSummary
	Next  *TaskCursor
}

type CoordinatorJob struct {
	ID              uuid.UUID
	WorkflowID      uuid.UUID
	ComponentID     string
	OperationID     string
	ContractVersion int
	Type            string
	EmissionKey     string
	IdempotencyKey  string
	Payload         json.RawMessage
	State           string
	Attempts        int
	MaxAttempts     int
	AvailableAt     time.Time
	LeaseOwner      uuid.UUID
	LeaseUntil      *time.Time
	FencingToken    int64
	ConfigEpoch     int64
	Result          json.RawMessage
	ErrorCode       string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type CoordinatorTaskChild struct {
	ComponentID     string
	OperationID     string
	ContractVersion int
	EmissionKey     string
	Payload         json.RawMessage
	MaxAttempts     int
	AvailableAt     time.Time
	DependsOn       []uuid.UUID
}

type CoordinatorTaskOutcome struct {
	Result   json.RawMessage
	Children []CoordinatorTaskChild
}

type CoordinatorJobCursor struct {
	CreatedAt time.Time
	ID        uuid.UUID
}

type CoordinatorJobSummary struct {
	ID              uuid.UUID
	ComponentID     string
	OperationID     string
	ContractVersion int
	Type            string
	State           string
	Attempts        int
	MaxAttempts     int
	AvailableAt     time.Time
	LeaseUntil      *time.Time
	ErrorCode       string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type CoordinatorJobListRequest struct {
	Owner       uuid.UUID
	ConfigEpoch int64
	Type        string
	State       string
	Limit       int
	Before      *CoordinatorJobCursor
}

type CoordinatorJobPage struct {
	Items []CoordinatorJobSummary
	Next  *CoordinatorJobCursor
}
