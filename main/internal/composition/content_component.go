package composition

import (
	"context"
	"errors"

	platform "github.com/Zubrium/cdn/core/presentation"
	contentengine "github.com/Zubrium/cdn/engines/content"
	contentapi "github.com/Zubrium/cdn/engines/content/presentation"
	websiteapi "github.com/Zubrium/cdn/engines/website/presentation"
	appconfig "github.com/Zubrium/cdn/main/internal/config"
)

type contentEngineComponent struct {
	core        *coreComponent
	config      appconfig.Resolved
	website     websiteapi.API
	engine      *contentengine.Engine
	projections *contentProjectionProvider
	serviceCtx  context.Context
	cancel      context.CancelFunc
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
	closed := make(chan error)
	close(closed)
	return closed
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

var _ platform.ManagedComponent = (*contentEngineComponent)(nil)
