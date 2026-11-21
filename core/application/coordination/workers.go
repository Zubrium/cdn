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

var taskErrorCodePattern = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,127}$`)
var taskTypePattern = regexp.MustCompile(`^[a-z][a-z0-9._/-]{0,127}$`)

var ErrCoordinatorLeaseLost = corecoord.ErrCoordinatorLeaseLost
var ErrTaskNotOwned = corecoord.ErrTaskNotOwned

type TaskStore = corecoord.TaskRepository

// Task is the durable work unit passed to one statically registered Worker.
type Task = corecoord.Task

// WorkerHandler must return when ctx is cancelled. TaskExecutor cannot forcibly
// stop an in-process goroutine; non-cooperative work requires process isolation.
type WorkerHandler func(context.Context, Task) (corecoord.TaskOutcome, error)

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

type TaskExecutorConfig struct {
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

type TaskExecutor struct {
	store    TaskStore
	cfg      TaskExecutorConfig
	handlers map[string]WorkerHandler

	mu      sync.Mutex
	started bool
	cancel  context.CancelFunc
	ctx     context.Context
	done    chan struct{}
	errors  chan error
	failed  atomic.Bool
}

var (
	ErrTaskExecutorAlreadyStarted = errors.New("task executor already started")
	ErrTaskExecutorNotStarted     = errors.New("task executor has not started")
	ErrTaskExecutorStore          = errors.New("task executor could not persist task state")
)

func NewTaskExecutor(store TaskStore, cfg TaskExecutorConfig, handlers map[string]WorkerHandler) (*TaskExecutor, error) {
	if isNilInterface(store) || cfg.Owner == uuid.Nil || cfg.ConfigEpoch <= 0 || cfg.Concurrency < 1 || cfg.Concurrency > 256 ||
		cfg.LeaseDuration < 3*time.Second || cfg.LeaseDuration > time.Hour || cfg.TaskTimeout <= 0 || cfg.TaskTimeout > 24*time.Hour || cfg.PollInterval <= 0 || cfg.PollInterval >= cfg.LeaseDuration ||
		cfg.RetryBase <= 0 || cfg.RetryMaximum < cfg.RetryBase || cfg.RetryMaximum > 24*time.Hour ||
		cfg.MaxWorkflowTasks < 1 || cfg.MaxWorkflowTasks > 100000 || cfg.MaxChildrenPerTask < 0 || cfg.MaxChildrenPerTask > 256 ||
		cfg.WorkflowRetention <= 0 || cfg.WorkflowRetention > 365*24*time.Hour || len(handlers) == 0 {
		return nil, ErrInvalid
	}
	copyHandlers := make(map[string]WorkerHandler, len(handlers))
	for dispatchKey, handler := range handlers {
		if !validWorkerHandlerKey(dispatchKey) || handler == nil {
			return nil, ErrInvalid
		}
		copyHandlers[dispatchKey] = handler
	}
	return &TaskExecutor{store: store, cfg: cfg, handlers: copyHandlers, done: make(chan struct{}), errors: make(chan error, 1)}, nil
}

func (p *TaskExecutor) Start(ctx context.Context) error {
	if p == nil || ctx == nil {
		return ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.started {
		return ErrTaskExecutorAlreadyStarted
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

func (p *TaskExecutor) Errors() <-chan error {
	if p == nil {
		ch := make(chan error)
		close(ch)
		return ch
	}
	return p.errors
}

func (p *TaskExecutor) Healthy() bool {
	if p == nil || p.failed.Load() {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.started && p.ctx != nil && p.ctx.Err() == nil
}

func (p *TaskExecutor) Close(ctx context.Context) error {
	if p == nil {
		return nil
	}
	if ctx == nil {
		return ErrInvalid
	}
	p.mu.Lock()
	if !p.started {
		p.mu.Unlock()
		return ErrTaskExecutorNotStarted
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

func (p *TaskExecutor) run(ctx context.Context) {
	for ctx.Err() == nil {
		tasks, err := p.store.Claim(ctx, p.cfg.Owner, p.cfg.ConfigEpoch, 1, p.cfg.LeaseDuration, p.cfg.WorkflowRetention)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if errors.Is(err, ErrCoordinatorLeaseLost) {
				p.report(ErrInstanceNotReady)
				return
			}
			p.report(ErrTaskExecutorStore)
			return
		}
		if len(tasks) == 0 {
			if !waitContext(ctx, p.cfg.PollInterval) {
				return
			}
			continue
		}
		p.execute(ctx, tasks[0])
	}
}

func (p *TaskExecutor) execute(ctx context.Context, task Task) {
	if task.Kind != corecoord.TaskKindWorker || task.Worker == nil {
		p.fail(ctx, task, NewTaskFailure("invalid_task_kind", false))
		return
	}
	handler := p.handlers[workerHandlerKey(task.Worker.ComponentID, task.Worker.OperationID, task.Worker.ContractVersion)]
	if handler == nil {
		p.fail(ctx, task, NewTaskFailure("handler_unavailable", false))
		return
	}
	taskCtx, cancelTask := context.WithTimeout(ctx, p.cfg.TaskTimeout)
	heartbeatDone := make(chan struct{})
	heartbeatFailure := make(chan error, 1)
	go p.renewTaskLease(taskCtx, cancelTask, task, heartbeatDone, heartbeatFailure)
	result, handlerErr := invokeWorkerHandler(taskCtx, handler, task)
	taskErr := taskCtx.Err()
	cancelTask()
	<-heartbeatDone
	select {
	case <-ctx.Done():
		return
	default:
	}
	if errors.Is(taskErr, context.DeadlineExceeded) {
		p.fail(ctx, task, NewTaskFailure("task_timeout", true))
		return
	}
	select {
	case heartbeatErr := <-heartbeatFailure:
		if errors.Is(heartbeatErr, ErrTaskNotOwned) {
			return
		}
		if errors.Is(heartbeatErr, ErrCoordinatorLeaseLost) {
			p.report(ErrInstanceNotReady)
			return
		}
		p.report(ErrTaskExecutorStore)
		return
	default:
	}
	if handlerErr != nil {
		failure := NewTaskFailure("task_failed", true)
		var typed *TaskFailure
		if errors.As(handlerErr, &typed) && typed != nil && taskErrorCodePattern.MatchString(typed.Code) {
			failure = typed
		}
		p.fail(ctx, task, failure)
		return
	}
	if !validTaskOutcome(result, p.cfg.MaxChildrenPerTask) {
		p.fail(ctx, task, NewTaskFailure("invalid_task_result", false))
		return
	}
	for _, child := range result.Children {
		if child.Kind == corecoord.TaskKindJoin {
			continue
		}
		if child.Worker == nil || p.handlers[workerHandlerKey(child.Worker.ComponentID, child.Worker.OperationID, child.Worker.ContractVersion)] == nil {
			p.fail(ctx, task, NewTaskFailure("handler_unavailable", false))
			return
		}
	}
	ackCtx, cancel := context.WithTimeout(ctx, min(p.cfg.LeaseDuration/4, 5*time.Second))
	err := p.store.Complete(ackCtx, task.ID, task.LeaseOwner, task.ConfigEpoch, task.FencingToken,
		p.cfg.MaxWorkflowTasks, p.cfg.MaxChildrenPerTask, p.cfg.WorkflowRetention, result)
	cancel()
	if errors.Is(err, corecoord.ErrLimit) || errors.Is(err, corecoord.ErrInvalid) {
		p.fail(ctx, task, NewTaskFailure("workflow_limit_exceeded", false))
		return
	}
	if err != nil && !errors.Is(err, ErrTaskNotOwned) && ctx.Err() == nil {
		p.report(ErrTaskExecutorStore)
	}
}

func invokeWorkerHandler(ctx context.Context, handler WorkerHandler, task Task) (result corecoord.TaskOutcome, err error) {
	defer func() {
		if recover() != nil {
			result = corecoord.TaskOutcome{}
			err = NewTaskFailure("worker_panic", true)
		}
	}()
	return handler(ctx, task)
}

func (p *TaskExecutor) renewTaskLease(ctx context.Context, cancel context.CancelFunc, task Task, done chan<- struct{}, failures chan<- error) {
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
			err := p.store.Extend(opCtx, task.ID, task.LeaseOwner, task.ConfigEpoch, task.FencingToken, p.cfg.LeaseDuration)
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

func (p *TaskExecutor) fail(ctx context.Context, task Task, failure *TaskFailure) {
	if ctx.Err() != nil {
		return
	}
	delay := time.Duration(0)
	if failure.Retryable {
		delay = retryDelay(p.cfg, task.ID, task.Attempts)
	}
	ackCtx, cancel := context.WithTimeout(ctx, min(p.cfg.LeaseDuration/4, 5*time.Second))
	err := p.store.Fail(ackCtx, task.ID, task.LeaseOwner, task.ConfigEpoch, task.FencingToken,
		failure.Code, failure.Retryable, delay, p.cfg.WorkflowRetention)
	cancel()
	if err != nil && !errors.Is(err, ErrTaskNotOwned) && ctx.Err() == nil {
		p.report(ErrTaskExecutorStore)
	}
}

func retryDelay(cfg TaskExecutorConfig, taskID uuid.UUID, attempt int) time.Duration {
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
	checksum := sha256.Sum256([]byte(fmt.Sprintf("%s:%d", taskID, attempt)))
	variation := int64(checksum[0])%401 - 200 // stable jitter in [-20%, +20%]
	adjustment := time.Duration(math.Round(float64(delay) * float64(variation) / 1000))
	delay += adjustment
	if delay < 0 {
		return 0
	}
	return min(delay, cfg.RetryMaximum)
}

func validTaskOutcome(outcome corecoord.TaskOutcome, maxChildren int) bool {
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
		if !validCoordinatorIdempotencyKey(child.EmissionKey) || len(child.DependsOn) > 256 ||
			len(child.DependsOnEmissions) > 256 || len(child.DependsOn)+len(child.DependsOnEmissions) > 256 ||
			!validAvailableAt(child.AvailableAt) {
			return false
		}
		switch child.Kind {
		case corecoord.TaskKindWorker:
			if child.Worker == nil || !taskComponentPattern.MatchString(child.Worker.ComponentID) || !taskTypePattern.MatchString(child.Worker.OperationID) ||
				child.Worker.ContractVersion < 1 || child.Worker.ContractVersion > 1<<16 || !validTaskPayload(child.Payload) ||
				child.MaxAttempts < 1 || child.MaxAttempts > 100 {
				return false
			}
		case corecoord.TaskKindJoin:
			if child.Worker != nil || len(child.DependsOn)+len(child.DependsOnEmissions) == 0 || child.MaxAttempts != 0 ||
				!validTaskPayload(child.Payload) {
				return false
			}
		default:
			return false
		}
		if _, duplicate := seen[child.EmissionKey]; duplicate {
			return false
		}
		seen[child.EmissionKey] = struct{}{}
	}
	emissionKeys := make(map[string]struct{}, len(outcome.Children))
	for _, child := range outcome.Children {
		emissionKeys[child.EmissionKey] = struct{}{}
	}
	for _, child := range outcome.Children {
		seenDependencies := make(map[string]struct{}, len(child.DependsOnEmissions))
		for _, key := range child.DependsOnEmissions {
			if !validCoordinatorIdempotencyKey(key) || key == child.EmissionKey {
				return false
			}
			if _, duplicate := seenDependencies[key]; duplicate {
				return false
			}
			if _, emitted := emissionKeys[key]; !emitted {
				return false
			}
			seenDependencies[key] = struct{}{}
		}
		seenIDs := make(map[uuid.UUID]struct{}, len(child.DependsOn))
		for _, id := range child.DependsOn {
			if id == uuid.Nil {
				return false
			}
			if _, duplicate := seenIDs[id]; duplicate {
				return false
			}
			seenIDs[id] = struct{}{}
		}
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

func (p *TaskExecutor) report(err error) {
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
