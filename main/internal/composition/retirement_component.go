package composition

import (
	"context"
	"errors"

	"github.com/Zubrium/cdn/main/internal/bus"
	appconfig "github.com/Zubrium/cdn/main/internal/config"
	"github.com/Zubrium/cdn/main/internal/retirement"
	"github.com/Zubrium/cdn/pkg/contracts"
)

// retirementComponent assembles the trusted durable workflow from registered
// capabilities only. The Admin HTTP ingress reaches it through the process-local
// bus; it cannot select routes or invoke Router operations directly.
type retirementComponent struct {
	core       *coreComponent
	config     appconfig.Resolved
	website    *websiteEngineComponent
	router     *routerEngineComponent
	dispatcher *bus.Dispatcher
	service    *retirement.Service
	client     *retirementClient
}

func (component *retirementComponent) Start(ctx context.Context) error {
	if component == nil || !component.config.Retirement.Enabled {
		return nil
	}
	if ctx == nil || component.core == nil || component.core.runtime == nil || component.website == nil ||
		component.website.service == nil || component.router == nil || component.router.api == nil || component.dispatcher == nil {
		return errors.New("Retirement dependencies are not initialized")
	}
	data, err := component.core.runtime.RegisterDataModule(retirement.ModuleDataDescriptor())
	if err != nil {
		return err
	}
	service, err := retirement.NewService(retirement.Config{
		BranchID:          component.config.RouterAPI.BranchID,
		Store:             data,
		Routes:            component.config.Retirement.Routes,
		Website:           component.website.service,
		WebsiteCredential: component.config.Retirement.WebsiteCredential,
		Router:            component.router.api,
		RouterCredential:  component.config.Retirement.RouterCredential,
	})
	if err != nil {
		return err
	}
	if err := bus.Register(component.dispatcher, contracts.RetirePageOperation(), service.RetirePage); err != nil {
		return err
	}
	if err := bus.Register(component.dispatcher, contracts.GetPageRetirementOperation(), service.GetPageRetirement); err != nil {
		return err
	}
	component.service = service
	component.client = &retirementClient{dispatcher: component.dispatcher}
	return nil
}

func (component *retirementComponent) Ready(context.Context) error {
	if component == nil || !component.config.Retirement.Enabled {
		return nil
	}
	if component.service == nil || component.client == nil {
		return errors.New("Retirement workflow is not initialized")
	}
	return nil
}

func (component *retirementComponent) Stop(context.Context) error {
	if component != nil {
		component.service = nil
		component.client = nil
	}
	return nil
}

func (component *retirementComponent) IsReady() bool {
	return component != nil && (!component.config.Retirement.Enabled || component.service != nil)
}

func (component *retirementComponent) Errors() <-chan error { return idleFailureStream() }

type retirementClient struct {
	dispatcher contracts.Dispatcher
}

func (client *retirementClient) RetirePage(ctx context.Context, request contracts.RetirePageRequest) (contracts.PageRetirement, error) {
	return contracts.Call(ctx, client.dispatcher, contracts.RetirePageOperation(), request)
}

func (client *retirementClient) GetPageRetirement(ctx context.Context, query contracts.PageRetirementQuery) (contracts.PageRetirement, error) {
	return contracts.Call(ctx, client.dispatcher, contracts.GetPageRetirementOperation(), query)
}
