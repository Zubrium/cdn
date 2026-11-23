// Package platform defines the neutral ports used by Website application.
// Main supplies implementations backed by module-scoped DataBus clients.
package platform

import (
	"context"

	"github.com/Zubrium/cdn/pkg/contracts"
	"github.com/google/uuid"
)

type Session = contracts.AuthorizationSubject
type Principal = contracts.Principal
type File = contracts.File
type FileSet = contracts.FileSet
type AccessLevel = contracts.AccessLevel
type WorkflowStartRequest = contracts.WorkflowStartInput
type WorkflowReceipt = contracts.WorkflowStartReceipt
type Workflow = contracts.WorkflowState
type TaskResult = contracts.WorkflowTaskResult
type OperationRef = contracts.WorkflowOperationRef

const (
	AccessLevelDenied = contracts.AccessLevelDenied
	AccessLevelRead   = contracts.AccessLevelRead
	AccessLevelWrite  = contracts.AccessLevelWrite
)

var (
	ErrUnauthorized     = contracts.ErrAccessUnauthorized
	ErrForbidden        = contracts.ErrAccessForbidden
	ErrFileNotFound     = contracts.ErrFileNotFound
	ErrRevisionConflict = contracts.ErrFileRevisionConflict
	ErrFileSetConflict  = contracts.ErrFileSetConflict
	ErrTaskMissing      = contracts.ErrWorkflowTaskMissing
	ErrTaskNotOwned     = contracts.ErrWorkflowTaskNotOwned
	ErrUnavailable      = contracts.ErrWorkflowUnavailable
)

func ValidAccessGroupKey(value string) bool { return contracts.ValidAccessGroupKey(value) }

type GroupAuthorizationCapability interface {
	Authenticate(context.Context, string) (Session, Principal, error)
	RequireGroupLevel(context.Context, Session, []string, AccessLevel) error
}

type SessionCapability interface {
	AuthenticateSession(context.Context, string) (Session, Principal, error)
}

type BuildWorkflowClient interface {
	Start(context.Context, WorkflowStartRequest) (WorkflowReceipt, error)
	Get(context.Context, uuid.UUID) (Workflow, error)
	GetTaskResult(context.Context, uuid.UUID, uuid.UUID) (TaskResult, error)
	ResolveTaskID(context.Context, uuid.UUID, string) (uuid.UUID, error)
}
