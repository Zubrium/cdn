package composition

import (
	"context"

	contentapi "github.com/Zubrium/cdn/engines/content/presentation"
	"github.com/Zubrium/cdn/pkg/contracts"
)

// publicationClient is the composition-side caller of the trusted publication
// operation. The request carries only server-owned revision values; route,
// page, snapshot and artifact identity remain in the publication registry.
type publicationClient struct{ dispatcher contracts.Dispatcher }

func (client publicationClient) Publish(ctx context.Context, request contracts.PublicationPublishRequest) error {
	if client.dispatcher == nil {
		return contracts.ErrDispatcherUnavailable
	}
	_, err := contracts.Call(ctx, client.dispatcher, contracts.PublicationPublishOperation(), request)
	return err
}

// contentPublicationAPI forwards Content's API unchanged and starts the durable
// publication fan-out only after a Content locale publication was committed.
// The wrapper lives in the composition root: Content never learns about Router
// targets, and the Admin HTTP ingress cannot select a fan-out target.
type contentPublicationAPI struct {
	contentapi.API
	client publicationClient
}

func (api contentPublicationAPI) PublishWorkspace(ctx context.Context, projection contentapi.Projections,
	request contentapi.PublishWorkspaceRequest) (contentapi.Workspace, error) {
	workspace, err := api.API.PublishWorkspace(ctx, projection, request)
	if err != nil || api.client.dispatcher == nil || workspace.PublishedRevision < 1 {
		return workspace, err
	}
	if err := api.client.Publish(ctx, contracts.PublicationPublishRequest{
		Locale:                     workspace.Locale,
		ContentPublicationRevision: workspace.PublishedRevision,
		SystemModelRevision:        workspace.ModelRevision,
		IdempotencyKey:             request.IdempotencyKey,
	}); err != nil {
		return workspace, err
	}
	return workspace, nil
}
