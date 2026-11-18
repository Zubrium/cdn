package coordinator

import (
	"regexp"
	"strconv"
	"strings"
)

var registeredTaskType = regexp.MustCompile(`^([a-z][a-z0-9._/-]{0,119})/v([1-9][0-9]{0,4})$`)

type taskDefinition struct {
	ComponentID     string
	OperationID     string
	ContractVersion int
	Handler         TaskHandler
}

// TaskCatalog is an immutable dispatch table built from the statically
// selected components. The persisted identity is component + operation +
// contract version; the source key is only a build-time shorthand.
type TaskCatalog struct {
	handlers    map[string]TaskHandler
	definitions map[string]taskDefinition
}

type ComponentTaskSet struct {
	ComponentID string
	Handlers    map[string]TaskHandler
}

func BuildTaskCatalog(plan BuildPlan, providers []ComponentTaskSet) (*TaskCatalog, error) {
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
	sets := make(map[string]map[string]TaskHandler, len(providers))
	for _, provider := range providers {
		if _, ok := selected[provider.ComponentID]; !ok || !componentIDPattern.MatchString(provider.ComponentID) || len(provider.Handlers) == 0 {
			return nil, ErrInvalid
		}
		if _, duplicate := sets[provider.ComponentID]; duplicate {
			return nil, ErrTaskHandlerConflict
		}
		sets[provider.ComponentID] = provider.Handlers
	}

	definitions := make(map[string]taskDefinition)
	for componentID, handlers := range sets {
		for sourceType, handler := range handlers {
			if handler == nil {
				return nil, ErrInvalid
			}
			operationID, version, ok := parseRegisteredTaskType(sourceType)
			if !ok {
				return nil, ErrInvalid
			}
			key := taskHandlerKey(componentID, operationID, version)
			if _, exists := definitions[key]; exists {
				return nil, ErrTaskHandlerConflict
			}
			definition := taskDefinition{ComponentID: componentID, OperationID: operationID,
				ContractVersion: version, Handler: handler}
			definitions[key] = definition
		}
	}
	if len(definitions) == 0 {
		return nil, nil
	}
	return &TaskCatalog{handlers: handlerMap(definitions), definitions: definitions}, nil
}

func parseRegisteredTaskType(value string) (string, int, bool) {
	match := registeredTaskType.FindStringSubmatch(value)
	if match == nil {
		return "", 0, false
	}
	version, err := strconv.Atoi(match[2])
	if err != nil || version > 1<<16 {
		return "", 0, false
	}
	return match[1], version, true
}

func taskHandlerKey(componentID, operationID string, version int) string {
	return componentID + "\x00" + operationID + "\x00" + strconv.Itoa(version)
}

func validTaskHandlerKey(value string) bool {
	parts := strings.Split(value, "\x00")
	if len(parts) != 3 || !componentIDPattern.MatchString(parts[0]) || !operationIDPattern.MatchString(parts[1]) {
		return false
	}
	version, err := strconv.Atoi(parts[2])
	return err == nil && version >= 1 && version <= 1<<16
}

func handlerMap(definitions map[string]taskDefinition) map[string]TaskHandler {
	result := make(map[string]TaskHandler, len(definitions))
	for key, definition := range definitions {
		result[key] = definition.Handler
	}
	return result
}

func (c *TaskCatalog) WorkerHandlers() map[string]TaskHandler {
	if c == nil {
		return nil
	}
	result := make(map[string]TaskHandler, len(c.handlers))
	for key, handler := range c.handlers {
		result[key] = handler
	}
	return result
}

func (c *TaskCatalog) definition(componentID, operationID string, version int) (taskDefinition, bool) {
	if c == nil {
		return taskDefinition{}, false
	}
	definition, ok := c.definitions[taskHandlerKey(componentID, operationID, version)]
	return definition, ok
}
