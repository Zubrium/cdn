package composition

import (
	"context"
	"encoding/json"
	"errors"

	platform "github.com/Zubrium/cdn/core/presentation"
	"github.com/Zubrium/cdn/main/internal/bus"
	"github.com/Zubrium/cdn/pkg/contracts"
	"github.com/google/uuid"
)

var errInvalidTaskOperation = errors.New("invalid durable operation reference")

type dataBusTaskExecutor struct {
	dispatcher *bus.Dispatcher
	allowed    map[contracts.Key]TaskOperationBinding
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
	return dataBusTaskExecutor{dispatcher: dispatcher, allowed: bindings}
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
		childBinding, registered := executor.allowed[childKey]
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
