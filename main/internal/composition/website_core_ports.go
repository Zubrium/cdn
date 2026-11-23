package composition

import (
	platform "github.com/Zubrium/cdn/core/presentation"
	websiteapp "github.com/Zubrium/cdn/engines/website/application"
	websiteplatform "github.com/Zubrium/cdn/engines/website/application/platform"
	"github.com/Zubrium/cdn/pkg/contracts"
)

// NewCoreAccess binds the neutral identity/group-access port to a Core
// runtime. It is the same adapter the Router composition uses for route
// identity and group authorization.
func NewCoreAccess(runtime *platform.Runtime) contracts.CoreAccess {
	return coreRuntimeAccess{runtime: runtime}
}

// NewCoreRecordDataStore wraps a registered Core module-data store with the
// production neutral record adapter, including Core-to-contract error
// mapping. Acceptance tests use it so record semantics match the composition
// root exactly.
func NewCoreRecordDataStore(store *platform.ModuleDataStore) contracts.RecordDataStore {
	return coreRecordDataStore{store: store}
}

// WebsiteCorePorts exposes the production neutral Website adapters bound to a
// Core runtime. The composition root wiring and the Website acceptance tests
// share this mapping so acceptance evidence covers the real boundary
// conversion instead of a test-only duplicate.
type WebsiteCorePorts struct {
	Authority websiteplatform.GroupAuthorizationCapability
	Sessions  websiteplatform.SessionCapability
	Files     websiteapp.MediaFilesCapability
}

// NewWebsiteCorePorts binds the neutral Website ports to the given Core
// runtime. The runtime must be initialized; only the workflow client below
// supports a nil runtime as an explicit fail-closed capability.
func NewWebsiteCorePorts(runtime *platform.Runtime) WebsiteCorePorts {
	return WebsiteCorePorts{
		Authority: websiteAuthority{runtime: runtime},
		Sessions:  websiteSessions{runtime: runtime},
		Files:     websiteFiles{runtime: runtime},
	}
}

// NewWebsiteWorkflowClient returns the owner-scoped durable workflow client
// used by Website application. The owner is fixed by the caller (composition
// or an acceptance test), never by a request.
func NewWebsiteWorkflowClient(runtime *platform.Runtime, owner string) websiteapp.BuildWorkflowClient {
	if runtime == nil {
		return &websiteBuildWorkflowClient{}
	}
	return &websiteBuildWorkflowClient{operations: coreWorkflowOperations{runtime: runtime, owner: owner}}
}
