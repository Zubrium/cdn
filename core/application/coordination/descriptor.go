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
		{Name: "coordinator.job.read", Action: access.Read, ResourceType: "coordinator-job"},
		{Name: "coordinator.job.retry", Action: access.Manage, ResourceType: "coordinator-job"},
	}
}

var _ coordination.Module = Descriptor{}
