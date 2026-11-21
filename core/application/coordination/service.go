package coordinator

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ServiceStore is the Coordinator's complete typed Core boundary. Core still
// owns persistence; the Coordinator receives no database or object-store
// handles.
type ServiceStore interface {
	InstanceStore
	TaskStore
	WorkflowControlStore
	WorkflowStore
}

type ServiceConfig struct {
	Store               ServiceStore
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
	Workers             *WorkerCatalog
}

// Service owns the live Coordinator instance, scoped workflow clients, safe
// task control and the single system-wide worker pool.
type Service struct {
	mu           sync.RWMutex
	cfg          ServiceConfig
	instance     *Instance
	executor     *TaskExecutor
	ready        bool
	failed       bool
	closing      bool
	started      bool
	activated    bool
	closeDone    chan struct{}
	shutdownOnce sync.Once
	closeErr     error
	monitorCtx   context.Context
	monitorStop  context.CancelFunc
	monitorWG    sync.WaitGroup
	errors       chan error
}

func NewService(config ServiceConfig) (*Service, error) {
	if isNilInterface(config.Store) || !validFingerprint(config.Fingerprint) ||
		config.InstanceLease < 3*time.Second || config.InstanceLease > time.Hour || config.HeartbeatInterval <= 0 || config.HeartbeatInterval >= config.InstanceLease/3 ||
		config.ExecutorConcurrency < 1 || config.ExecutorConcurrency > 256 || config.TaskLease < 3*time.Second || config.TaskLease > time.Hour ||
		config.TaskTimeout <= 0 || config.TaskTimeout > 24*time.Hour || config.PollInterval <= 0 || config.PollInterval >= config.TaskLease ||
		config.RetryBase <= 0 || config.RetryMaximum < config.RetryBase || config.RetryMaximum > 24*time.Hour ||
		config.MaxAttempts < 1 || config.MaxAttempts > 100 || config.MaxWorkflowTasks < 1 || config.MaxWorkflowTasks > 100000 ||
		config.MaxChildrenPerTask < 0 || config.MaxChildrenPerTask > 256 || config.WorkflowRetention <= 0 || config.WorkflowRetention > 365*24*time.Hour {
		return nil, ErrInvalid
	}
	if config.Workers != nil && len(config.Workers.workers) == 0 {
		return nil, ErrInvalid
	}
	return &Service{cfg: config, closeDone: make(chan struct{}), errors: make(chan error, 1)}, nil
}

func (s *Service) Start(ctx context.Context) error {
	if s == nil || ctx == nil {
		return ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	if s.started || s.closing {
		s.mu.Unlock()
		return ErrTaskExecutorAlreadyStarted
	}
	s.started = true
	s.mu.Unlock()

	instance, err := StartInstance(ctx, s.cfg.Store, InstanceConfig{
		Fingerprint:   s.cfg.Fingerprint,
		LeaseDuration: s.cfg.InstanceLease, HeartbeatInterval: s.cfg.HeartbeatInterval, WorkflowRetention: s.cfg.WorkflowRetention,
	})
	if err != nil {
		s.abortStart()
		return err
	}
	var executor *TaskExecutor
	if s.cfg.Workers != nil {
		executor, err = NewTaskExecutor(s.cfg.Store, TaskExecutorConfig{
			Owner: instance.ID(), ConfigEpoch: instance.Epoch(),
			Concurrency: s.cfg.ExecutorConcurrency, LeaseDuration: s.cfg.TaskLease,
			TaskTimeout: s.cfg.TaskTimeout, PollInterval: s.cfg.PollInterval,
			RetryBase: s.cfg.RetryBase, RetryMaximum: s.cfg.RetryMaximum,
			MaxWorkflowTasks: s.cfg.MaxWorkflowTasks, MaxChildrenPerTask: s.cfg.MaxChildrenPerTask,
			WorkflowRetention: s.cfg.WorkflowRetention,
		}, s.cfg.Workers.handlers())
		if err != nil {
			s.abortStart()
			return errors.Join(err, closeInstanceAfterStartFailure(instance, s.cfg.InstanceLease))
		}
	}
	monitorCtx, monitorStop := context.WithCancel(context.Background())
	s.mu.Lock()
	s.instance, s.executor = instance, executor
	s.monitorCtx, s.monitorStop = monitorCtx, monitorStop
	s.ready = true
	s.mu.Unlock()
	s.monitor("instance", instance.Errors())
	return nil
}

// Ready checks the Coordinator's startup prerequisites. Worker execution is
// intentionally activated after all selected engines have passed readiness.
func (s *Service) CheckStartup(ctx context.Context) error {
	if s == nil || ctx == nil {
		return ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.RLock()
	instance, closing, failed := s.instance, s.closing, s.failed
	s.mu.RUnlock()
	if instance == nil || closing || failed || !instance.Ready() {
		return ErrInstanceNotReady
	}
	return nil
}

// Activate starts the single system-wide task pool only after engines and
// presentation components are initialized and ready.
func (s *Service) Activate(ctx context.Context) error {
	if s == nil || ctx == nil {
		return ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	if !s.started || s.closing || s.failed {
		s.mu.Unlock()
		return ErrInstanceNotReady
	}
	if s.activated {
		s.mu.Unlock()
		return ErrTaskExecutorAlreadyStarted
	}
	executor := s.executor
	s.mu.Unlock()
	if executor != nil {
		if err := executor.Start(ctx); err != nil {
			return err
		}
		if !executor.Healthy() {
			return ErrTaskExecutorNotReady
		}
	}
	s.mu.Lock()
	if s.closing || s.failed || s.instance == nil || !s.instance.Ready() {
		s.mu.Unlock()
		return ErrInstanceNotReady
	}
	s.activated = true
	s.mu.Unlock()
	if executor != nil {
		s.monitor("task executor", executor.Errors())
	}
	return nil
}

func (s *Service) Stop(ctx context.Context) error { return s.Close(ctx) }

// abortStart releases the one-shot startup guard so a failed registration or
// executor initialization can be retried without restarting the process.
func (s *Service) abortStart() {
	s.mu.Lock()
	s.started = false
	s.mu.Unlock()
}

func closeInstanceAfterStartFailure(instance *Instance, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return instance.Close(ctx)
}

func (s *Service) monitor(component string, source <-chan error) {
	s.mu.Lock()
	if s.closing || s.monitorCtx == nil {
		s.mu.Unlock()
		return
	}
	monitorCtx := s.monitorCtx
	s.monitorWG.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.monitorWG.Done()
		for {
			select {
			case <-monitorCtx.Done():
				return
			case err, ok := <-source:
				if !ok {
					s.fail(component)
					return
				}
				if err != nil {
					s.fail(component)
					return
				}
			}
		}
	}()
}

func (s *Service) fail(component string) {
	s.mu.Lock()
	if s.closing || s.failed {
		s.mu.Unlock()
		return
	}
	s.failed, s.ready = true, false
	s.mu.Unlock()
	select {
	case s.errors <- fmt.Errorf("Coordinator %s component reported a fatal failure", component):
	default:
	}
}

func (s *Service) Ready() bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	ready, activated, closing, failed := s.ready, s.activated, s.closing, s.failed
	instance, executor := s.instance, s.executor
	s.mu.RUnlock()
	return ready && activated && !closing && !failed && instance != nil && instance.Ready() && (executor == nil || executor.Healthy())
}

func (s *Service) IsReady() bool { return s.Ready() }

func (s *Service) Errors() <-chan error {
	if s == nil {
		ch := make(chan error)
		close(ch)
		return ch
	}
	return s.errors
}

func (s *Service) NewWorkflowClient(componentID string) (*WorkflowClient, error) {
	if s == nil || !s.Ready() || s.cfg.Workers == nil {
		return nil, ErrInstanceNotReady
	}
	s.mu.RLock()
	instance := s.instance
	s.mu.RUnlock()
	return NewWorkflowClient(WorkflowClientConfig{Store: s.cfg.Store, Workers: s.cfg.Workers,
		OwnerEngineID: componentID, InstanceID: instance.ID(), ConfigEpoch: instance.Epoch(), MaxAttempts: s.cfg.MaxAttempts,
		Retention: s.cfg.WorkflowRetention})
}

// NewWorkflowControl is safe only behind an authenticated, separately
// authorized presentation handler. It exposes summaries, task graphs, and
// task retry without exposing payloads or results.
func (s *Service) NewWorkflowControl() (*WorkflowControl, error) {
	if s == nil || !s.Ready() {
		return nil, ErrInstanceNotReady
	}
	s.mu.RLock()
	instance := s.instance
	s.mu.RUnlock()
	return NewWorkflowControl(WorkflowControlConfig{Store: s.cfg.Store,
		Owner: instance.ID(), ConfigEpoch: instance.Epoch()})
}

func (s *Service) Close(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if ctx == nil {
		return ErrInvalid
	}
	s.mu.Lock()
	if s.closing {
		done := s.closeDone
		s.mu.Unlock()
		select {
		case <-done:
			s.mu.RLock()
			defer s.mu.RUnlock()
			return s.closeErr
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if !s.started {
		s.closing = true
		s.closeErr = nil
		close(s.errors)
		close(s.closeDone)
		s.mu.Unlock()
		return nil
	}
	s.closing, s.ready = true, false
	executor, instance, monitorStop := s.executor, s.instance, s.monitorStop
	s.mu.Unlock()
	s.shutdownOnce.Do(func() {
		go s.shutdown(executor, instance, monitorStop)
	})
	select {
	case <-s.closeDone:
		s.mu.RLock()
		defer s.mu.RUnlock()
		return s.closeErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

// shutdown continues after the caller's deadline. Core must keep its database
// open until cooperative handlers have stopped and the instance lease is
// released; later Close calls can wait for this same shutdown.
func (s *Service) shutdown(executor *TaskExecutor, instance *Instance, monitorStop context.CancelFunc) {
	if monitorStop != nil {
		monitorStop()
	}
	var closeErr error
	if executor != nil {
		closeErr = errors.Join(closeErr, executor.Close(context.Background()))
	}
	if instance != nil {
		closeErr = errors.Join(closeErr, instance.Close(context.Background()))
	}
	s.monitorWG.Wait()
	s.mu.Lock()
	s.instance, s.executor = nil, nil
	s.closeErr = closeErr
	close(s.errors)
	close(s.closeDone)
	s.mu.Unlock()
}
