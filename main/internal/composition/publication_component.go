package composition

import (
	"context"
	"errors"
	"fmt"

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
	data, err := component.core.registerModuleData(publication.ModuleDataDescriptor())
	if err != nil {
		return err
	}
	session, _, err := component.core.runtime.Identity().Authenticate(ctx, component.config.RouterAPI.ServiceCredential)
	if err != nil {
		return fmt.Errorf("authenticate publication file service: %w", err)
	}
	component.core.setCoreFiles("main.publication", coreSessionFileAdapter{files: component.core.runtime.Files(), session: session})
	starter := publicationWorkflowStarter{dispatcher: bus.NewModuleDispatcher(component.dispatcher, "main.publication")}
	service, err := publication.NewService(publication.Config{
		BranchID:    component.config.RouterAPI.BranchID,
		Store:       data,
		Targets:     component.config.Publication.Targets,
		Starter:     starter,
		MaxAttempts: component.config.Coordinator.MaxAttempts,
	})
	if err != nil {
		return err
	}
	component.service = service
	setPublicationWorkflowService(service)
	setPublicationHandlerCore(component.core)
	setPublicationHandlerRouter(component.router)
	return nil
}

func (component *publicationComponent) publish(ctx context.Context, request contracts.PublicationPublishRequest) (contracts.PublicationPublishResponse, error) {
	if component == nil || component.service == nil {
		return contracts.PublicationPublishResponse{}, errors.New("Publication fan-out is unavailable")
	}
	result, err := component.service.Publish(ctx, publication.Trigger{
		Locale: request.Locale, ContentPublicationRevision: request.ContentPublicationRevision,
		SystemModelRevision: request.SystemModelRevision, IdempotencyKey: request.IdempotencyKey,
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
