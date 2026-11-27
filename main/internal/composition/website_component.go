package composition

import (
	"context"
	"errors"

	websiteengine "github.com/Zubrium/cdn/engines/website"
	websiteapp "github.com/Zubrium/cdn/engines/website/application"
	websiteapi "github.com/Zubrium/cdn/engines/website/presentation"
	websiteworkflow "github.com/Zubrium/cdn/engines/website/workflow"
	"github.com/Zubrium/cdn/main/internal/bus"
	appconfig "github.com/Zubrium/cdn/main/internal/config"
	"github.com/Zubrium/cdn/main/internal/contentbridge"
	"github.com/Zubrium/cdn/pkg/contracts"
)

type websiteEngineComponent struct {
	core               *coreComponent
	config             appconfig.Resolved
	service            *websiteapp.Service
	serviceCtx         context.Context
	projectionProvider *websiteContentProjectionProvider
	localeCatalog      *websiteLocaleCatalog
	contentBuildView   *websiteContentBuildProjection
	inputResolver      websiteapp.BuildInputResolver
}

func newWebsiteEngineComponent(core *coreComponent, config appconfig.Resolved) *websiteEngineComponent {
	return &websiteEngineComponent{
		core: core, config: config,
	}
}

func (component *websiteEngineComponent) Start(ctx context.Context) error {
	if component == nil || ctx == nil || component.core == nil || component.core.runtime == nil || component.core.dispatcher == nil {
		return errors.New("Website dependencies are not initialized")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	component.core.websiteHandle.Store(component)
	data, err := component.core.registerModuleData(websiteengine.RecordDescriptor())
	if err != nil {
		return err
	}
	if err := component.core.configureWebsiteFileServices(ctx, component.config.RouterAPI.ServiceCredential); err != nil {
		return err
	}
	moduleDispatcher := bus.NewModuleDispatcher(component.core.dispatcher, "engine.website")
	ports, err := newWebsiteCorePorts(moduleDispatcher)
	if err != nil {
		return err
	}
	files, err := contracts.NewCoreFilesClient(moduleDispatcher, func() contracts.StreamRegistrar {
		return bus.NewScopedStreams()
	})
	if err != nil {
		return err
	}
	var buildWorkflow websiteapp.BuildWorkflowClient
	if websiteBuildEnabled {
		buildWorkflow = newWebsiteBuildWorkflowClient(moduleDispatcher)
	}
	bridge, err := contentbridge.NewClient(newContentBridgeWorkflowClient(moduleDispatcher), component.config.CoordinatorFingerprint)
	if err != nil {
		return err
	}
	component.localeCatalog = newWebsiteLocaleCatalog(bridge)
	component.contentBuildView = newWebsiteContentBuildProjection(bridge)
	buildInputResolver, err := websiteapp.NewModuleBuildInputResolver(data, websiteContentBuildProjectionAdapter{client: component.contentBuildView}, component.config.Prerender.SandboxImage)
	if err != nil {
		return err
	}
	serviceConfig := websiteapp.Config{
		AccessGroup:            "features",
		CoordinatorAccessGroup: component.config.Coordinator.WorkflowAccessGroup,
		BuildWorkflow:          buildWorkflow,
		LocaleCatalog:          component.localeCatalog,
		BuildInputResolver:     buildInputResolver,
		BuildFiles:             moduleCoreFiles{files: files},
		PrerenderImageDigest:   component.config.Prerender.SandboxImage,
		BuildOperationID:       websiteworkflow.OperationID,
		BuildContractVersion:   websiteworkflow.ContractVersion,
		FeatureImportFileSetID: component.config.FeatureImport.TargetFileSetID,
	}
	if component.config.FeatureImport.Enabled {
		importDispatcher := bus.NewModuleDispatcher(component.core.dispatcher, "engine.website.import")
		importFiles, err := contracts.NewCoreFilesClient(importDispatcher, func() contracts.StreamRegistrar {
			return bus.NewScopedStreams()
		})
		if err != nil {
			return err
		}
		serviceConfig.Files = moduleCoreFiles{files: importFiles}
		serviceConfig.Workflow = newWebsiteBuildWorkflowClient(importDispatcher)
		serviceConfig.Sessions = ports.Sessions
	}
	if component.config.Media.Enabled {
		state, err := component.core.MediaSandboxState()
		if err != nil {
			return err
		}
		serviceConfig.MediaWorkflow = newWebsiteBuildWorkflowClient(moduleDispatcher)
		serviceConfig.MediaFiles = moduleCoreFiles{files: files}
		serviceConfig.MediaOperationID = websiteworkflow.MediaOperationID
		serviceConfig.MediaContractVersion = websiteworkflow.MediaContractVersion
		serviceConfig.MediaProcessorVersion = component.config.Media.ProcessorVersion
		serviceConfig.MediaSandbox = staticMediaSandboxProbe{state: state}
	}
	service, err := websiteapp.NewService(serviceConfig, data, ports.Authority, ports.Sessions)
	if err != nil {
		return err
	}
	serviceCtx, err := service.Authenticate(ctx, component.config.ContentAPI.ServiceCredential)
	if err != nil || serviceCtx == nil {
		return errors.New("Website service context could not be established")
	}
	projectionProvider, err := newWebsiteContentProjectionProvider(service, serviceCtx, component.config.ContentAPI.Locale)
	if err != nil {
		return err
	}
	component.service, component.serviceCtx, component.projectionProvider = service, serviceCtx, projectionProvider
	component.inputResolver = buildInputResolver
	return nil
}

type staticMediaSandboxProbe struct {
	state websiteapi.MediaSandboxState
}

func (probe staticMediaSandboxProbe) MediaSandboxState() websiteapi.MediaSandboxState {
	return probe.state
}

func (component *websiteEngineComponent) Ready(context.Context) error {
	if component == nil || component.service == nil {
		return errors.New("Website Engine is not initialized")
	}
	return nil
}

func (component *websiteEngineComponent) Stop(context.Context) error {
	if component != nil {
		component.service = nil
		component.localeCatalog = nil
	}
	return nil
}

func (component *websiteEngineComponent) IsReady() bool {
	return component != nil && component.service != nil
}

func (component *websiteEngineComponent) Errors() <-chan error {
	return idleFailureStream()
}

var _ interface {
	Start(context.Context) error
	Ready(context.Context) error
	Stop(context.Context) error
	IsReady() bool
	Errors() <-chan error
} = (*websiteEngineComponent)(nil)
