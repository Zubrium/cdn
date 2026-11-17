package coordinator

import (
	"sort"
	"strings"
)

// TaskCatalog is an immutable snapshot of the handlers compiled into one
// application profile. The same catalog scopes both worker dispatch and
// engine job submission, so jobs cannot be queued for an absent handler.
type TaskCatalog struct {
	handlers map[string]TaskHandler
}

// WorkerTaskSet binds one statically selected worker module to its handlers.
// Runtime composition builds the catalog from these sets and a separate
// publication handler when that automation is selected.
type WorkerTaskSet struct {
	ComponentID string
	Handlers    map[string]TaskHandler
}

// BuildTaskCatalog verifies that every selected worker has exactly one handler
// provider and that no unselected module can register work. The reserved site
// publication task is supplied only by the publication automation.
func BuildTaskCatalog(plan BuildPlan, workers []WorkerTaskSet, publication TaskHandler) (*TaskCatalog, error) {
	canonical, err := canonicalBuildPlan(append([]string(nil), plan.Components...))
	if err != nil {
		return nil, err
	}
	selectedWorkers := make(map[string]struct{})
	hasPublication := false
	for _, id := range canonical.Components {
		if strings.HasPrefix(id, "worker.") {
			selectedWorkers[id] = struct{}{}
		}
		if id == "automation.site-publication" {
			hasPublication = true
		}
	}

	providers := make(map[string]map[string]TaskHandler, len(workers))
	for _, worker := range workers {
		if _, selected := selectedWorkers[worker.ComponentID]; !selected || worker.ComponentID == "" || len(worker.Handlers) == 0 {
			return nil, ErrInvalid
		}
		if _, duplicate := providers[worker.ComponentID]; duplicate {
			return nil, ErrTaskHandlerConflict
		}
		providers[worker.ComponentID] = worker.Handlers
	}

	handlers := make(map[string]TaskHandler)
	for workerID := range selectedWorkers {
		provider, ok := providers[workerID]
		if !ok {
			return nil, ErrTaskProviderUnavailable
		}
		for jobType, handler := range provider {
			if jobType == SitePublicationJobType || handler == nil {
				return nil, ErrInvalid
			}
			if _, duplicate := handlers[jobType]; duplicate {
				return nil, ErrTaskHandlerConflict
			}
			handlers[jobType] = handler
		}
	}
	if hasPublication {
		if publication == nil {
			return nil, ErrTaskProviderUnavailable
		}
		handlers[SitePublicationJobType] = publication
	} else if publication != nil {
		return nil, ErrInvalid
	}
	if len(handlers) == 0 {
		return nil, nil
	}
	return NewTaskCatalog(handlers)
}

func NewTaskCatalog(handlers map[string]TaskHandler) (*TaskCatalog, error) {
	if len(handlers) == 0 {
		return nil, ErrInvalid
	}
	copyHandlers := make(map[string]TaskHandler, len(handlers))
	for jobType, handler := range handlers {
		if !taskTypePattern.MatchString(jobType) || handler == nil {
			return nil, ErrInvalid
		}
		copyHandlers[jobType] = handler
	}
	return &TaskCatalog{handlers: copyHandlers}, nil
}

// SubmissionTypes excludes the reserved publication type, which can only be
// scheduled through PublicationScheduler and Core's generation transaction.
func (c *TaskCatalog) SubmissionTypes() []string {
	if c == nil {
		return nil
	}
	types := make([]string, 0, len(c.handlers))
	for jobType := range c.handlers {
		if jobType != SitePublicationJobType {
			types = append(types, jobType)
		}
	}
	sort.Strings(types)
	return types
}

func (c *TaskCatalog) JobTypes() []string {
	if c == nil {
		return nil
	}
	types := make([]string, 0, len(c.handlers))
	for jobType := range c.handlers {
		types = append(types, jobType)
	}
	sort.Strings(types)
	return types
}

// ForTypes derives a per-engine submission scope from the complete compiled
// handler catalog. Unknown or repeated job types fail closed.
func (c *TaskCatalog) ForTypes(jobTypes []string) (*TaskCatalog, error) {
	if c == nil || len(jobTypes) == 0 {
		return nil, ErrInvalid
	}
	selected := make(map[string]TaskHandler, len(jobTypes))
	for _, jobType := range jobTypes {
		if jobType == SitePublicationJobType {
			return nil, ErrTaskTypeUnavailable
		}
		handler, exists := c.handlers[jobType]
		if !exists {
			return nil, ErrTaskTypeUnavailable
		}
		if _, duplicate := selected[jobType]; duplicate {
			return nil, ErrDuplicate
		}
		selected[jobType] = handler
	}
	return &TaskCatalog{handlers: selected}, nil
}

// WorkerHandlers returns an independent map so a caller cannot change the
// catalog's dispatch or submission allowlist after construction.
func (c *TaskCatalog) WorkerHandlers() map[string]TaskHandler {
	if c == nil {
		return nil
	}
	handlers := make(map[string]TaskHandler, len(c.handlers))
	for jobType, handler := range c.handlers {
		handlers[jobType] = handler
	}
	return handlers
}
