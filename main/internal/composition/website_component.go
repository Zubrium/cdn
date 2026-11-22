package composition

import (
	"context"
	"errors"

	platform "github.com/Zubrium/cdn/core/presentation"
	websiteengine "github.com/Zubrium/cdn/engines/website"
	websiteapp "github.com/Zubrium/cdn/engines/website/application"
	websiteworkflow "github.com/Zubrium/cdn/engines/website/workflow"
	appconfig "github.com/Zubrium/cdn/main/internal/config"
)

type websiteEngineComponent struct {
	core          *coreComponent
	config        appconfig.Resolved
	service       *websiteapp.Service
	localeCatalog *websiteLocaleCatalog
}

func (component *websiteEngineComponent) Start(ctx context.Context) error {
	if component == nil || ctx == nil || component.core == nil || component.core.runtime == nil {
		return errors.New("Website dependencies are not initialized")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
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
	service, err := websiteapp.NewService(websiteapp.Config{
		AccessGroup: "features", CoordinatorAccessGroup: component.config.Coordinator.WorkflowAccessGroup,
		BuildWorkflow:        buildWorkflow,
		LocaleCatalog:        localeCatalog,
		BuildOperationID:     websiteworkflow.OperationID,
		BuildContractVersion: websiteworkflow.ContractVersion,
	}, data, component.core.runtime.GroupAuthorization(), component.core.runtime.Sessions())
	if err != nil {
		return err
	}
	component.service = service
	component.localeCatalog = localeCatalog
	return nil
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
