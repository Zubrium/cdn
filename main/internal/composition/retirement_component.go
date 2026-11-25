package composition

import (
	"context"
	"errors"
	"fmt"

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
	data, err := component.core.registerModuleData(retirement.ModuleDataDescriptor())
	if err != nil {
		return err
	}
	starter := retirementWorkflowStarter{dispatcher: bus.NewModuleDispatcher(component.dispatcher, "composition.retirement")}
	service, err := retirement.NewService(retirement.Config{
		BranchID:    component.config.RouterAPI.BranchID,
		Store:       data,
		Routes:      component.config.Retirement.Routes,
		Starter:     starter,
		MaxAttempts: component.config.Coordinator.MaxAttempts,
	})
	if err != nil {
		return err
	}
	component.service = service
	setRetirementWorkflowService(service)
	setRetirementHandlerCore(component.core)
	setRetirementHandlerRouter(component.router)
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

func (component *retirementComponent) retirePage(ctx context.Context, request contracts.RetirePageRequest) (contracts.PageRetirement, error) {
	if component == nil || component.service == nil {
		return contracts.PageRetirement{}, errors.New("Retirement workflow is unavailable")
	}
	job, exists, err := component.service.FindReceipt(ctx, request)
	if err != nil {
		return contracts.PageRetirement{}, err
	}
	if exists {
		return component.service.Launch(ctx, job)
	}
	retired, err := component.service.PageIsRetired(ctx, request.PageID)
	if err != nil {
		return contracts.PageRetirement{}, err
	}
	if retired {
		return contracts.PageRetirement{}, retirement.ErrConflict
	}
	if err := validateRetirementPage(ctx, component, request); err != nil {
		return contracts.PageRetirement{}, err
	}
	job, err = component.service.Prepare(ctx, request)
	if err != nil {
		return contracts.PageRetirement{}, err
	}
	if err := markPageRetiring(ctx, component.core, job); err != nil {
		return contracts.PageRetirement{}, err
	}
	return component.service.Launch(ctx, job)
}

func validateRetirementPage(ctx context.Context, component *retirementComponent, request contracts.RetirePageRequest) error {
	if component == nil || component.core == nil || component.config.Retirement.WebsiteCredential == "" {
		return retirement.ErrUnavailable
	}
	website := component.core.websiteHandle.Load()
	if website == nil || website.service == nil {
		return retirement.ErrUnavailable
	}
	websiteCtx, err := website.service.Authenticate(ctx, component.config.Retirement.WebsiteCredential)
	if err != nil {
		return retirement.ErrUnavailable
	}
	page, err := website.service.GetPage(websiteCtx, request.PageID)
	if err != nil {
		return retirement.ErrUnavailable
	}
	if page.ID != request.PageID || page.LifecycleState != contracts.RetirementPageStateActive ||
		page.RetirementJobID != "" || request.IfMatch != fmt.Sprintf("\"%d\"", page.Revision) {
		return retirement.ErrConflict
	}
	return nil
}

func (component *retirementComponent) getPageRetirement(ctx context.Context, request contracts.PageRetirementQuery) (contracts.PageRetirement, error) {
	if component == nil || component.service == nil {
		return contracts.PageRetirement{}, errors.New("Retirement workflow is unavailable")
	}
	return component.service.GetPageRetirement(ctx, request)
}

type retirementClient struct {
	dispatcher contracts.Dispatcher
}

func (client *retirementClient) RetirePage(ctx context.Context, request contracts.RetirePageRequest) (contracts.PageRetirement, error) {
	if client == nil {
		return contracts.PageRetirement{}, contracts.ErrDispatcherUnavailable
	}
	return contracts.Call(ctx, client.dispatcher, contracts.RetirePageOperation(), request)
}

func (client *retirementClient) GetPageRetirement(ctx context.Context, query contracts.PageRetirementQuery) (contracts.PageRetirement, error) {
	if client == nil {
		return contracts.PageRetirement{}, contracts.ErrDispatcherUnavailable
	}
	return contracts.Call(ctx, client.dispatcher, contracts.GetPageRetirementOperation(), query)
}
