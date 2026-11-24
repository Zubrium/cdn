package composition

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	platform "github.com/Zubrium/cdn/core/presentation"
	"github.com/Zubrium/cdn/main/internal/bus"
	"github.com/Zubrium/cdn/pkg/contracts"
	"github.com/google/uuid"
)

var errInvalidTaskOperation = errors.New("invalid durable operation reference")

type dataBusTaskExecutor struct {
	dispatcher *bus.Dispatcher
	// allowed contains only the operations this executor may dispatch. For
	// isolated pools it is limited to the pool's own class.
	allowed map[contracts.Key]TaskOperationBinding
	// childBindings is the full compiled registry: a handler may legitimately
	// declare children owned by another class. Those children are claimed and
	// executed by their own pool, never by this one.
	childBindings map[contracts.Key]TaskOperationBinding
}

type TaskOperationBinding struct {
	TaskClass string
	Handler   bus.TaskOperationHandler
}

func NewDataBusTaskExecutor(dispatcher *bus.Dispatcher, allowed map[contracts.Key]TaskOperationBinding) dataBusTaskExecutor {
	bindings := make(map[contracts.Key]TaskOperationBinding, len(allowed))
	for key, binding := range allowed {
		bindings[key] = binding
	}
	return dataBusTaskExecutor{dispatcher: dispatcher, allowed: bindings, childBindings: bindings}
}

// NewClassScopedDataBusTaskExecutor gives one worker pool a dispatchable
// handler map that contains only its own class. The Core claim is also
// class-scoped, so neither executor can claim or complete another pool's task.
// Cross-class children remain resolvable from the full compiled registry.
func NewClassScopedDataBusTaskExecutor(dispatcher *bus.Dispatcher, allowed map[contracts.Key]TaskOperationBinding, classID string) (dataBusTaskExecutor, error) {
	if classID == "" || isNilDispatcher(dispatcher) {
		return dataBusTaskExecutor{}, errInvalidTaskOperation
	}
	own := make(map[contracts.Key]TaskOperationBinding)
	children := make(map[contracts.Key]TaskOperationBinding, len(allowed))
	for key, binding := range allowed {
		children[key] = binding
		if binding.TaskClass == classID {
			own[key] = binding
		}
	}
	if len(own) == 0 {
		return dataBusTaskExecutor{}, errInvalidTaskOperation
	}
	return dataBusTaskExecutor{dispatcher: dispatcher, allowed: own, childBindings: children}, nil
}

func isNilDispatcher(dispatcher *bus.Dispatcher) bool { return dispatcher == nil }

// TaskPoolSpec is the neutral per-class operational contract used to build
// Core worker pools. Retry and workflow limits stay global in Core.
type TaskPoolSpec struct {
	ClassID       string
	Concurrency   int
	LeaseDuration time.Duration
	TaskTimeout   time.Duration
	PollInterval  time.Duration
}

// NewDataBusTaskPools converts explicit per-class specs into Core pool configs
// with non-overlapping dispatchable handler maps.
func NewDataBusTaskPools(dispatcher *bus.Dispatcher, bindings map[contracts.Key]TaskOperationBinding, specs []TaskPoolSpec) ([]platform.TaskPoolConfig, error) {
	pools := make([]platform.TaskPoolConfig, 0, len(specs))
	seen := make(map[string]struct{}, len(specs))
	for _, spec := range specs {
		if _, duplicate := seen[spec.ClassID]; duplicate {
			return nil, fmt.Errorf("duplicate worker pool class %q", spec.ClassID)
		}
		seen[spec.ClassID] = struct{}{}
		executor, err := NewClassScopedDataBusTaskExecutor(dispatcher, bindings, spec.ClassID)
		if err != nil {
			return nil, fmt.Errorf("worker pool %s: %w", spec.ClassID, err)
		}
		pools = append(pools, platform.TaskPoolConfig{
			ClassID: spec.ClassID, Enabled: true, Concurrency: spec.Concurrency,
			LeaseDuration: spec.LeaseDuration, TaskTimeout: spec.TaskTimeout,
			PollInterval: spec.PollInterval, Executor: executor,
		})
	}
	return pools, nil
}

func (executor dataBusTaskExecutor) Execute(ctx context.Context, task platform.Task) (platform.TaskOutcome, error) {
	if executor.dispatcher == nil || ctx == nil || task.Operation == nil || task.Operation.Version < 1 {
		return platform.TaskOutcome{}, errInvalidTaskOperation
	}
	key := contracts.Key{ID: task.Operation.ID, Version: uint32(task.Operation.Version)}
	binding, registered := executor.allowed[key]
	if !registered || binding.TaskClass != task.Operation.ClassID {
		return platform.TaskOutcome{}, platform.NewTaskFailure("operation_unavailable", false)
	}
	request := contracts.OperationTask{
		TaskID: task.ID.String(), WorkflowID: task.WorkflowID.String(),
		IdempotencyKey: task.IdempotencyKey, EmissionKey: task.EmissionKey,
		Payload: append([]byte(nil), task.Payload...),
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return platform.TaskOutcome{}, err
	}
	response, err := executor.dispatcher.Dispatch(ctx, contracts.Envelope{OperationID: key.ID, Version: key.Version, Payload: payload})
	if err != nil {
		return platform.TaskOutcome{}, err
	}
	if response.Key() != key {
		return platform.TaskOutcome{}, contracts.ErrResponseTypeMismatch
	}
	operationOutcome, err := contracts.DecodePayload[contracts.OperationOutcome](response.Payload)
	if err != nil {
		return platform.TaskOutcome{}, err
	}
	outcome := platform.TaskOutcome{Result: append([]byte(nil), operationOutcome.Result...), Children: make([]platform.TaskChild, 0, len(operationOutcome.Children))}
	for _, child := range operationOutcome.Children {
		childKey := contracts.Key{ID: child.OperationID, Version: child.Version}
		childBinding, registered := executor.childBindings[childKey]
		if !registered || child.Version == 0 {
			return platform.TaskOutcome{}, platform.NewTaskFailure("operation_unavailable", false)
		}
		dependencies := make([]uuid.UUID, len(child.DependsOn))
		for index, dependency := range child.DependsOn {
			dependencies[index], err = uuid.Parse(dependency)
			if err != nil || dependencies[index] == uuid.Nil {
				return platform.TaskOutcome{}, platform.NewTaskFailure("operation_unavailable", false)
			}
		}
		outcome.Children = append(outcome.Children, platform.TaskChild{
			Kind:        platform.TaskKindOperation,
			Operation:   &platform.OperationRef{ClassID: childBinding.TaskClass, ID: child.OperationID, Version: int(child.Version)},
			EmissionKey: child.EmissionKey, Payload: append([]byte(nil), child.Payload...), MaxAttempts: child.MaxAttempts,
			AvailableAt: child.AvailableAt, DependsOn: dependencies,
			DependsOnEmissions: append([]string(nil), child.DependsOnEmissions...),
		})
	}
	return outcome, nil
}
