package composition

import (
	"context"
	"errors"

	platform "github.com/Zubrium/cdn/core/presentation"
	websiteengine "github.com/Zubrium/cdn/engines/website"
	websiteapp "github.com/Zubrium/cdn/engines/website/application"
	appconfig "github.com/Zubrium/cdn/main/internal/config"
)

type websiteEngineComponent struct {
	core    *coreComponent
	config  appconfig.Resolved
	service *websiteapp.Service
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
	service, err := websiteapp.NewService(websiteapp.Config{AccessGroup: "features", CoordinatorAccessGroup: component.config.Coordinator.WorkflowAccessGroup}, data,
		component.core.runtime.GroupAuthorization(), component.core.runtime.Sessions())
	if err != nil {
		return err
	}
	component.service = service
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
	}
	return nil
}

func (component *websiteEngineComponent) IsReady() bool {
	return component != nil && component.service != nil
}

func (component *websiteEngineComponent) Errors() <-chan error {
	closed := make(chan error)
	close(closed)
	return closed
}

var _ platform.ManagedComponent = (*websiteEngineComponent)(nil)
