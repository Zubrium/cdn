package composition

import (
	"context"
	"errors"

		websiteengine "github.com/Zubrium/cdn/engines/website"
	websiteapp "github.com/Zubrium/cdn/engines/website/application"
	websiteapi "github.com/Zubrium/cdn/engines/website/presentation"
	websiteworkflow "github.com/Zubrium/cdn/engines/website/workflow"
	appconfig "github.com/Zubrium/cdn/main/internal/config"
	"github.com/Zubrium/cdn/workers/archive"
)

type websiteEngineComponent struct {
	core             *coreComponent
	config           appconfig.Resolved
	service          *websiteapp.Service
	localeCatalog    *websiteLocaleCatalog
	contentBuildView *websiteContentBuildProjection
	inputResolver    websiteapp.BuildInputResolver
}

func newWebsiteEngineComponent(core *coreComponent, config appconfig.Resolved) *websiteEngineComponent {
	return &websiteEngineComponent{
		core: core, config: config,
		localeCatalog:    newWebsiteLocaleCatalog(),
		contentBuildView: newWebsiteContentBuildProjection(),
	}
}

func (component *websiteEngineComponent) Start(ctx context.Context) error {
	if component == nil || ctx == nil || component.core == nil || component.core.runtime == nil {
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
	var buildWorkflow websiteapp.BuildWorkflowClient
	if websiteBuildEnabled {
		buildWorkflow = newWebsiteBuildWorkflowClient(component.core, websiteworkflow.ComponentID)
	}
	if component.localeCatalog == nil {
		component.localeCatalog = newWebsiteLocaleCatalog()
	}
	if component.contentBuildView == nil {
		component.contentBuildView = newWebsiteContentBuildProjection()
	}
	buildInputResolver, err := websiteapp.NewModuleBuildInputResolver(data, component.contentBuildView, component.config.Prerender.SandboxImage)
	if err != nil {
		return err
	}
	serviceConfig := websiteapp.Config{
		AccessGroup:            "features",
		CoordinatorAccessGroup: component.config.Coordinator.WorkflowAccessGroup,
		BuildWorkflow:          buildWorkflow,
		LocaleCatalog:          component.localeCatalog,
		BuildInputResolver:     buildInputResolver,
		BuildFiles:             websiteFiles{runtime: component.core.runtime},
		PrerenderImageDigest:   component.config.Prerender.SandboxImage,
		BuildOperationID:       websiteworkflow.OperationID,
		BuildContractVersion:   websiteworkflow.ContractVersion,
		FeatureImportFileSetID: component.config.FeatureImport.TargetFileSetID,
	}
	if component.config.FeatureImport.Enabled {
		serviceConfig.Files = websiteFiles{runtime: component.core.runtime}
		serviceConfig.Workflow = newWebsiteBuildWorkflowClient(component.core, archive.DataModuleID)
		serviceConfig.Sessions = websiteSessions{runtime: component.core.runtime}
	}
	if component.config.Media.Enabled {
		state, err := component.core.MediaSandboxState()
		if err != nil {
			return err
		}
		serviceConfig.MediaWorkflow = newWebsiteBuildWorkflowClient(component.core, websiteworkflow.ComponentID)
		serviceConfig.MediaFiles = websiteFiles{runtime: component.core.runtime}
		serviceConfig.MediaOperationID = websiteworkflow.MediaOperationID
		serviceConfig.MediaContractVersion = websiteworkflow.MediaContractVersion
		serviceConfig.MediaProcessorVersion = component.config.Media.ProcessorVersion
		serviceConfig.MediaSandbox = staticMediaSandboxProbe{state: state}
	}
	service, err := websiteapp.NewService(serviceConfig, data, websiteAuthority{runtime: component.core.runtime}, websiteSessions{runtime: component.core.runtime})
	if err != nil {
		return err
	}
	component.service = service
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
