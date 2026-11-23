package composition

import (
	"context"
	"encoding/json"
	"errors"

	platform "github.com/Zubrium/cdn/core/presentation"
	contentengine "github.com/Zubrium/cdn/engines/content"
	contentapi "github.com/Zubrium/cdn/engines/content/presentation"
	websiteapp "github.com/Zubrium/cdn/engines/website/application"
	websiteapi "github.com/Zubrium/cdn/engines/website/presentation"
	appconfig "github.com/Zubrium/cdn/main/internal/config"
	prerendercontract "github.com/Zubrium/cdn/pkg/contracts/prerender"
)

type contentEngineComponent struct {
	core               *coreComponent
	config             appconfig.Resolved
	website            websiteapi.API
	engine             *contentengine.Engine
	projections        *contentProjectionProvider
	serviceCtx         context.Context
	cancel             context.CancelFunc
	buildLocaleCatalog *websiteLocaleCatalog
	buildProjection    *websiteContentBuildProjection
}

func newContentEngineComponent(core *coreComponent, config appconfig.Resolved, website websiteapi.API) *contentEngineComponent {
	return &contentEngineComponent{core: core, config: config, website: website}
}

func (component *contentEngineComponent) Start(ctx context.Context) error {
	if component == nil || ctx == nil || component.core == nil || component.core.runtime == nil || component.website == nil {
		return errors.New("Content dependencies are not initialized")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	credential := component.config.ContentAPI.ServiceCredential
	if credential == "" {
		return errors.New("Content service credential is required")
	}
	if component.config.ContentAPI.Locale == "" {
		return errors.New("Content service locale is required")
	}
	identity := component.core.runtime.EngineIdentity()
	_, principal, err := identity.Authenticate(ctx, credential)
	if err != nil {
		return errors.New("Content service credential could not be authenticated")
	}
	if principal.ID == "" || !principal.Active {
		return errors.New("Content service identity is inactive")
	}
	serviceCtx, err := component.website.Authenticate(ctx, credential)
	if err != nil || serviceCtx == nil {
		return errors.New("Content Website service context could not be established")
	}
	serviceCtx, cancel := context.WithCancel(serviceCtx)
	data, err := component.core.runtime.RegisterDataModule(contentengine.ModuleDataDescriptor())
	if err != nil {
		cancel()
		return err
	}
	provider, err := newContentProjectionProvider(component.website, serviceCtx, component.config.ContentAPI.Locale)
	if err != nil {
		cancel()
		return err
	}
	engine, err := contentengine.New(contentengine.Dependencies{
		Identity: identity, Sessions: component.core.runtime.Sessions(),
		Authorization: component.core.runtime.GroupAuthorization(), Files: component.core.runtime.EngineFiles(),
		Data: data, Projections: provider,
	})
	if err != nil {
		cancel()
		return err
	}
	component.engine, component.projections, component.serviceCtx, component.cancel = engine, provider, serviceCtx, cancel
	if component.core != nil {
		component.core.contentHandle.Store(component)
	}
	return nil
}

// Activate publishes the trusted locale catalog reader to Website's durable
// build capability after every component has started and the service context
// is authenticated.
func (component *contentEngineComponent) Activate(context.Context) error {
	if component == nil {
		return errors.New("Content Engine is not initialized")
	}
	if component.engine == nil || component.serviceCtx == nil {
		return nil
	}
	if component.buildLocaleCatalog != nil {
		component.buildLocaleCatalog.setSource(component.serviceCtx, component.engine)
	}
	if component.buildProjection != nil {
		component.buildProjection.setSource(component.serviceCtx, component)
	}
	return nil
}

func (component *contentEngineComponent) Ready(context.Context) error {
	if component == nil || component.engine == nil || component.projections == nil {
		return errors.New("Content Engine is not initialized")
	}
	return nil
}

func (component *contentEngineComponent) Stop(context.Context) error {
	if component == nil {
		return nil
	}
	if component.cancel != nil {
		component.cancel()
	}
	component.engine, component.projections, component.serviceCtx, component.cancel = nil, nil, nil, nil
	return nil
}

func (component *contentEngineComponent) IsReady() bool {
	return component != nil && component.engine != nil && component.projections != nil
}

func (component *contentEngineComponent) Errors() <-chan error {
	return idleFailureStream()
}

// ReadPublishedContent is the narrow trusted runtime method used by Router's
// public projection. It never accepts projections from a request and does not
// require an end-user authorization context.
func (component *contentEngineComponent) ReadPublishedContent(ctx context.Context, pageSnapshotID, locale string) (contentapi.PublishedContent, error) {
	if component == nil || ctx == nil || component.engine == nil || component.projections == nil {
		return contentapi.PublishedContent{}, errors.New("Content Engine is not initialized")
	}
	if err := ctx.Err(); err != nil {
		return contentapi.PublishedContent{}, err
	}
	projections, err := component.projections.GetContentProjectionsForLocale(ctx, pageSnapshotID, locale)
	if err != nil {
		return contentapi.PublishedContent{}, err
	}
	if !projections.VerifyScope(pageSnapshotID) || projections.Locale != locale {
		return contentapi.PublishedContent{}, errors.New("invalid Content projection scope")
	}
	return component.engine.ReadPublishedContent(ctx, projections)
}

// ResolveContentBuildProjection pins the published Content revision and the
// locale-specific editorial selection for one page snapshot. It uses only
// trusted projections derived from the Website-owned snapshot.
func (component *contentEngineComponent) ResolveContentBuildProjection(ctx context.Context, pageSnapshotID, locale string) (websiteapp.ContentBuildProjection, error) {
	if component == nil || ctx == nil || component.engine == nil || component.projections == nil || pageSnapshotID == "" || locale == "" {
		return websiteapp.ContentBuildProjection{}, contentapi.ErrUnavailable
	}
	projections, err := component.projections.GetContentProjectionsForLocale(ctx, pageSnapshotID, locale)
	if err != nil || !projections.VerifyScope(pageSnapshotID) || projections.Locale != locale {
		return websiteapp.ContentBuildProjection{}, contentapi.ErrUnavailable
	}
	workspace, err := component.engine.GetWorkspace(ctx, projections)
	if err != nil || workspace.PublishedRevision < 0 || workspace.Selections == nil {
		return websiteapp.ContentBuildProjection{}, contentapi.ErrUnavailable
	}
	encoded, err := json.Marshal(workspace.Selections)
	if err != nil {
		return websiteapp.ContentBuildProjection{}, contentapi.ErrUnavailable
	}
	return websiteapp.ContentBuildProjection{
		PublicationRevision: workspace.PublishedRevision,
		SelectionDigest:     prerendercontract.HexDigest(encoded),
	}, nil
}

var (
	_ platform.ManagedComponent     = (*contentEngineComponent)(nil)
	_ platform.ActivatableComponent = (*contentEngineComponent)(nil)
)
