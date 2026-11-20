package coordinator

import (
	"github.com/Zubrium/cdn/core/domain/access"
	"github.com/Zubrium/cdn/core/domain/coordination"
)

// Descriptor registers Core's Coordinator subsystem with the registry. It is
// metadata, not an API permission
// check; operator-facing handlers must authorize their own requests.
type Descriptor struct{}

func (Descriptor) Operations() []coordination.Operation {
	return []coordination.Operation{
		{Name: "coordinator.workflow.read", Action: access.Read, ResourceType: "coordinator-workflow"},
		{Name: "coordinator.task.retry", Action: access.Manage, ResourceType: "coordinator-task"},
	}
}

var _ coordination.Module = Descriptor{}
