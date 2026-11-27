package content

import (
	"context"

	"github.com/Zubrium/cdn/pkg/contracts"
)

// ListPublicationIntents serves the trusted composition operation.
func (engine *Engine) ListPublicationIntents(ctx context.Context, request contracts.ContentPublicationIntentList) (contracts.ContentPublicationIntentPage, error) {
	items, next, err := engine.repository.ListPendingPublicationIntents(ctx, request.After, request.Limit)
	if err != nil {
		return contracts.ContentPublicationIntentPage{}, mapError(err)
	}
	page := contracts.ContentPublicationIntentPage{Items: make([]contracts.ContentPublicationIntentItem, 0, len(items)), NextCursor: next}
	for _, item := range items {
		page.Items = append(page.Items, contracts.ContentPublicationIntentItem{
			Publication: contracts.PublicationPublishRequest{
				Locale: item.Intent.Locale, ContentPublicationRevision: item.Intent.ContentRevision,
				SystemModelRevision: item.Intent.SystemModelRevision, IdempotencyKey: item.Intent.WorkflowKey,
			},
			StoreRevision: item.StoreRevision,
		})
	}
	return page, nil
}

func (engine *Engine) AcknowledgePublicationIntent(ctx context.Context, request contracts.ContentPublicationIntentAck) (struct{}, error) {
	return struct{}{}, mapError(engine.repository.MarkPublicationIntentDelivered(ctx, request.Locale, request.ContentRevision, request.StoreRevision, request.RunID))
}
