// Package coordination defines the storage-neutral coordinator vocabulary.
package coordination

import (
	"context"
	"encoding/json"
	"errors"
	"time"

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
	ErrCoordinatorConflict   = ErrConflict
	ErrCoordinatorLeaseLost  = ErrLeaseLost
	ErrCoordinatorConfigDiff = ErrConfigDiff
	ErrCoordinatorStale      = ErrStale
	ErrCoordinatorInvalid    = ErrInvalid
)

// Repository ports are part of the coordination domain. Application services
// compose these narrow interfaces; infrastructure implements them without
// becoming visible to engines or other Core packages.
type InstanceRepository interface {
	RegisterInstance(context.Context, uuid.UUID, string, time.Duration, time.Duration) (int64, error)
	RenewInstance(context.Context, uuid.UUID, int64, time.Duration) error
	ReleaseInstance(context.Context, uuid.UUID, int64) error
}

type TaskRepository interface {
	Claim(context.Context, uuid.UUID, int64, []string, int, time.Duration, time.Duration) ([]Task, error)
	Extend(context.Context, uuid.UUID, uuid.UUID, int64, int64, time.Duration) error
	Complete(context.Context, uuid.UUID, uuid.UUID, int64, int64, int, int, time.Duration, TaskOutcome) error
	Fail(context.Context, uuid.UUID, uuid.UUID, int64, int64, string, bool, time.Duration, time.Duration) error
}

type WorkflowControlRepository interface {
	ListWorkflows(context.Context, WorkflowListRequest) (WorkflowPage, error)
	GetWorkflowDetail(context.Context, uuid.UUID, int64, uuid.UUID, int, *TaskCursor) (WorkflowDetail, error)
	RetryTask(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, int64) error
}

type WorkflowRepository interface {
	StartWorkflow(context.Context, WorkflowRequest) (Workflow, bool, error)
	GetWorkflow(context.Context, string, uuid.UUID, int64, uuid.UUID) (Workflow, error)
	CancelWorkflow(context.Context, string, uuid.UUID, int64, uuid.UUID, time.Duration) error
}

// TaskResult is a bounded, engine-neutral view of one durable task result.
// It carries no input payload, scheduling metadata or administrative fields.
type TaskResult struct {
	TaskID     uuid.UUID
	WorkflowID uuid.UUID
	Operation  *OperationRef
	State      string
	Result     json.RawMessage
	ErrorCode  string
}

// TaskResultRepository is a narrow read port for the owner of a workflow. It is
// deliberately separate from WorkflowRepository and WorkflowControlRepository:
// task results are not part of the administrative task graph.
type TaskResultRepository interface {
	GetTaskResult(context.Context, string, uuid.UUID, int64, uuid.UUID, uuid.UUID) (TaskResult, error)
}

type TaskKind string

const (
	TaskKindOperation TaskKind = "operation"
	TaskKindJoin      TaskKind = "join"
)

// OperationRef is a generic, profile-bound operation identity. ClassID is an
// opaque scheduler scope and is never interpreted as a product module kind.
type OperationRef struct {
	ClassID string
	ID      string
	Version int
}

// Task is the durable unit in a workflow. An operation task is dispatched by
// the outer composition; a join task is completed by Core after dependencies succeed.
type Task struct {
	ID             uuid.UUID
	WorkflowID     uuid.UUID
	Kind           TaskKind
	Operation      *OperationRef
	ParentTaskID   uuid.UUID
	EmissionKey    string
	IdempotencyKey string
	Payload        json.RawMessage
	State          string
	Attempts       int
	MaxAttempts    int
	AvailableAt    time.Time
	LeaseOwner     uuid.UUID
	LeaseUntil     *time.Time
	FencingToken   int64
	ConfigEpoch    int64
	Result         json.RawMessage
	ErrorCode      string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// TaskRequest is either a workflow root or a child emitted by a successful
// task. Dependencies are references within the same workflow.
type TaskRequest struct {
	ID                 uuid.UUID
	WorkflowID         uuid.UUID
	Kind               TaskKind
	Operation          *OperationRef
	ParentTaskID       uuid.UUID
	EmissionKey        string
	Payload            json.RawMessage
	MaxAttempts        int
	AvailableAt        time.Time
	DependsOn          []uuid.UUID
	DependsOnEmissions []string
}

type WorkflowRequest struct {
	ID             uuid.UUID
	Owner          uuid.UUID
	OwnerClassID   string
	IdempotencyKey string
	InputHash      []byte
	ConfigEpoch    int64
	Retention      time.Duration
	Root           TaskRequest
}

type WorkflowCursor struct {
	CreatedAt time.Time
	ID        uuid.UUID
}

type WorkflowListRequest struct {
	Owner       uuid.UUID
	ConfigEpoch int64
	State       string
	Limit       int
	Before      *WorkflowCursor
}

type WorkflowSummary struct {
	ID           uuid.UUID
	OwnerClassID string
	State        string
	TaskCount    int
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type WorkflowPage struct {
	Items []WorkflowSummary
	Next  *WorkflowCursor
}

type WorkflowDetail struct {
	Workflow Workflow
	TaskPage TaskPage
}

type Workflow struct {
	ID           uuid.UUID
	OwnerClassID string
	State        string
	RootTaskID   uuid.UUID
	Result       json.RawMessage
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type TaskCursor struct {
	CreatedAt time.Time
	ID        uuid.UUID
}

type TaskSummary struct {
	ID           uuid.UUID
	WorkflowID   uuid.UUID
	Kind         TaskKind
	Operation    *OperationRef
	ParentTaskID uuid.UUID
	DependsOn    []uuid.UUID
	EmissionKey  string
	State        string
	Attempts     int
	MaxAttempts  int
	AvailableAt  time.Time
	LeaseUntil   *time.Time
	ErrorCode    string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type TaskPage struct {
	Items []TaskSummary
	Next  *TaskCursor
}

type TaskChild struct {
	Kind               TaskKind
	Operation          *OperationRef
	EmissionKey        string
	Payload            json.RawMessage
	MaxAttempts        int
	AvailableAt        time.Time
	DependsOn          []uuid.UUID
	DependsOnEmissions []string
}

type TaskOutcome struct {
	Result   json.RawMessage
	Children []TaskChild
}
