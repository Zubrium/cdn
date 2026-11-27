package composition

import (
	"context"
	"errors"
	"time"

	"github.com/Zubrium/cdn/main/internal/bus"
	"github.com/Zubrium/cdn/pkg/contracts"
	"github.com/Zubrium/cdn/pkg/observability"
)

func registerPublicationIntentOperations(dispatcher *bus.Dispatcher, content *contentEngineComponent) error {
	if err := bus.Register(dispatcher, contracts.ListContentPublicationIntentsOperation(), func(ctx context.Context, request contracts.ContentPublicationIntentList) (contracts.ContentPublicationIntentPage, error) {
		if bus.ModuleIDFromContext(ctx) != "main.publication" {
			return contracts.ContentPublicationIntentPage{}, bus.ErrInvalidModuleIdentity
		}
		if content.engine == nil {
			return contracts.ContentPublicationIntentPage{}, contracts.ErrAdminContentUnavailable
		}
		return content.engine.ListPublicationIntents(ctx, request)
	}); err != nil {
		return err
	}
	return bus.Register(dispatcher, contracts.AcknowledgeContentPublicationIntentOperation(), func(ctx context.Context, request contracts.ContentPublicationIntentAck) (struct{}, error) {
		if bus.ModuleIDFromContext(ctx) != "main.publication" {
			return struct{}{}, bus.ErrInvalidModuleIdentity
		}
		if content.engine == nil {
			return struct{}{}, contracts.ErrAdminContentUnavailable
		}
		return content.engine.AcknowledgePublicationIntent(ctx, request)
	})
}

func deliverPublicationPage(ctx context.Context, dispatcher contracts.Dispatcher, after string, limit int) (string, error) {
	page, err := contracts.Call(ctx, dispatcher, contracts.ListContentPublicationIntentsOperation(), contracts.ContentPublicationIntentList{After: after, Limit: limit})
	if err != nil {
		return after, err
	}
	var failures error
	for _, item := range page.Items {
		if err := ctx.Err(); err != nil {
			return page.NextCursor, errors.Join(failures, err)
		}
		result, err := contracts.Call(ctx, dispatcher, contracts.PublicationPublishOperation(), item.Publication)
		if err != nil {
			failures = errors.Join(failures, err)
			continue
		}
		if !contracts.ValidWorkflowID(result.RunID) || !result.WorkflowStarted {
			failures = errors.Join(failures, errors.New("publication workflow start unconfirmed"))
			continue
		}
		_, err = contracts.Call(ctx, dispatcher, contracts.AcknowledgeContentPublicationIntentOperation(), contracts.ContentPublicationIntentAck{
			Locale: item.Publication.Locale, ContentRevision: item.Publication.ContentPublicationRevision,
			StoreRevision: item.StoreRevision, RunID: result.RunID,
		})
		failures = errors.Join(failures, err)
	}
	return page.NextCursor, failures
}

func (component *publicationComponent) ActivateAfterComponents(ctx context.Context) error {
	if !component.config.Publication.Enabled {
		return nil
	}
	settings := component.config.Publication
	limit := component.config.Core.ModuleData.MaxPageSize
	if settings.PollInterval <= 0 || settings.BuildTimeout <= 0 || limit <= 0 {
		return errors.New("publication recovery limits are not configured")
	}
	lifetime, cancel := context.WithCancel(context.WithoutCancel(ctx))
	done := make(chan struct{})
	component.deliveryCancel, component.deliveryDone = cancel, done
	dispatcher := bus.NewModuleDispatcher(component.dispatcher, "main.publication")
	go func() {
		defer close(done)
		ticker := time.NewTicker(settings.PollInterval)
		defer ticker.Stop()
		var cursor string
		for lifetime.Err() == nil {
			pass, finish := context.WithTimeout(lifetime, settings.BuildTimeout)
			next, err := deliverPublicationPage(pass, dispatcher, cursor, limit)
			finish()
			cursor = next
			if err != nil && lifetime.Err() == nil && component.observer != nil {
				component.observer.Log(lifetime, observability.LevelWarn, "Content publication delivery will retry")
			}
			select {
			case <-lifetime.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return nil
}
