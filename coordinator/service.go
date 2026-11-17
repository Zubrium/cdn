package coordinator

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	coreplatform "github.com/Zubrium/cdn/core/platform"
	"github.com/google/uuid"
)

// ServiceStore is the Coordinator's complete typed Core boundary. Core still
// owns persistence; the Coordinator receives no database or object-store
// handles.
type ServiceStore interface {
	InstanceStore
	TaskStore
	JobEnqueueStore
	JobControlStore
	PublicationStore
}

type ServiceConfig struct {
	Store             ServiceStore
	SystemID          string
	Fingerprint       string
	InstanceLease     time.Duration
	HeartbeatInterval time.Duration
	WorkerConcurrency int
	JobLease          time.Duration
	TaskTimeout       time.Duration
	PollInterval      time.Duration
	RetryBase         time.Duration
	RetryMaximum      time.Duration
	MaxAttempts       int
	Bindings          []PublicationBinding
	Catalog           *TaskCatalog
}

// Service owns the live Coordinator instance, route scheduler, scoped engine
// schedulers, safe job control and the single system-wide worker pool.
type Service struct {
	mu          sync.RWMutex
	cfg         ServiceConfig
	instance    *Instance
	publication *PublicationScheduler
	workers     *WorkerComponent
	ready       bool
	failed      bool
	closing     bool
	started     bool
	activated   bool
	closeDone   chan struct{}
	closeErr    error
	monitorCtx  context.Context
	monitorStop context.CancelFunc
	monitorWG   sync.WaitGroup
	errors      chan error
}

func NewService(config ServiceConfig) (*Service, error) {
	if isNilInterface(config.Store) || !coordinatorSystemIDPattern.MatchString(config.SystemID) || !validFingerprint(config.Fingerprint) ||
		config.InstanceLease < 3*time.Second || config.InstanceLease > time.Hour || config.HeartbeatInterval <= 0 || config.HeartbeatInterval >= config.InstanceLease/3 ||
		config.WorkerConcurrency < 1 || config.WorkerConcurrency > 256 || config.JobLease < 3*time.Second || config.JobLease > time.Hour ||
		config.TaskTimeout <= 0 || config.TaskTimeout > 24*time.Hour || config.PollInterval <= 0 || config.PollInterval >= config.JobLease ||
		config.RetryBase <= 0 || config.RetryMaximum < config.RetryBase || config.RetryMaximum > 24*time.Hour ||
		config.MaxAttempts < 1 || config.MaxAttempts > 100 {
		return nil, ErrInvalid
	}
	bindings := append([]PublicationBinding(nil), config.Bindings...)
	if _, err := indexPublicationBindings(bindings); err != nil {
		return nil, err
	}
	if config.Catalog != nil && len(config.Catalog.handlers) == 0 {
		return nil, ErrInvalid
	}
	config.Bindings = bindings
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
		return ErrWorkerAlreadyStarted
	}
	s.started = true
	s.mu.Unlock()

	instance, err := StartInstance(ctx, s.cfg.Store, InstanceConfig{
		SystemID: s.cfg.SystemID, Fingerprint: s.cfg.Fingerprint,
		LeaseDuration: s.cfg.InstanceLease, HeartbeatInterval: s.cfg.HeartbeatInterval,
	})
	if err != nil {
		return err
	}
	publication, err := NewPublicationScheduler(PublicationSchedulerConfig{
		Store: s.cfg.Store, SystemID: s.cfg.SystemID, Owner: instance.ID(), ConfigEpoch: instance.Epoch(),
		MaxAttempts: s.cfg.MaxAttempts, Bindings: s.cfg.Bindings,
	})
	if err != nil {
		return errors.Join(err, closeInstanceAfterStartFailure(instance, s.cfg.InstanceLease))
	}
	var workers *WorkerComponent
	if s.cfg.Catalog != nil {
		workers, err = NewWorkerComponent(s.cfg.Store, WorkerConfig{
			SystemID: s.cfg.SystemID, Owner: instance.ID(), ConfigEpoch: instance.Epoch(),
			Concurrency: s.cfg.WorkerConcurrency, LeaseDuration: s.cfg.JobLease,
			TaskTimeout: s.cfg.TaskTimeout, PollInterval: s.cfg.PollInterval,
			RetryBase: s.cfg.RetryBase, RetryMaximum: s.cfg.RetryMaximum,
		}, s.cfg.Catalog.WorkerHandlers())
		if err != nil {
			return errors.Join(err, closeInstanceAfterStartFailure(instance, s.cfg.InstanceLease))
		}
	}
	monitorCtx, monitorStop := context.WithCancel(context.Background())
	s.mu.Lock()
	s.instance, s.publication, s.workers = instance, publication, workers
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
	instance, publication, closing, failed := s.instance, s.publication, s.closing, s.failed
	s.mu.RUnlock()
	if instance == nil || publication == nil || closing || failed || !instance.Ready() {
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
		return ErrWorkerAlreadyStarted
	}
	workers := s.workers
	s.mu.Unlock()
	if workers != nil {
		if err := workers.Start(ctx); err != nil {
			return err
		}
		if !workers.IsReady() {
			return ErrWorkerComponentNotReady
		}
	}
	s.mu.Lock()
	if s.closing || s.failed || s.instance == nil || !s.instance.Ready() {
		s.mu.Unlock()
		return ErrInstanceNotReady
	}
	s.activated = true
	s.mu.Unlock()
	if workers != nil {
		s.monitor("workers", workers.Errors())
	}
	return nil
}

func (s *Service) Stop(ctx context.Context) error { return s.Close(ctx) }

func closeInstanceAfterStartFailure(instance *Instance, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return instance.Close(ctx)
}

func (s *Service) monitor(component string, source <-chan error) {
	s.monitorWG.Add(1)
	go func() {
		defer s.monitorWG.Done()
		for {
			select {
			case <-s.monitorCtx.Done():
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
	ready, closing, failed := s.ready, s.closing, s.failed
	instance, workers := s.instance, s.workers
	s.mu.RUnlock()
	return ready && !closing && !failed && instance != nil && instance.Ready() && (workers == nil || workers.IsReady())
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

func (s *Service) ScheduleAutomaticPublication(ctx context.Context, routeID uuid.UUID, input PublicationInput) (int64, coreplatform.CoordinatorJob, bool, error) {
	publication := s.publicationScheduler()
	if publication == nil || !s.Ready() {
		return 0, coreplatform.CoordinatorJob{}, false, ErrInstanceNotReady
	}
	return publication.ScheduleAutomatic(ctx, routeID, input)
}

// ScheduleConfiguredRebuild is reserved for a Coordinator-owned workflow
// after its caller has authorized the operation.
func (s *Service) ScheduleConfiguredRebuild(ctx context.Context, routeID uuid.UUID, input PublicationInput) (int64, coreplatform.CoordinatorJob, bool, error) {
	publication := s.publicationScheduler()
	if publication == nil || !s.Ready() {
		return 0, coreplatform.CoordinatorJob{}, false, ErrInstanceNotReady
	}
	return publication.ScheduleRebuild(ctx, routeID, input)
}

func (s *Service) publicationScheduler() *PublicationScheduler {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.publication
}

// NewJobScheduler issues a narrow submission capability for the explicit
// task-type subset assigned to one engine adapter by the composition root.
func (s *Service) NewJobScheduler(jobTypes []string) (*JobScheduler, error) {
	if s == nil || !s.Ready() || s.cfg.Catalog == nil {
		return nil, ErrInstanceNotReady
	}
	s.mu.RLock()
	instance := s.instance
	s.mu.RUnlock()
	subset, err := s.cfg.Catalog.ForTypes(jobTypes)
	if err != nil {
		return nil, err
	}
	return NewJobScheduler(JobSchedulerConfig{Store: s.cfg.Store, SystemID: s.cfg.SystemID,
		Owner: instance.ID(), ConfigEpoch: instance.Epoch(), MaxAttempts: s.cfg.MaxAttempts, Catalog: subset})
}

// NewJobControl is safe only behind an authenticated, separately authorized
// presentation handler. It exposes bounded summaries and explicit retry.
func (s *Service) NewJobControl() (*JobControl, error) {
	if s == nil || !s.Ready() {
		return nil, ErrInstanceNotReady
	}
	s.mu.RLock()
	instance := s.instance
	s.mu.RUnlock()
	return NewJobControl(JobControlConfig{Store: s.cfg.Store, SystemID: s.cfg.SystemID,
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
	workers, instance, monitorStop := s.workers, s.instance, s.monitorStop
	s.mu.Unlock()
	if monitorStop != nil {
		monitorStop()
	}
	var closeErr error
	if workers != nil {
		closeErr = errors.Join(closeErr, workers.Stop(ctx))
	}
	if instance != nil {
		closeErr = errors.Join(closeErr, instance.Close(ctx))
	}
	s.monitorWG.Wait()
	s.mu.Lock()
	s.instance, s.publication, s.workers = nil, nil, nil
	s.closeErr = closeErr
	close(s.errors)
	close(s.closeDone)
	s.mu.Unlock()
	return closeErr
}
