package composition

import (
	"context"
	"encoding/json"
	"errors"

	platform "github.com/Zubrium/cdn/core/presentation"
	contentengine "github.com/Zubrium/cdn/engines/content"
	contentapi "github.com/Zubrium/cdn/engines/content/presentation"
	"github.com/Zubrium/cdn/main/internal/bus"
	appconfig "github.com/Zubrium/cdn/main/internal/config"
	"github.com/Zubrium/cdn/main/internal/contentbridge"
	"github.com/Zubrium/cdn/pkg/contracts"
	prerendercontract "github.com/Zubrium/cdn/pkg/contracts/prerender"
)

type contentEngineComponent struct {
	core        *coreComponent
	config      appconfig.Resolved
	engine      *contentengine.Engine
	projections *contentProjectionClient
	serviceCtx  context.Context
	cancel      context.CancelFunc
}

func newContentEngineComponent(core *coreComponent, config appconfig.Resolved) *contentEngineComponent {
	return &contentEngineComponent{core: core, config: config}
}

func (component *contentEngineComponent) Start(ctx context.Context) error {
	if component == nil || ctx == nil || component.core == nil || component.core.runtime == nil || component.core.dispatcher == nil {
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
	moduleDispatcher := bus.NewModuleDispatcher(component.core.dispatcher, "engine.content")
	coreAccess, err := contracts.NewCoreAccessClient(moduleDispatcher)
	if err != nil {
		return errors.New("Content Core access DataBus client could not be initialized")
	}
	identity := moduleCoreIdentity{access: coreAccess}
	sessions := moduleCoreSessions{access: coreAccess}
	authorization := moduleCoreAuthorization{access: coreAccess}
	files := moduleCoreFileStat{dispatcher: moduleDispatcher}
	_, principal, err := identity.Authenticate(ctx, credential)
	if err != nil {
		return errors.New("Content service credential could not be authenticated")
	}
	if principal.ID == "" || !principal.Active {
		return errors.New("Content service identity is inactive")
	}
	data, err := component.core.registerModuleData(contentengine.RecordDescriptor())
	if err != nil {
		return err
	}
	bridge, err := contentbridge.NewClient(newContentBridgeWorkflowClient(moduleDispatcher))
	if err != nil {
		return err
	}
	provider, err := newContentProjectionClient(bridge, component.config.ContentAPI.Locale)
	if err != nil {
		return err
	}
	engine, err := contentengine.New(contentengine.Dependencies{
		Identity: identity, Sessions: sessions,
		Authorization: authorization, Files: files,
		Data: data, Projections: provider,
	})
	if err != nil {
		return err
	}
	contentCtx, err := engine.Authenticate(ctx, credential)
	if err != nil || contentCtx == nil {
		return errors.New("Content service context could not be established")
	}
	contentCtx, contentCancel := context.WithCancel(contentCtx)
	component.engine, component.projections, component.serviceCtx = engine, provider, contentCtx
	component.cancel = contentCancel
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
func (component *contentEngineComponent) ResolveContentBuildProjection(ctx context.Context, pageSnapshotID, locale string) (contracts.ContentBuildProjectionResponse, error) {
	if component == nil || component.projections == nil {
		return contracts.ContentBuildProjectionResponse{}, contentapi.ErrUnavailable
	}
	projections, err := component.projections.GetContentProjectionsForLocale(ctx, pageSnapshotID, locale)
	if err != nil {
		return contracts.ContentBuildProjectionResponse{}, err
	}
	return component.ResolveContentBuildProjectionFromProjections(ctx, projections, locale)
}

// ResolveContentBuildProjectionFromProjections uses an already validated,
// workflow-produced projection. The isolated build worker receives the Website
// projection through the root workflow's declared emission dependency and
// never calls a Website-owned operation itself.
func (component *contentEngineComponent) ResolveContentBuildProjectionFromProjections(ctx context.Context, projections contentapi.Projections, locale string) (contracts.ContentBuildProjectionResponse, error) {
	if component == nil || ctx == nil || component.engine == nil || locale == "" {
		return contracts.ContentBuildProjectionResponse{}, contentapi.ErrUnavailable
	}
	if !projections.VerifyScope(projections.Page.PageSnapshotID) || projections.Locale != locale {
		return contracts.ContentBuildProjectionResponse{}, contentapi.ErrUnavailable
	}
	workspace, err := component.engine.GetWorkspace(ctx, projections)
	if err != nil || workspace.PublishedRevision < 0 || workspace.Selections == nil {
		return contracts.ContentBuildProjectionResponse{}, contentapi.ErrUnavailable
	}
	encoded, err := json.Marshal(workspace.Selections)
	if err != nil {
		return contracts.ContentBuildProjectionResponse{}, contentapi.ErrUnavailable
	}
	return contracts.ContentBuildProjectionResponse{
		PublicationRevision: workspace.PublishedRevision,
		SelectionDigest:     prerendercontract.HexDigest(encoded),
	}, nil
}

var (
	_ platform.ManagedComponent     = (*contentEngineComponent)(nil)
	_ platform.ActivatableComponent = (*contentEngineComponent)(nil)
)
