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

// Generic component contracts let engines and workers register capabilities
// without importing Core domain or application packages.
type ComponentOperation = corecoord.ComponentOperation
type ComponentDescriptor = corecoord.ComponentDescriptor
type ComponentDescriptorProvider = corecoord.ComponentDescriptorProvider
type CoreJob = corecoord.CoordinatorJob
type CoordinatorTask = corecoord.Task
type CoordinatorTaskChild = corecoord.CoordinatorTaskChild
type CoordinatorTaskOutcome = corecoord.CoordinatorTaskOutcome
type CoordinatorDescriptor = appcoord.Descriptor

// Build and lifecycle contracts are the only public surface for the generic
// component composition and durable Coordinator worker catalog.
type BuildPlan = appcoord.BuildPlan
type ComponentTaskSet = appcoord.ComponentTaskSet
type TaskHandler = appcoord.TaskHandler
type TaskCatalog = appcoord.TaskCatalog
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
	ErrComponentPlanInvalid         = appcoord.ErrInvalid
	ErrComponentAlreadyRegistered   = appcoord.ErrDuplicate
	ErrComponentUnavailable         = appcoord.ErrComponentUnavailable
	ErrComponentPanic               = appcoord.ErrComponentPanic
	ErrTaskProviderUnavailable      = appcoord.ErrTaskProviderUnavailable
	ErrTaskHandlerConflict          = appcoord.ErrTaskHandlerConflict
	ErrWorkflowOperationUnavailable = appcoord.ErrWorkflowOperationUnavailable
	ErrCoordinatorNotReady          = appcoord.ErrInstanceNotReady
	ErrCoordinatorInputInvalid      = appcoord.ErrInvalid
)

func NewRuntimeRegistry() *RuntimeRegistry { return appcoord.NewRuntimeRegistry() }

func BuildTaskCatalog(plan BuildPlan, providers []ComponentTaskSet) (*TaskCatalog, error) {
	return appcoord.BuildTaskCatalog(plan, providers)
}

func Compose(ctx context.Context, plan BuildPlan, registry *RuntimeRegistry, shutdownTimeout time.Duration) (*ComponentApplication, error) {
	return appcoord.Compose(ctx, plan, registry, shutdownTimeout)
}
