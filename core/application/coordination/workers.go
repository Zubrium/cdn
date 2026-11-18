package coordinator

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	corecoord "github.com/Zubrium/cdn/core/domain/coordination"
	"github.com/google/uuid"
)

const MaxTaskResultBytes = 1 << 20

var taskTypePattern = regexp.MustCompile(`^[a-z][a-z0-9._/-]{0,127}$`)
var taskErrorCodePattern = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,127}$`)

var ErrCoordinatorLeaseLost = corecoord.ErrCoordinatorLeaseLost
var ErrCoordinatorJobNotOwned = corecoord.ErrCoordinatorJobNotOwned

type TaskStore = corecoord.TaskRepository

// CoreJob keeps the worker package decoupled from Core's persistence package.
// The Core presentation facade maps the capability onto this DTO.
type CoreJob = corecoord.CoordinatorJob

// TaskHandler must return when ctx is cancelled. WorkerPool cannot forcibly
// stop an in-process goroutine; non-cooperative work requires process isolation.
type TaskHandler func(context.Context, CoreJob) (corecoord.CoordinatorTaskOutcome, error)

type TaskFailure struct {
	Code      string
	Retryable bool
}

func (e *TaskFailure) Error() string {
	if e == nil {
		return "task failed"
	}
	return e.Code
}

func NewTaskFailure(code string, retryable bool) *TaskFailure {
	if !taskErrorCodePattern.MatchString(code) {
		return &TaskFailure{Code: "task_failed", Retryable: retryable}
	}
	return &TaskFailure{Code: code, Retryable: retryable}
}

type WorkerConfig struct {
	Owner              uuid.UUID
	ConfigEpoch        int64
	Concurrency        int
	LeaseDuration      time.Duration
	TaskTimeout        time.Duration
	PollInterval       time.Duration
	RetryBase          time.Duration
	RetryMaximum       time.Duration
	MaxWorkflowTasks   int
	MaxChildrenPerTask int
	WorkflowRetention  time.Duration
}

type WorkerPool struct {
	store    TaskStore
	cfg      WorkerConfig
	handlers map[string]TaskHandler

	mu      sync.Mutex
	started bool
	cancel  context.CancelFunc
	ctx     context.Context
	done    chan struct{}
	errors  chan error
	failed  atomic.Bool
}

var (
	ErrWorkerAlreadyStarted = errors.New("coordinator worker pool already started")
	ErrWorkerNotStarted     = errors.New("coordinator worker pool has not started")
	ErrWorkerStore          = errors.New("coordinator worker could not persist task state")
)

func NewWorkerPool(store TaskStore, cfg WorkerConfig, handlers map[string]TaskHandler) (*WorkerPool, error) {
	if isNilInterface(store) || cfg.Owner == uuid.Nil || cfg.ConfigEpoch <= 0 || cfg.Concurrency < 1 || cfg.Concurrency > 256 ||
		cfg.LeaseDuration < 3*time.Second || cfg.LeaseDuration > time.Hour || cfg.TaskTimeout <= 0 || cfg.TaskTimeout > 24*time.Hour || cfg.PollInterval <= 0 || cfg.PollInterval >= cfg.LeaseDuration ||
		cfg.RetryBase <= 0 || cfg.RetryMaximum < cfg.RetryBase || cfg.RetryMaximum > 24*time.Hour ||
		cfg.MaxWorkflowTasks < 1 || cfg.MaxWorkflowTasks > 100000 || cfg.MaxChildrenPerTask < 0 || cfg.MaxChildrenPerTask > 256 ||
		cfg.WorkflowRetention <= 0 || cfg.WorkflowRetention > 365*24*time.Hour || len(handlers) == 0 {
		return nil, ErrInvalid
	}
	copyHandlers := make(map[string]TaskHandler, len(handlers))
	for dispatchKey, handler := range handlers {
		if !validTaskHandlerKey(dispatchKey) || handler == nil {
			return nil, ErrInvalid
		}
		copyHandlers[dispatchKey] = handler
	}
	return &WorkerPool{store: store, cfg: cfg, handlers: copyHandlers, done: make(chan struct{}), errors: make(chan error, 1)}, nil
}

func (p *WorkerPool) Start(ctx context.Context) error {
	if p == nil || ctx == nil {
		return ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.started {
		return ErrWorkerAlreadyStarted
	}
	workerCtx, cancel := context.WithCancel(ctx)
	p.cancel = cancel
	p.ctx = workerCtx
	p.started = true
	var workers sync.WaitGroup
	for index := 0; index < p.cfg.Concurrency; index++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			p.run(workerCtx)
		}()
	}
	go func() {
		workers.Wait()
		close(p.done)
		close(p.errors)
	}()
	return nil
}

func (p *WorkerPool) Errors() <-chan error {
	if p == nil {
		ch := make(chan error)
		close(ch)
		return ch
	}
	return p.errors
}

func (p *WorkerPool) Healthy() bool {
	if p == nil || p.failed.Load() {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.started && p.ctx != nil && p.ctx.Err() == nil
}

func (p *WorkerPool) Close(ctx context.Context) error {
	if p == nil {
		return nil
	}
	if ctx == nil {
		return ErrInvalid
	}
	p.mu.Lock()
	if !p.started {
		p.mu.Unlock()
		return ErrWorkerNotStarted
	}
	p.cancel()
	p.mu.Unlock()
	select {
	case <-p.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *WorkerPool) run(ctx context.Context) {
	for ctx.Err() == nil {
		jobs, err := p.store.Claim(ctx, p.cfg.Owner, p.cfg.ConfigEpoch, 1, p.cfg.LeaseDuration, p.cfg.WorkflowRetention)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if errors.Is(err, ErrCoordinatorLeaseLost) {
				p.report(ErrInstanceNotReady)
				return
			}
			p.report(ErrWorkerStore)
			return
		}
		if len(jobs) == 0 {
			if !waitContext(ctx, p.cfg.PollInterval) {
				return
			}
			continue
		}
		p.execute(ctx, jobs[0])
	}
}

func (p *WorkerPool) execute(ctx context.Context, job CoreJob) {
	handler := p.handlers[taskHandlerKey(job.ComponentID, job.OperationID, job.ContractVersion)]
	if handler == nil {
		p.fail(ctx, job, NewTaskFailure("handler_unavailable", false))
		return
	}
	taskCtx, cancelTask := context.WithTimeout(ctx, p.cfg.TaskTimeout)
	heartbeatDone := make(chan struct{})
	heartbeatFailure := make(chan error, 1)
	go p.renewTaskLease(taskCtx, cancelTask, job, heartbeatDone, heartbeatFailure)
	result, handlerErr := invokeTaskHandler(taskCtx, handler, job)
	taskErr := taskCtx.Err()
	cancelTask()
	<-heartbeatDone
	select {
	case <-ctx.Done():
		return
	default:
	}
	if errors.Is(taskErr, context.DeadlineExceeded) {
		p.fail(ctx, job, NewTaskFailure("task_timeout", true))
		return
	}
	select {
	case heartbeatErr := <-heartbeatFailure:
		if errors.Is(heartbeatErr, ErrCoordinatorJobNotOwned) {
			return
		}
		if errors.Is(heartbeatErr, ErrCoordinatorLeaseLost) {
			p.report(ErrInstanceNotReady)
			return
		}
		p.report(ErrWorkerStore)
		return
	default:
	}
	if handlerErr != nil {
		failure := NewTaskFailure("task_failed", true)
		var typed *TaskFailure
		if errors.As(handlerErr, &typed) && typed != nil && taskErrorCodePattern.MatchString(typed.Code) {
			failure = typed
		}
		p.fail(ctx, job, failure)
		return
	}
	if !validTaskOutcome(result, p.cfg.MaxChildrenPerTask) {
		p.fail(ctx, job, NewTaskFailure("invalid_task_result", false))
		return
	}
	for _, child := range result.Children {
		if p.handlers[taskHandlerKey(child.ComponentID, child.OperationID, child.ContractVersion)] == nil {
			p.fail(ctx, job, NewTaskFailure("handler_unavailable", false))
			return
		}
	}
	ackCtx, cancel := context.WithTimeout(ctx, min(p.cfg.LeaseDuration/4, 5*time.Second))
	err := p.store.Complete(ackCtx, job.ID, job.LeaseOwner, job.ConfigEpoch, job.FencingToken,
		p.cfg.MaxWorkflowTasks, p.cfg.MaxChildrenPerTask, p.cfg.WorkflowRetention, result)
	cancel()
	if errors.Is(err, corecoord.ErrLimit) || errors.Is(err, corecoord.ErrInvalid) {
		p.fail(ctx, job, NewTaskFailure("workflow_limit_exceeded", false))
		return
	}
	if err != nil && !errors.Is(err, ErrCoordinatorJobNotOwned) && ctx.Err() == nil {
		p.report(ErrWorkerStore)
	}
}

func invokeTaskHandler(ctx context.Context, handler TaskHandler, job CoreJob) (result corecoord.CoordinatorTaskOutcome, err error) {
	defer func() {
		if recover() != nil {
			result = corecoord.CoordinatorTaskOutcome{}
			err = NewTaskFailure("worker_panic", true)
		}
	}()
	return handler(ctx, job)
}

func (p *WorkerPool) renewTaskLease(ctx context.Context, cancel context.CancelFunc, job CoreJob, done chan<- struct{}, failures chan<- error) {
	defer close(done)
	interval := p.cfg.LeaseDuration / 3
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			opCtx, stop := context.WithTimeout(ctx, max(time.Second, p.cfg.LeaseDuration/6))
			err := p.store.Extend(opCtx, job.ID, job.LeaseOwner, job.ConfigEpoch, job.FencingToken, p.cfg.LeaseDuration)
			stop()
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				select {
				case failures <- err:
				default:
				}
				cancel()
				return
			}
		}
	}
}

func (p *WorkerPool) fail(ctx context.Context, job CoreJob, failure *TaskFailure) {
	if ctx.Err() != nil {
		return
	}
	delay := time.Duration(0)
	if failure.Retryable {
		delay = retryDelay(p.cfg, job.ID, job.Attempts)
	}
	ackCtx, cancel := context.WithTimeout(ctx, min(p.cfg.LeaseDuration/4, 5*time.Second))
	err := p.store.Fail(ackCtx, job.ID, job.LeaseOwner, job.ConfigEpoch, job.FencingToken,
		failure.Code, failure.Retryable, delay, p.cfg.WorkflowRetention)
	cancel()
	if err != nil && !errors.Is(err, ErrCoordinatorJobNotOwned) && ctx.Err() == nil {
		p.report(ErrWorkerStore)
	}
}

func retryDelay(cfg WorkerConfig, jobID uuid.UUID, attempt int) time.Duration {
	delay := cfg.RetryBase
	for i := 1; i < attempt && delay < cfg.RetryMaximum; i++ {
		if delay > cfg.RetryMaximum/2 {
			delay = cfg.RetryMaximum
			break
		}
		delay *= 2
	}
	if delay > cfg.RetryMaximum {
		delay = cfg.RetryMaximum
	}
	checksum := sha256.Sum256([]byte(fmt.Sprintf("%s:%d", jobID, attempt)))
	variation := int64(checksum[0])%401 - 200 // stable jitter in [-20%, +20%]
	adjustment := time.Duration(math.Round(float64(delay) * float64(variation) / 1000))
	delay += adjustment
	if delay < 0 {
		return 0
	}
	return min(delay, cfg.RetryMaximum)
}

func validTaskOutcome(outcome corecoord.CoordinatorTaskOutcome, maxChildren int) bool {
	result := outcome.Result
	if len(result) == 0 || len(result) > MaxTaskResultBytes || !utf8.Valid(result) || !json.Valid(result) {
		return false
	}
	var value map[string]json.RawMessage
	if json.Unmarshal(result, &value) != nil || value == nil || maxChildren < 0 || len(outcome.Children) > maxChildren {
		return false
	}
	seen := make(map[string]struct{}, len(outcome.Children))
	for _, child := range outcome.Children {
		if !taskComponentPattern.MatchString(child.ComponentID) || !taskTypePattern.MatchString(child.OperationID) ||
			child.ContractVersion < 1 || child.ContractVersion > 1<<16 || !validCoordinatorIdempotencyKey(child.EmissionKey) ||
			!validTaskPayload(child.Payload) || child.MaxAttempts < 1 || child.MaxAttempts > 100 ||
			len(child.DependsOn) > 256 || !validAvailableAt(child.AvailableAt) {
			return false
		}
		if _, duplicate := seen[child.EmissionKey]; duplicate {
			return false
		}
		seen[child.EmissionKey] = struct{}{}
	}
	return true
}

var taskComponentPattern = regexp.MustCompile(`^[a-z][a-z0-9.-]{0,63}$`)

func validTaskPayload(payload json.RawMessage) bool {
	if len(payload) == 0 || len(payload) > MaxTaskResultBytes || !utf8.Valid(payload) || !json.Valid(payload) {
		return false
	}
	var value map[string]json.RawMessage
	return json.Unmarshal(payload, &value) == nil && value != nil
}

func validAvailableAt(value time.Time) bool {
	return value.IsZero() || (value.Year() >= 2000 && value.Year() <= 9999)
}

func waitContext(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (p *WorkerPool) report(err error) {
	if p == nil || err == nil {
		return
	}
	p.failed.Store(true)
	p.mu.Lock()
	cancel := p.cancel
	p.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	select {
	case p.errors <- err:
	default:
	}
}
