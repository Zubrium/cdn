package composition

import (
	"context"
	"errors"

	platform "github.com/Zubrium/cdn/core/presentation"
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

func (component *websiteEngineComponent) Start(ctx context.Context) error {
	if component == nil || ctx == nil || component.core == nil || component.core.runtime == nil {
		return errors.New("Website dependencies are not initialized")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	component.core.websiteHandle.Store(component)
	data, err := component.core.runtime.RegisterDataModule(websiteengine.ModuleDataDescriptor())
	if err != nil {
		return err
	}
	var buildWorkflow websiteapp.BuildWorkflowClient
	if websiteBuildEnabled {
		workflowClient, err := component.core.runtime.NewWorkflowClient(websiteworkflow.ComponentID)
		if err != nil {
			return err
		}
		workflowControl, err := component.core.runtime.NewWorkflowControl()
		if err != nil {
			return err
		}
		buildWorkflow = newWebsiteBuildWorkflowClient(workflowClient, workflowClient, workflowClient, workflowControl,
			component.config.Coordinator.MaxWorkflowTasks)
	}
	localeCatalog := newWebsiteLocaleCatalog()
	contentBuildView := newWebsiteContentBuildProjection()
	buildInputResolver, err := websiteapp.NewModuleBuildInputResolver(data, contentBuildView, component.config.Prerender.SandboxImage)
	if err != nil {
		return err
	}
	serviceConfig := websiteapp.Config{
		AccessGroup:            "features",
		CoordinatorAccessGroup: component.config.Coordinator.WorkflowAccessGroup,
		BuildWorkflow:          buildWorkflow,
		LocaleCatalog:          localeCatalog,
		BuildInputResolver:     buildInputResolver,
		BuildFiles:             component.core.runtime.EngineFiles(),
		PrerenderImageDigest:   component.config.Prerender.SandboxImage,
		BuildOperationID:       websiteworkflow.OperationID,
		BuildContractVersion:   websiteworkflow.ContractVersion,
		FeatureImportFileSetID: component.config.FeatureImport.TargetFileSetID,
	}
	if component.config.FeatureImport.Enabled {
		workflow, err := component.core.runtime.NewWorkflowClient(archive.DataModuleID)
		if err != nil {
			return err
		}
		serviceConfig.Files = component.core.runtime.EngineFiles()
		serviceConfig.Workflow = workflow
		serviceConfig.Sessions = component.core.runtime.Sessions()
	}
	if component.config.Media.Enabled {
		workflowClient, err := component.core.runtime.NewWorkflowClient(websiteworkflow.ComponentID)
		if err != nil {
			return err
		}
		state, err := component.core.MediaSandboxState()
		if err != nil {
			return err
		}
		workflowControl, err := component.core.runtime.NewWorkflowControl()
		if err != nil {
			return err
		}
		serviceConfig.MediaWorkflow = newWebsiteBuildWorkflowClient(workflowClient, workflowClient, workflowClient,
			workflowControl, component.config.Coordinator.MaxWorkflowTasks)
		serviceConfig.MediaFiles = component.core.runtime.EngineFiles()
		serviceConfig.MediaOperationID = websiteworkflow.MediaOperationID
		serviceConfig.MediaContractVersion = websiteworkflow.MediaContractVersion
		serviceConfig.MediaProcessorVersion = component.config.Media.ProcessorVersion
		serviceConfig.MediaSandbox = staticMediaSandboxProbe{state: state}
	}
	service, err := websiteapp.NewService(serviceConfig, data, component.core.runtime.GroupAuthorization(), component.core.runtime.Sessions())
	if err != nil {
		return err
	}
	component.service = service
	component.localeCatalog = localeCatalog
	component.contentBuildView = contentBuildView
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

var _ platform.ManagedComponent = (*websiteEngineComponent)(nil)
