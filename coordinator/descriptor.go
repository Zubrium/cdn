package coordinator

import (
	"github.com/Zubrium/cdn/core/access"
	"github.com/Zubrium/cdn/core/platform"
)

// Descriptor registers the composition-root capability with Core before Core
// issues the durable CoordinatorStore. It is metadata, not an API permission
// check; operator-facing handlers must authorize their own requests.
type Descriptor struct{}

func (Descriptor) Operations() []platform.Operation {
	return []platform.Operation{
		{Name: "coordinator.job.read", Action: access.Read, ResourceType: "coordinator-job"},
		{Name: "coordinator.job.retry", Action: access.Manage, ResourceType: "coordinator-job"},
		{Name: "coordinator.route.publish", Action: access.Publish, ResourceType: "route"},
	}
}

var _ platform.Module = Descriptor{}
