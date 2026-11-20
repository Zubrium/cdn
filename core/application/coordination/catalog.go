package coordinator

import (
	"regexp"
	"strconv"
	"strings"
)

var registeredWorkerType = regexp.MustCompile(`^([a-z][a-z0-9._/-]{0,119})/v([1-9][0-9]{0,4})$`)

// Worker is a statically registered operation and its executable handler. It is
// part of the selected runtime profile and is never stored as a durable row.
type Worker struct {
	ComponentID     string
	OperationID     string
	ContractVersion int
	Handler         WorkerHandler
}

// WorkerCatalog is the immutable set of workers compiled into one runtime profile.
// A Task may reference one entry; join Tasks deliberately have no Worker.
type WorkerCatalog struct {
	workers map[string]Worker
}

type ComponentWorkerSet struct {
	ComponentID string
	Handlers    map[string]WorkerHandler
}

func BuildWorkerCatalog(plan BuildPlan, providers []ComponentWorkerSet) (*WorkerCatalog, error) {
	if len(plan.Components) == 0 {
		return nil, ErrInvalid
	}
	selected := make(map[string]struct{}, len(plan.Components))
	for _, id := range plan.Components {
		if !componentIDPattern.MatchString(id) {
			return nil, ErrInvalid
		}
		if _, duplicate := selected[id]; duplicate {
			return nil, ErrDuplicate
		}
		selected[id] = struct{}{}
	}
	sets := make(map[string]map[string]WorkerHandler, len(providers))
	for _, provider := range providers {
		if _, ok := selected[provider.ComponentID]; !ok || !componentIDPattern.MatchString(provider.ComponentID) || len(provider.Handlers) == 0 {
			return nil, ErrInvalid
		}
		if _, duplicate := sets[provider.ComponentID]; duplicate {
			return nil, ErrWorkerHandlerConflict
		}
		sets[provider.ComponentID] = provider.Handlers
	}

	workers := make(map[string]Worker)
	for componentID, handlers := range sets {
		for sourceType, handler := range handlers {
			if handler == nil {
				return nil, ErrInvalid
			}
			operationID, version, ok := parseRegisteredWorkerType(sourceType)
			if !ok {
				return nil, ErrInvalid
			}
			key := workerHandlerKey(componentID, operationID, version)
			if _, exists := workers[key]; exists {
				return nil, ErrWorkerHandlerConflict
			}
			workers[key] = Worker{ComponentID: componentID, OperationID: operationID,
				ContractVersion: version, Handler: handler}
		}
	}
	if len(workers) == 0 {
		return nil, nil
	}
	return &WorkerCatalog{workers: workers}, nil
}

func parseRegisteredWorkerType(value string) (string, int, bool) {
	match := registeredWorkerType.FindStringSubmatch(value)
	if match == nil {
		return "", 0, false
	}
	version, err := strconv.Atoi(match[2])
	if err != nil || version > 1<<16 {
		return "", 0, false
	}
	return match[1], version, true
}

func workerHandlerKey(componentID, operationID string, version int) string {
	return componentID + "\x00" + operationID + "\x00" + strconv.Itoa(version)
}

func validWorkerHandlerKey(value string) bool {
	parts := strings.Split(value, "\x00")
	if len(parts) != 3 || !componentIDPattern.MatchString(parts[0]) || !operationIDPattern.MatchString(parts[1]) {
		return false
	}
	version, err := strconv.Atoi(parts[2])
	return err == nil && version >= 1 && version <= 1<<16
}

func (c *WorkerCatalog) definition(componentID, operationID string, version int) (Worker, bool) {
	if c == nil {
		return Worker{}, false
	}
	worker, ok := c.workers[workerHandlerKey(componentID, operationID, version)]
	return worker, ok
}

func (c *WorkerCatalog) hasComponent(componentID string) bool {
	if c == nil {
		return false
	}
	for _, worker := range c.workers {
		if worker.ComponentID == componentID {
			return true
		}
	}
	return false
}

func (c *WorkerCatalog) handlers() map[string]WorkerHandler {
	if c == nil {
		return nil
	}
	result := make(map[string]WorkerHandler, len(c.workers))
	for key, worker := range c.workers {
		result[key] = worker.Handler
	}
	return result
}
