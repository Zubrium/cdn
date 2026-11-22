package composition

import (
	"context"
	"errors"

	"github.com/Zubrium/cdn/main/internal/bus"
	appconfig "github.com/Zubrium/cdn/main/internal/config"
	"github.com/Zubrium/cdn/main/internal/publication"
	"github.com/Zubrium/cdn/pkg/contracts"
)

// publicationComponent owns the server-side rebuild fan-out. It is assembled
// only from startup configuration and registered engine capabilities; no HTTP
// request can select its routes, pages, locales or Router operations.
type publicationComponent struct {
	core       *coreComponent
	config     appconfig.Resolved
	website    *websiteEngineComponent
	router     *routerEngineComponent
	dispatcher *bus.Dispatcher
	service    *publication.Service
}

func (component *publicationComponent) Start(ctx context.Context) error {
	if component == nil || !component.config.Publication.Enabled {
		return nil
	}
	if ctx == nil || component.core == nil || component.core.runtime == nil || component.website == nil ||
		component.website.service == nil || component.website.localeCatalog == nil || component.website.inputResolver == nil ||
		component.router == nil || component.router.api == nil || component.dispatcher == nil ||
		component.config.Publication.WebsiteCredential == "" {
		return errors.New("Publication dependencies are not initialized")
	}
	data, err := component.core.runtime.RegisterDataModule(publication.ModuleDataDescriptor())
	if err != nil {
		return err
	}
	service, err := publication.NewService(publication.Config{
		BranchID:          component.config.RouterAPI.BranchID,
		Store:             data,
		Targets:           component.config.Publication.Targets,
		Website:           component.website.service,
		WebsiteCredential: component.config.Publication.WebsiteCredential,
		Router:            component.router.api,
		RouterCredential:  component.config.Publication.RouterCredential,
		FileIdentity:      component.core.runtime.EngineIdentity(),
		Files:             component.core.runtime.EngineFiles(),
		FileCredential:    component.config.Publication.WebsiteCredential,
		LocaleCatalog:     component.website.localeCatalog,
		InputResolver:     component.website.inputResolver,
		BuildTimeout:      component.config.Publication.BuildTimeout,
		PollInterval:      component.config.Publication.PollInterval,
	})
	if err != nil {
		return err
	}
	if err := bus.Register(component.dispatcher, contracts.PublicationPublishOperation(), func(ctx context.Context, request contracts.PublicationPublishRequest) (contracts.PublicationPublishResponse, error) {
		result, err := service.Publish(ctx, publication.Trigger{
			Locale:                     request.Locale,
			ContentPublicationRevision: request.ContentPublicationRevision,
			SystemModelRevision:        request.SystemModelRevision,
			IdempotencyKey:             request.IdempotencyKey,
		})
		if err != nil {
			return contracts.PublicationPublishResponse{}, err
		}
		response := contracts.PublicationPublishResponse{RunID: result.RunID, State: result.State, Targets: make([]contracts.PublicationTargetResult, 0, len(result.Targets))}
		for _, target := range result.Targets {
			response.Targets = append(response.Targets, contracts.PublicationTargetResult{
				RouteID: target.RouteID, Locale: target.Locale, State: target.State,
				BuildID: target.BuildID, ArtifactManifestFileID: target.ArtifactManifestFileID, ErrorCode: target.ErrorCode,
			})
		}
		return response, nil
	}); err != nil {
		return err
	}
	component.service = service
	return nil
}

func (component *publicationComponent) Ready(context.Context) error {
	if component == nil || !component.config.Publication.Enabled {
		return nil
	}
	if component.service == nil {
		return errors.New("Publication fan-out is not initialized")
	}
	return nil
}

func (component *publicationComponent) Stop(context.Context) error {
	if component != nil {
		component.service = nil
	}
	return nil
}

func (component *publicationComponent) IsReady() bool {
	return component != nil && (!component.config.Publication.Enabled || component.service != nil)
}

func (component *publicationComponent) Errors() <-chan error { return idleFailureStream() }
