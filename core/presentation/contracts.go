package presentation

import (
	"context"
	"time"

	appaccess "github.com/Zubrium/cdn/core/application/access"
	appcoord "github.com/Zubrium/cdn/core/application/coordination"
	"github.com/Zubrium/cdn/core/domain/access"
	corecoord "github.com/Zubrium/cdn/core/domain/coordination"
)

// Access contracts are stable Core API values. Their implementation remains
// private to the Core domain and application layers.
type AccessAction = access.Action
type AccessLevel = access.Level

const (
	AccessActionRead   AccessAction = access.Read
	AccessActionWrite  AccessAction = access.Write
	AccessActionDelete AccessAction = access.Delete
	AccessActionUpload AccessAction = access.Upload
	AccessActionManage AccessAction = access.Manage

	AccessLevelNone  AccessLevel = access.LevelNone
	AccessLevelRead  AccessLevel = access.LevelRead
	AccessLevelWrite AccessLevel = access.LevelWrite
)

func ValidAccessGroupKey(value string) bool      { return appaccess.ValidGroupKey(value) }
func ValidAccessAction(action AccessAction) bool { return appaccess.ValidAction(action) }

type Task = corecoord.Task
type TaskKind = corecoord.TaskKind

const (
	TaskKindOperation = corecoord.TaskKindOperation
	TaskKindJoin      = corecoord.TaskKindJoin
)

type OperationRef = corecoord.OperationRef
type TaskChild = corecoord.TaskChild
type TaskOutcome = corecoord.TaskOutcome
type Workflow = corecoord.Workflow
type WorkflowSummary = corecoord.WorkflowSummary
type WorkflowPage = corecoord.WorkflowPage
type WorkflowDetail = corecoord.WorkflowDetail
type WorkflowCursor = corecoord.WorkflowCursor
type WorkflowListFilter = appcoord.WorkflowListFilter
type WorkflowDetailFilter = appcoord.WorkflowDetailFilter
type TaskCursor = corecoord.TaskCursor
type TaskSummary = corecoord.TaskSummary
type TaskPage = corecoord.TaskPage
type CoordinatorDescriptor = appcoord.Descriptor

// Build and lifecycle contracts support the Core-owned Coordinator instance.
type BuildPlan = appcoord.BuildPlan
type OperationExecutor = appcoord.OperationExecutor
type OperationExecutorFunc = appcoord.OperationExecutorFunc
type TaskFailure = appcoord.TaskFailure
type RuntimeRegistry = appcoord.RuntimeRegistry
type RuntimeComponent = appcoord.RuntimeComponent
type ManagedComponent = appcoord.ManagedComponent
type CurrentReadiness = appcoord.CurrentReadiness
type FailureSource = appcoord.FailureSource
type ActivatableComponent = appcoord.ActivatableComponent
type FinalActivator = appcoord.FinalActivator
type PreStopComponent = appcoord.PreStopComponent
type ComponentFactory = appcoord.ComponentFactory
type ComponentApplication = appcoord.Runtime

var (
	ErrComponentPlanInvalid       = appcoord.ErrInvalid
	ErrComponentAlreadyRegistered = appcoord.ErrDuplicate
	ErrComponentUnavailable       = appcoord.ErrComponentUnavailable
	ErrComponentPanic             = appcoord.ErrComponentPanic
	ErrCoordinatorNotReady        = appcoord.ErrInstanceNotReady
	ErrCoordinatorInputInvalid    = appcoord.ErrInvalid
)

func NewRuntimeRegistry() *RuntimeRegistry { return appcoord.NewRuntimeRegistry() }

func NewTaskFailure(code string, retryable bool) *TaskFailure {
	return appcoord.NewTaskFailure(code, retryable)
}

func Compose(ctx context.Context, plan BuildPlan, registry *RuntimeRegistry, shutdownTimeout time.Duration) (*ComponentApplication, error) {
	return appcoord.Compose(ctx, plan, registry, shutdownTimeout)
}
