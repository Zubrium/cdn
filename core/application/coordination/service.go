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

// TaskPoolConfig is one class-isolated worker pool. Every enabled pool shares
// the Coordinator instance owner and config epoch, but owns its own executor,
// handler map and admission limits. A disabled pool registers no handlers and
// must never claim durable work.
type TaskPoolConfig struct {
	ClassID       string
	Enabled       bool
	Concurrency   int
	LeaseDuration time.Duration
	TaskTimeout   time.Duration
	PollInterval  time.Duration
	Executor      OperationExecutor
}

type ServiceConfig struct {
	Store              ServiceStore
	Fingerprint        string
	InstanceLease      time.Duration
	HeartbeatInterval  time.Duration
	RetryBase          time.Duration
	RetryMaximum       time.Duration
	MaxAttempts        int
	MaxWorkflowTasks   int
	MaxChildrenPerTask int
	WorkflowRetention  time.Duration
	TaskClasses        []string
	Pools              []TaskPoolConfig
}

// Service owns the live Coordinator instance, scoped workflow clients, safe
// task control and the explicitly configured class-isolated worker pools.
type Service struct {
	mu           sync.RWMutex
	cfg          ServiceConfig
	instance     *Instance
	executors    []*TaskExecutor
	poolClasses  []string
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
		config.RetryBase <= 0 || config.RetryMaximum < config.RetryBase || config.RetryMaximum > 24*time.Hour ||
		config.MaxAttempts < 1 || config.MaxAttempts > 100 || config.MaxWorkflowTasks < 1 || config.MaxWorkflowTasks > 100000 ||
		config.MaxChildrenPerTask < 0 || config.MaxChildrenPerTask > 256 || config.WorkflowRetention <= 0 || config.WorkflowRetention > 365*24*time.Hour {
		return nil, ErrInvalid
	}
	config.TaskClasses = append([]string(nil), config.TaskClasses...)
	seenClasses := make(map[string]struct{}, len(config.TaskClasses))
	for _, class := range config.TaskClasses {
		if !taskClassPattern.MatchString(class) {
			return nil, ErrInvalid
		}
		if _, duplicate := seenClasses[class]; duplicate {
			return nil, ErrInvalid
		}
		seenClasses[class] = struct{}{}
	}
	seenPools := make(map[string]struct{}, len(config.Pools))
	for _, pool := range config.Pools {
		if !taskClassPattern.MatchString(pool.ClassID) {
			return nil, ErrInvalid
		}
		if _, duplicate := seenPools[pool.ClassID]; duplicate {
			return nil, ErrInvalid
		}
		if _, compiled := seenClasses[pool.ClassID]; !compiled {
			return nil, ErrInvalid
		}
		seenPools[pool.ClassID] = struct{}{}
		if !pool.Enabled {
			if !isNilInterface(pool.Executor) {
				return nil, ErrInvalid
			}
			continue
		}
		if isNilInterface(pool.Executor) || pool.Concurrency < 1 || pool.Concurrency > 256 ||
			pool.LeaseDuration < 3*time.Second || pool.LeaseDuration > time.Hour ||
			pool.TaskTimeout <= 0 || pool.TaskTimeout > 24*time.Hour ||
			pool.PollInterval <= 0 || pool.PollInterval >= pool.LeaseDuration {
			return nil, ErrInvalid
		}
	}
	config.Pools = append([]TaskPoolConfig(nil), config.Pools...)
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
	executors := make([]*TaskExecutor, 0, len(s.cfg.Pools))
	poolClasses := make([]string, 0, len(s.cfg.Pools))
	for _, pool := range s.cfg.Pools {
		if !pool.Enabled {
			continue
		}
		executor, err := NewTaskExecutor(s.cfg.Store, TaskExecutorConfig{
			Owner: instance.ID(), ConfigEpoch: instance.Epoch(),
			TaskClasses: []string{pool.ClassID},
			Concurrency: pool.Concurrency, LeaseDuration: pool.LeaseDuration,
			TaskTimeout: pool.TaskTimeout, PollInterval: pool.PollInterval,
			RetryBase: s.cfg.RetryBase, RetryMaximum: s.cfg.RetryMaximum,
			MaxWorkflowTasks: s.cfg.MaxWorkflowTasks, MaxChildrenPerTask: s.cfg.MaxChildrenPerTask,
			WorkflowRetention: s.cfg.WorkflowRetention,
		}, pool.Executor)
		if err != nil {
			for _, started := range executors {
				_ = started.Close(context.Background())
			}
			s.abortStart()
			return errors.Join(err, closeInstanceAfterStartFailure(instance, s.cfg.InstanceLease))
		}
		executors = append(executors, executor)
		poolClasses = append(poolClasses, pool.ClassID)
	}
	monitorCtx, monitorStop := context.WithCancel(context.Background())
	s.mu.Lock()
	s.instance, s.executors, s.poolClasses = instance, executors, poolClasses
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

// Activate starts every enabled class pool only after engines and presentation
// components are initialized and ready. Pools are independent: one saturated
// class must not change another class's admission or handler map.
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
	executors := append([]*TaskExecutor(nil), s.executors...)
	poolClasses := append([]string(nil), s.poolClasses...)
	s.mu.Unlock()
	for index, executor := range executors {
		if err := executor.Start(ctx); err != nil {
			s.forgetExecutors(executors[:index])
			return err
		}
		if !executor.Healthy() {
			s.forgetExecutors(executors[:index+1])
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
	for index, executor := range executors {
		s.monitor(fmt.Sprintf("task pool %s", poolClasses[index]), executor.Errors())
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

// forgetExecutors drops pools that were already closed during a failed
// activation so shutdown never closes the same executor twice. A failed
// activation is terminal: retrying it would see an empty executor set and could
// make the service Ready while no worker pool can claim work.
func (s *Service) forgetExecutors(closed []*TaskExecutor) {
	for _, executor := range closed {
		_ = executor.Close(context.Background())
	}
	s.mu.Lock()
	s.executors = nil
	s.poolClasses = nil
	s.failed, s.ready = true, false
	s.mu.Unlock()
	select {
	case s.errors <- errors.New("Coordinator task pool activation failed"):
	default:
	}
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
	instance, executors := s.instance, s.executors
	s.mu.RUnlock()
	if !ready || !activated || closing || failed || instance == nil || !instance.Ready() {
		return false
	}
	for _, executor := range executors {
		if !executor.Healthy() {
			return false
		}
	}
	return true
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

func (s *Service) NewWorkflowClient(taskClass string) (*WorkflowClient, error) {
	if s == nil || !containsTaskClass(s.cfg.TaskClasses, taskClass) {
		return nil, ErrTaskClassUnavailable
	}
	// Engines construct their scoped workflow clients during composition,
	// before isolated task pools are activated. The instance lease already
	// fixes the owner and config epoch at this point; full readiness is only
	// required once the outer runtime starts dispatching work.
	s.mu.RLock()
	instance, closing, failed := s.instance, s.closing, s.failed
	s.mu.RUnlock()
	if instance == nil || closing || failed || !instance.Ready() {
		return nil, ErrInstanceNotReady
	}
	return NewWorkflowClient(WorkflowClientConfig{Store: s.cfg.Store, TaskClass: taskClass, TaskClasses: s.cfg.TaskClasses,
		InstanceID: instance.ID(), ConfigEpoch: instance.Epoch(), MaxAttempts: s.cfg.MaxAttempts,
		Retention: s.cfg.WorkflowRetention})
}

func containsTaskClass(classes []string, candidate string) bool {
	for _, class := range classes {
		if class == candidate {
			return true
		}
	}
	return false
}

// NewWorkflowControl is safe only behind an authenticated, separately
// authorized presentation handler. It exposes summaries, task graphs, and
// task retry without exposing payloads or results.
func (s *Service) NewWorkflowControl() (*WorkflowControl, error) {
	if s == nil {
		return nil, ErrInstanceNotReady
	}
	// Workflow inspection is a read capability. Composition and read-only
	// Admin handlers construct it after the instance lease is registered but
	// before durable workers are activated; requiring full readiness here
	// would make that startup order impossible.
	s.mu.RLock()
	instance, closing, failed := s.instance, s.closing, s.failed
	s.mu.RUnlock()
	if instance == nil || closing || failed || !instance.Ready() {
		return nil, ErrInstanceNotReady
	}
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
	executors, instance, monitorStop := s.executors, s.instance, s.monitorStop
	s.mu.Unlock()
	s.shutdownOnce.Do(func() {
		go s.shutdown(executors, instance, monitorStop)
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
func (s *Service) shutdown(executors []*TaskExecutor, instance *Instance, monitorStop context.CancelFunc) {
	if monitorStop != nil {
		monitorStop()
	}
	var closeErr error
	for _, executor := range executors {
		closeErr = errors.Join(closeErr, executor.Close(context.Background()))
	}
	if instance != nil {
		closeErr = errors.Join(closeErr, instance.Close(context.Background()))
	}
	s.monitorWG.Wait()
	s.mu.Lock()
	s.instance, s.executors, s.poolClasses = nil, nil, nil
	s.closeErr = closeErr
	close(s.errors)
	close(s.closeDone)
	s.mu.Unlock()
}
