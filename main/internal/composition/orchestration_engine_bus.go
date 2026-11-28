package composition

import (
	"context"

	websiteapp "github.com/Zubrium/cdn/engines/website/application"
	websiteapi "github.com/Zubrium/cdn/engines/website/presentation"
	"github.com/Zubrium/cdn/main/internal/bus"
	"github.com/Zubrium/cdn/main/internal/publication"
	"github.com/Zubrium/cdn/pkg/contracts"
	prerendercontract "github.com/Zubrium/cdn/pkg/contracts/prerender"
)

const (
	publicationCaller = "main.publication"
	retirementCaller  = "composition.retirement"
)

type orchestrationEngines struct {
	website                  *websiteEngineComponent
	router                   *routerEngineComponent
	publicationWebsiteSecret string
	publicationRouterSecret  string
	retirementWebsiteSecret  string
	retirementRouterSecret   string
}

func orchestrationEngineKeys(publicationEnabled, retirementEnabled bool) []contracts.Key {
	var keys []contracts.Key
	if publicationEnabled || retirementEnabled {
		keys = append(keys, contracts.OrchestrationRouteReadOperation().Key)
	}
	if publicationEnabled {
		keys = append(keys, contracts.PublicationInputOperation().Key, contracts.PublicationWebsiteBuildStartOperation().Key,
			contracts.PublicationWebsiteBuildReadOperation().Key, contracts.PublicationRouteCASOperation().Key)
	}
	if retirementEnabled {
		keys = append(keys, contracts.RetirementRouteDeleteOperation().Key, contracts.RetirementWebsiteReadOperation().Key,
			contracts.RetirementWebsiteTransitionOperation().Key)
	}
	return keys
}

func (engines orchestrationEngines) register(dispatcher *bus.Dispatcher, publicationEnabled, retirementEnabled bool) error {
	if publicationEnabled || retirementEnabled {
		if err := bus.Register(dispatcher, contracts.OrchestrationRouteReadOperation(), engines.readRoute); err != nil {
			return err
		}
	}
	if publicationEnabled {
		if err := bus.Register(dispatcher, contracts.PublicationInputOperation(), engines.publicationInput); err != nil {
			return err
		}
		if err := bus.Register(dispatcher, contracts.PublicationWebsiteBuildStartOperation(), engines.startBuild); err != nil {
			return err
		}
		if err := bus.Register(dispatcher, contracts.PublicationWebsiteBuildReadOperation(), engines.readBuild); err != nil {
			return err
		}
		if err := bus.Register(dispatcher, contracts.PublicationRouteCASOperation(), engines.activate); err != nil {
			return err
		}
	}
	if retirementEnabled {
		if err := bus.Register(dispatcher, contracts.RetirementRouteDeleteOperation(), engines.withdraw); err != nil {
			return err
		}
		if err := bus.Register(dispatcher, contracts.RetirementWebsiteReadOperation(), engines.readPage); err != nil {
			return err
		}
		if err := bus.Register(dispatcher, contracts.RetirementWebsiteTransitionOperation(), engines.transitionPage); err != nil {
			return err
		}
	}
	return nil
}

func (engines orchestrationEngines) websiteContext(ctx context.Context, owner, credential string) (context.Context, error) {
	if bus.ModuleIDFromContext(ctx) != owner {
		return nil, bus.ErrInvalidModuleIdentity
	}
	if credential == "" || engines.website == nil || engines.website.service == nil {
		return nil, websiteapi.ErrUnavailable
	}
	return engines.website.service.Authenticate(ctx, credential)
}

func (engines orchestrationEngines) routerContext(ctx context.Context) (context.Context, error) {
	var secret string
	switch bus.ModuleIDFromContext(ctx) {
	case publicationCaller:
		secret = engines.publicationRouterSecret
	case retirementCaller:
		secret = engines.retirementRouterSecret
	default:
		return nil, bus.ErrInvalidModuleIdentity
	}
	if secret == "" || engines.router == nil || engines.router.api == nil {
		return nil, contracts.ErrPublicUnavailable
	}
	return engines.router.api.Authenticate(ctx, secret)
}

func (engines orchestrationEngines) publicationInput(ctx context.Context, request contracts.PublicationInputRequest) (contracts.PublicationInputResponse, error) {
	website, err := engines.websiteContext(ctx, publicationCaller, engines.publicationWebsiteSecret)
	if err != nil {
		return contracts.PublicationInputResponse{}, err
	}
	if engines.website.localeCatalog == nil || engines.website.inputResolver == nil {
		return contracts.PublicationInputResponse{}, publication.ErrUnavailable
	}
	snapshot, err := engines.website.service.GetSnapshot(website, request.SnapshotID)
	if err != nil {
		return contracts.PublicationInputResponse{}, err
	}
	if snapshot.ID != request.SnapshotID || snapshot.State != "ready" {
		return contracts.PublicationInputResponse{}, publication.ErrInvalid
	}
	revision, err := engines.website.localeCatalog.LocaleCatalogRevision(website, request.Locale)
	if err != nil || revision < 1 {
		return contracts.PublicationInputResponse{}, publication.ErrUnavailable
	}
	input, err := resolvePinnedBuildInput(engines.website.inputResolver, website, snapshot, request.Locale, revision, request.ContentRevision)
	if err != nil {
		return contracts.PublicationInputResponse{}, err
	}
	if input.SnapshotID != request.SnapshotID || input.Locale != request.Locale ||
		input.ContentPublicationRevision != request.ContentRevision {
		return contracts.PublicationInputResponse{}, publication.ErrStale
	}
	digest, err := prerendercontract.InputDigest(input)
	if err != nil {
		return contracts.PublicationInputResponse{}, err
	}
	return contracts.PublicationInputResponse{Digest: digest, SystemModelRevision: input.SystemModelRevision}, nil
}

func resolvePinnedBuildInput(resolver websiteapp.BuildInputResolver, ctx context.Context, snapshot websiteapi.Snapshot,
	locale string, catalogRevision, contentRevision int64) (prerendercontract.TrustedInput, error) {
	if contentRevision < 0 {
		return prerendercontract.TrustedInput{}, publication.ErrInvalid
	}
	if contentRevision == 0 {
		return resolver.ResolveBuildInput(ctx, snapshot, locale, catalogRevision)
	}
	pinned, ok := resolver.(websiteapp.PinnedBuildInputResolver)
	if !ok {
		return prerendercontract.TrustedInput{}, publication.ErrUnavailable
	}
	return pinned.ResolveBuildInputPinned(ctx, snapshot, locale, catalogRevision, contentRevision)
}

func (engines orchestrationEngines) startBuild(ctx context.Context, request contracts.PublicationWebsiteBuildRequest) (contracts.AdminWebsiteBuildJob, error) {
	website, err := engines.websiteContext(ctx, publicationCaller, engines.publicationWebsiteSecret)
	if err != nil {
		return contracts.AdminWebsiteBuildJob{}, err
	}
	return engines.website.service.BuildSnapshot(website, websiteapi.BuildSnapshotRequest{
		SnapshotID: request.SnapshotID, Locale: request.Locale,
		ExpectedContentRevision: request.ContentRevision, IdempotencyKey: request.IdempotencyKey,
	})
}

func (engines orchestrationEngines) readBuild(ctx context.Context, request contracts.OrchestrationObjectRequest) (contracts.AdminWebsiteBuildJob, error) {
	website, err := engines.websiteContext(ctx, publicationCaller, engines.publicationWebsiteSecret)
	if err != nil {
		return contracts.AdminWebsiteBuildJob{}, err
	}
	return engines.website.service.GetBuild(website, request.ID)
}

func (engines orchestrationEngines) readRoute(ctx context.Context, request contracts.OrchestrationRouteRequest) (contracts.OrchestrationRouteState, error) {
	router, err := engines.routerContext(ctx)
	if err != nil {
		return contracts.OrchestrationRouteState{}, err
	}
	route, err := engines.router.api.GetRoute(router, request.RouteID)
	if err != nil {
		return contracts.OrchestrationRouteState{}, err
	}
	return orchestrationRouteState(route, request.Locale), nil
}

func (engines orchestrationEngines) activate(ctx context.Context, request contracts.PublicationRouteCASRequest) (contracts.OrchestrationRouteState, error) {
	if bus.ModuleIDFromContext(ctx) != publicationCaller {
		return contracts.OrchestrationRouteState{}, bus.ErrInvalidModuleIdentity
	}
	router, err := engines.routerContext(ctx)
	if err != nil {
		return contracts.OrchestrationRouteState{}, err
	}
	target := request.ReplaceLocaleTargetRequest
	target.IdempotencyKey = request.IdempotencyKey
	route, err := engines.router.api.ReplaceLocaleTarget(router, target)
	if err != nil {
		return contracts.OrchestrationRouteState{}, err
	}
	return orchestrationRouteState(route, target.Locale), nil
}

func orchestrationRouteState(route contracts.Route, locale string) contracts.OrchestrationRouteState {
	state := contracts.OrchestrationRouteState{ID: route.ID, State: route.State, Revision: route.Revision}
	if slot, found := route.Target.LocaleTarget(locale); found {
		state.Slot = &contracts.OrchestrationLocaleState{
			Locale: slot.Locale, Revision: slot.Revision, ArtifactManifestFileID: slot.ArtifactManifestFileID,
		}
	}
	return state
}

func (engines orchestrationEngines) withdraw(ctx context.Context, request contracts.DeleteRouteRequest) (struct{}, error) {
	if bus.ModuleIDFromContext(ctx) != retirementCaller {
		return struct{}{}, bus.ErrInvalidModuleIdentity
	}
	router, err := engines.routerContext(ctx)
	if err != nil {
		return struct{}{}, err
	}
	return struct{}{}, engines.router.api.DeleteRoute(router, request)
}

func (engines orchestrationEngines) readPage(ctx context.Context, request contracts.OrchestrationObjectRequest) (contracts.AdminWebsitePage, error) {
	website, err := engines.websiteContext(ctx, retirementCaller, engines.retirementWebsiteSecret)
	if err != nil {
		return contracts.AdminWebsitePage{}, err
	}
	return engines.website.service.GetPage(website, request.ID)
}

func (engines orchestrationEngines) transitionPage(ctx context.Context, request contracts.RetirementWebsiteTransitionRequest) (contracts.AdminWebsitePage, error) {
	website, err := engines.websiteContext(ctx, retirementCaller, engines.retirementWebsiteSecret)
	if err != nil {
		return contracts.AdminWebsitePage{}, err
	}
	return engines.website.service.RetirePage(website, websiteapi.RetirePageRequest(request))
}

type retirementWebsite struct {
	dispatcher contracts.Dispatcher
}

func (website retirementWebsite) page(ctx context.Context, id string) (websiteapi.Page, error) {
	return contracts.Call(ctx, website.dispatcher, contracts.RetirementWebsiteReadOperation(), contracts.OrchestrationObjectRequest{ID: id})
}

func (website retirementWebsite) transition(ctx context.Context, request websiteapi.RetirePageRequest) (websiteapi.Page, error) {
	return contracts.Call(ctx, website.dispatcher, contracts.RetirementWebsiteTransitionOperation(), contracts.RetirementWebsiteTransitionRequest(request))
}
