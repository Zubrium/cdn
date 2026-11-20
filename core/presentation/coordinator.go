package presentation

import (
	"context"
	"errors"
	"time"

	appcoord "github.com/Zubrium/cdn/core/application/coordination"
	corecoord "github.com/Zubrium/cdn/core/domain/coordination"
	"github.com/Zubrium/cdn/core/infrastructure/postgres"
)

var (
	ErrCoordinatorConflict   = corecoord.ErrConflict
	ErrCoordinatorLeaseLost  = corecoord.ErrLeaseLost
	ErrCoordinatorConfigDiff = corecoord.ErrConfigDiff
	ErrTaskMissing           = corecoord.ErrTaskMissing
	ErrTaskNotOwned          = corecoord.ErrTaskNotOwned
	ErrCoordinatorStale      = corecoord.ErrStale
	ErrCoordinatorInvalid    = corecoord.ErrInvalid
	ErrCoordinatorLimit      = corecoord.ErrLimit
)

type WorkflowRequest = corecoord.WorkflowRequest
type WorkflowStartRequest = appcoord.WorkflowStartRequest
type WorkflowReceipt = appcoord.WorkflowReceipt
type WorkflowClient = appcoord.WorkflowClient
type WorkflowControl = appcoord.WorkflowControl

// CoordinatorRuntimeConfig contains explicit operational limits. Component
// contracts and task handlers are collected from the selected composition;
// Core passes only their neutral descriptors into the workflow service.
type CoordinatorRuntimeConfig struct {
	Fingerprint         string
	InstanceLease       time.Duration
	HeartbeatInterval   time.Duration
	ExecutorConcurrency int
	TaskLease           time.Duration
	TaskTimeout         time.Duration
	PollInterval        time.Duration
	RetryBase           time.Duration
	RetryMaximum        time.Duration
	MaxAttempts         int
	MaxWorkflowTasks    int
	MaxChildrenPerTask  int
	WorkflowRetention   time.Duration
	ShutdownTimeout     time.Duration
	WorkerCatalog       *appcoord.WorkerCatalog
}

// StartCoordinator is called once during Core startup after component
// descriptors are registered. The returned repository remains private to
// Core; callers receive only scoped workflow or administrative capabilities.
func (r *Runtime) StartCoordinator(ctx context.Context, cfg CoordinatorRuntimeConfig) error {
	if err := r.valid(); err != nil {
		return err
	}
	if ctx == nil || cfg.ShutdownTimeout <= 0 || r.Registry == nil {
		return ErrCoordinatorInvalid
	}
	if _, err := r.Registry.Lookup("coordinator"); err != nil {
		return ErrCoordinatorInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	r.coordinatorMu.Lock()
	if r.coordinator != nil || r.coordinatorStarting {
		r.coordinatorMu.Unlock()
		return ErrCoordinatorInvalid
	}
	r.coordinatorStarting = true
	r.coordinatorMu.Unlock()

	store := postgres.CoordinatorStore{Pool: r.database}
	service, err := appcoord.NewService(appcoord.ServiceConfig{
		Store: store, Fingerprint: cfg.Fingerprint,
		InstanceLease: cfg.InstanceLease, HeartbeatInterval: cfg.HeartbeatInterval,
		ExecutorConcurrency: cfg.ExecutorConcurrency, TaskLease: cfg.TaskLease,
		TaskTimeout: cfg.TaskTimeout, PollInterval: cfg.PollInterval,
		RetryBase: cfg.RetryBase, RetryMaximum: cfg.RetryMaximum,
		MaxAttempts: cfg.MaxAttempts, MaxWorkflowTasks: cfg.MaxWorkflowTasks,
		MaxChildrenPerTask: cfg.MaxChildrenPerTask, WorkflowRetention: cfg.WorkflowRetention,
		Workers: cfg.WorkerCatalog,
	})
	if err != nil {
		r.coordinatorMu.Lock()
		r.coordinatorStarting = false
		r.coordinatorMu.Unlock()
		return err
	}
	if err := service.Start(ctx); err != nil {
		closeErr := service.Close(context.Background())
		r.coordinatorMu.Lock()
		r.coordinatorStarting = false
		r.coordinatorMu.Unlock()
		return errors.Join(err, closeErr)
	}

	r.coordinatorMu.Lock()
	r.coordinator = service
	r.coordinatorShutdownTimeout = cfg.ShutdownTimeout
	r.coordinatorStarting = false
	r.coordinatorMu.Unlock()
	return nil
}

// ActivateCoordinator starts durable workers after the application composition
// has initialized every selected component.
func (r *Runtime) ActivateCoordinator(ctx context.Context) error {
	service := r.coordinatorService()
	if service == nil {
		return appcoord.ErrInstanceNotReady
	}
	return service.Activate(ctx)
}

func (r *Runtime) CoordinatorReady() bool {
	service := r.coordinatorService()
	return service != nil && service.Ready()
}

// CoordinatorStartupReady checks instance/lease prerequisites before the
// composition's final worker-activation phase.
func (r *Runtime) CoordinatorStartupReady(ctx context.Context) error {
	if ctx == nil {
		return ErrCoordinatorInvalid
	}
	service := r.coordinatorService()
	if service == nil {
		return appcoord.ErrInstanceNotReady
	}
	return service.CheckStartup(ctx)
}

// StopCoordinator stops accepting and executing workflow tasks while leaving
// Core storage available for other component shutdown and handler cleanup.
func (r *Runtime) StopCoordinator(ctx context.Context) error {
	if ctx == nil {
		return ErrCoordinatorInvalid
	}
	return r.closeCoordinator(ctx)
}

// CoordinatorErrors exposes only fatal Coordinator failures to the outer
// lifecycle. Storage maintenance and cache-listener warnings use Runtime.Errors.
func (r *Runtime) CoordinatorErrors() <-chan error {
	service := r.coordinatorService()
	if service != nil {
		return service.Errors()
	}
	closed := make(chan error)
	close(closed)
	return closed
}

func (r *Runtime) NewWorkflowClient(componentID string) (*WorkflowClient, error) {
	service := r.coordinatorService()
	if service == nil {
		return nil, appcoord.ErrInstanceNotReady
	}
	return service.NewWorkflowClient(componentID)
}

// NewWorkflowControl exposes workflow summaries, task graphs, and task retry.
// HTTP adapters must authenticate and authorize each request before using it.
func (r *Runtime) NewWorkflowControl() (*WorkflowControl, error) {
	service := r.coordinatorService()
	if service == nil {
		return nil, appcoord.ErrInstanceNotReady
	}
	return service.NewWorkflowControl()
}

func (r *Runtime) coordinatorService() *appcoord.Service {
	if r == nil {
		return nil
	}
	r.coordinatorMu.RLock()
	defer r.coordinatorMu.RUnlock()
	return r.coordinator
}

func (r *Runtime) closeCoordinator(ctx context.Context) error {
	if r == nil {
		return nil
	}
	r.coordinatorMu.RLock()
	service := r.coordinator
	r.coordinatorMu.RUnlock()
	if service == nil {
		return nil
	}
	if err := service.Close(ctx); err != nil {
		return err
	}
	r.coordinatorMu.Lock()
	if r.coordinator == service {
		r.coordinator = nil
	}
	r.coordinatorMu.Unlock()
	return nil
}

func IsCoordinatorConflict(err error) bool {
	return errors.Is(err, ErrCoordinatorConflict) || errors.Is(err, ErrCoordinatorStale)
}
