package composition

import (
	platform "github.com/Zubrium/cdn/core/presentation"
	websiteapp "github.com/Zubrium/cdn/engines/website/application"
	websiteplatform "github.com/Zubrium/cdn/engines/website/application/platform"
	"github.com/Zubrium/cdn/main/internal/bus"
	"github.com/Zubrium/cdn/pkg/contracts"
)

// NewCoreAccess binds the neutral identity/group-access port to a module-scoped
// Core DataBus dispatcher. It is exported for acceptance tests that start a
// disposable Core runtime; production composition uses core.dispatcher directly.
func NewCoreAccess(runtime *platform.Runtime, credential string, groups []string) (contracts.CoreAccess, error) {
	dispatcher, err := newRuntimeModuleDispatcher(runtime, "engine.website", "engine.website", credential, groups)
	if err != nil {
		return nil, err
	}
	return contracts.NewCoreAccessClient(dispatcher)
}

// NewCoreRecordDataStore wraps a registered Core module-data store with the
// production neutral record adapter, including Core-to-contract error mapping.
func NewCoreRecordDataStore(store *platform.ModuleDataStore) contracts.RecordDataStore {
	return coreRecordDataStore{store: store}
}

// WebsiteCorePorts exposes neutral Website ports backed only by module-scoped
// DataBus clients. Acceptance tests share this mapping with production wiring.
type WebsiteCorePorts struct {
	Authority websiteplatform.GroupAuthorizationCapability
	Sessions  websiteplatform.SessionCapability
	Files     websiteapp.MediaFilesCapability
}

func NewWebsiteCorePorts(runtime *platform.Runtime, credential string, groups []string) (WebsiteCorePorts, error) {
	dispatcher, err := newRuntimeModuleDispatcher(runtime, "engine.website", "engine.website", credential, groups)
	if err != nil {
		return WebsiteCorePorts{}, err
	}
	return newWebsiteCorePorts(dispatcher)
}

func newWebsiteCorePorts(dispatcher contracts.Dispatcher) (WebsiteCorePorts, error) {
	access, err := contracts.NewCoreAccessClient(dispatcher)
	if err != nil {
		return WebsiteCorePorts{}, err
	}
	files, err := contracts.NewCoreFilesClient(dispatcher, func() contracts.StreamRegistrar {
		return bus.NewScopedStreams()
	})
	if err != nil {
		return WebsiteCorePorts{}, err
	}
	ports := moduleCoreFiles{files: files}
	return WebsiteCorePorts{
		Authority: moduleCoreGroupAuthorization{access: access},
		Sessions:  moduleCoreSessions{access: access},
		Files:     ports,
	}, nil
}

// NewWebsiteWorkflowClient returns the owner-scoped durable workflow client
// used by Website application. The owner is fixed by the caller, never by a
// request payload.
func NewWebsiteWorkflowClient(runtime *platform.Runtime, moduleID, credential string, groups []string) websiteapp.BuildWorkflowClient {
	dispatcher, err := newRuntimeModuleDispatcher(runtime, "engine.website", moduleID, credential, groups)
	if err != nil {
		return &websiteBuildWorkflowClient{}
	}
	return newWebsiteBuildWorkflowClient(dispatcher)
}
