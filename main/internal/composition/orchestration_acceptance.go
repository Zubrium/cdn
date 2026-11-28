//go:build full && website_orchestration_acceptance && !devfixtures && !router

package composition

import (
	"context"
	"time"

	core "github.com/Zubrium/cdn/core/presentation"
	websiteapp "github.com/Zubrium/cdn/engines/website/application"
	"github.com/Zubrium/cdn/main/internal/bus"
	appconfig "github.com/Zubrium/cdn/main/internal/config"
	"github.com/Zubrium/cdn/main/internal/publication"
	"github.com/Zubrium/cdn/main/internal/retirement"
	"github.com/Zubrium/cdn/pkg/contracts"
)

// OrchestrationAcceptance attaches disposable services to the production task
// handlers. Only the acceptance build includes this fixture entry point.
type OrchestrationAcceptance struct {
	Runtime       *core.Runtime
	Website       *websiteapp.Service
	Router        contracts.Contract
	LocaleCatalog websiteapp.LocaleCatalogResolver
	BuildInputs   websiteapp.BuildInputResolver
	Files         core.FilesCapability
	Session       core.Session
	Credential    string
}

func (fixture OrchestrationAcceptance) Publication(ctx context.Context, store contracts.RecordDataStore, targets []publication.Target) (*publication.Service, map[contracts.Key]TaskOperationBinding, func(), error) {
	const caller = "main.publication"
	dispatcher, err := newRuntimeModuleDispatcher(fixture.Runtime, caller, "engine.publication", fixture.Credential, []string{"features"})
	if err != nil {
		return nil, nil, nil, err
	}
	service, err := publication.NewService(publication.Config{
		Store: store, Targets: targets, Starter: publicationWorkflowStarter{dispatcher: dispatcher}, MaxAttempts: 3,
	})
	if err != nil {
		return nil, nil, nil, err
	}
	keys := append(orchestrationEngineKeys(true, false), contracts.FileReadOperation().Key, contracts.FileCreateVerifiedOperation().Key)
	fileBus, err := bus.NewAllowlisted(bus.ConcurrencyLimits{MaxInFlight: 32}, keys...)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := fixture.engines().register(fileBus, true, false); err != nil {
		return nil, nil, nil, err
	}
	files := coreSessionFileAdapter{files: fixture.Files, session: fixture.Session}
	if err := bus.RegisterFileRead(fileBus, func(context.Context, string) (contracts.CoreFileReader, error) { return files, nil }); err != nil {
		return nil, nil, nil, err
	}
	if err := bus.RegisterFileCreateVerified(fileBus, func(context.Context, string) (contracts.VerifiedFileCreator, error) { return files, nil }); err != nil {
		return nil, nil, nil, err
	}
	if err := fileBus.Seal(); err != nil {
		return nil, nil, nil, err
	}
	component := fixture.component()
	component.dispatcher = fileBus
	component.config.Core.ModuleData.MaxPayloadBytes = 1 << 20
	component.config.Publication = appconfig.PublicationSettings{
		WebsiteCredential: fixture.Credential, RouterCredential: fixture.Credential,
		BuildTimeout: 30 * time.Second, PollInterval: 10 * time.Millisecond,
	}
	setPublicationWorkflowService(service)
	root, err := newPublicationRootHandler(3)
	if err != nil {
		return nil, nil, nil, err
	}
	return service, map[contracts.Key]TaskOperationBinding{
			{ID: contracts.PublicationStartOperationID, Version: contracts.PublicationStartContractVersion}:    {TaskClass: "engine.publication", Handler: root},
			{ID: contracts.PublicationBuildOperationID, Version: contracts.PublicationChildContractVersion}:    {TaskClass: "engine.website", Handler: (publicationBuildHandler{component: component}).WorkerHandler},
			{ID: contracts.PublicationActivateOperationID, Version: contracts.PublicationChildContractVersion}: {TaskClass: "engine.router", Handler: (publicationActivateHandler{component: component}).WorkerHandler},
			{ID: contracts.PublicationFinalizeOperationID, Version: contracts.PublicationChildContractVersion}: {TaskClass: "engine.website", Handler: publicationFinalizeHandler{}.WorkerHandler},
		}, func() {
			setPublicationWorkflowService(nil)
			_ = fileBus.Close(context.WithoutCancel(ctx))
		}, nil
}

func (fixture OrchestrationAcceptance) Retirement(store contracts.RecordDataStore, routes map[string][]string) (
	func(context.Context, contracts.RetirePageRequest) (contracts.PageRetirement, error),
	*retirement.Service, map[contracts.Key]TaskOperationBinding, func(), error,
) {
	const caller = "composition.retirement"
	dispatcher, err := newRuntimeModuleDispatcher(fixture.Runtime, caller, caller, fixture.Credential, []string{"features"})
	if err != nil {
		return nil, nil, nil, nil, err
	}
	service, err := retirement.NewService(retirement.Config{
		Store: store, Routes: routes, Starter: retirementWorkflowStarter{dispatcher: dispatcher}, MaxAttempts: 3,
	})
	if err != nil {
		return nil, nil, nil, nil, err
	}
	engineBus, err := bus.NewAllowlisted(bus.ConcurrencyLimits{MaxInFlight: 32}, orchestrationEngineKeys(false, true)...)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if err := fixture.engines().register(engineBus, false, true); err != nil {
		return nil, nil, nil, nil, err
	}
	if err := engineBus.Seal(); err != nil {
		return nil, nil, nil, nil, err
	}
	component := fixture.component()
	component.dispatcher = engineBus
	ingress := &retirementComponent{core: component, service: service}
	root, err := newRetirementRootHandler(3)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	setRetirementWorkflowService(service)
	return ingress.retirePage, service, map[contracts.Key]TaskOperationBinding{
			{ID: contracts.RetirementStartOperationID, Version: contracts.RetirementStartContractVersion}:        {TaskClass: caller, Handler: root},
			{ID: contracts.RetirementRouteRemoveOperationID, Version: contracts.RetirementChildContractVersion}:  {TaskClass: "engine.router", Handler: (retirementRouteRemoveHandler{component: component}).WorkerHandler},
			{ID: contracts.RetirementPageFinalizeOperationID, Version: contracts.RetirementChildContractVersion}: {TaskClass: "engine.website", Handler: (retirementFinalizeHandler{component: component}).WorkerHandler},
		}, func() {
			setRetirementWorkflowService(nil)
			_ = engineBus.Close(context.Background())
		}, nil
}

func (fixture OrchestrationAcceptance) component() *coreComponent {
	return &coreComponent{runtime: fixture.Runtime}
}

func (fixture OrchestrationAcceptance) engines() orchestrationEngines {
	return orchestrationEngines{
		website: &websiteEngineComponent{
			service: fixture.Website, localeCatalog: fixture.LocaleCatalog, inputResolver: fixture.BuildInputs,
		},
		router:                   &routerEngineComponent{api: fixture.Router},
		publicationWebsiteSecret: fixture.Credential, publicationRouterSecret: fixture.Credential,
		retirementWebsiteSecret: fixture.Credential, retirementRouterSecret: fixture.Credential,
	}
}
