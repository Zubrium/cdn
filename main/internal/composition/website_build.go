package composition

import (
	"context"
	"errors"
	"sync"

	contentapi "github.com/Zubrium/cdn/engines/content/presentation"
	"github.com/Zubrium/cdn/pkg/contracts"
	"github.com/google/uuid"
)

var (
	errWebsiteBuildUnavailable  = errors.New("Website build capability is not initialized")
	errLocaleCatalogUnavailable = errors.New("Website locale catalog is not initialized")
)

// websiteBuildWorkflowClient is the narrow, owner-scoped adapter supplied to
// Website application over neutral DataBus workflow DTOs. The dispatcher binds
// the module identity; no request can choose an operation, owner or task.
type websiteBuildWorkflowClient struct {
	dispatcher contracts.Dispatcher
}

func newWebsiteBuildWorkflowClient(dispatcher contracts.Dispatcher) *websiteBuildWorkflowClient {
	return &websiteBuildWorkflowClient{dispatcher: dispatcher}
}

func (client *websiteBuildWorkflowClient) Start(ctx context.Context, request contracts.WorkflowStartInput) (contracts.WorkflowStartReceipt, error) {
	if client == nil || client.dispatcher == nil {
		return contracts.WorkflowStartReceipt{}, errWebsiteBuildUnavailable
	}
	return contracts.CallWorkflowStart(ctx, client.dispatcher, request)
}

func (client *websiteBuildWorkflowClient) Get(ctx context.Context, workflowID uuid.UUID) (contracts.WorkflowState, error) {
	if client == nil || client.dispatcher == nil {
		return contracts.WorkflowState{}, errWebsiteBuildUnavailable
	}
	return contracts.CallWorkflowState(ctx, client.dispatcher, workflowID)
}

func (client *websiteBuildWorkflowClient) GetTaskResult(ctx context.Context, workflowID, taskID uuid.UUID) (contracts.WorkflowTaskResult, error) {
	if client == nil || client.dispatcher == nil {
		return contracts.WorkflowTaskResult{}, errWebsiteBuildUnavailable
	}
	return contracts.CallWorkflowTaskResult(ctx, client.dispatcher, workflowID, taskID)
}

func (client *websiteBuildWorkflowClient) ResolveTaskID(ctx context.Context, workflowID uuid.UUID, emissionKey string) (uuid.UUID, error) {
	if client == nil || client.dispatcher == nil {
		return uuid.Nil, errWebsiteBuildUnavailable
	}
	return contracts.CallWorkflowTaskIDResolve(ctx, client.dispatcher, workflowID, emissionKey)
}

type websiteLocaleCatalogSource interface {
	GetLocaleConfig(context.Context) (contentapi.LocaleConfig, error)
}

// websiteLocaleCatalog resolves a locale revision through the trusted Content
// service context. The caller's authorization context is never forwarded: a
// Website build is authorized separately before this capability is used.
type websiteLocaleCatalog struct {
	mu     sync.RWMutex
	ctx    context.Context
	source websiteLocaleCatalogSource
}

func newWebsiteLocaleCatalog() *websiteLocaleCatalog {
	return &websiteLocaleCatalog{}
}

func (catalog *websiteLocaleCatalog) setSource(ctx context.Context, source websiteLocaleCatalogSource) {
	if catalog == nil || ctx == nil || source == nil {
		return
	}
	catalog.mu.Lock()
	defer catalog.mu.Unlock()
	catalog.ctx, catalog.source = ctx, source
}

func (catalog *websiteLocaleCatalog) LocaleCatalogRevision(ctx context.Context, locale string) (int64, error) {
	if catalog == nil || ctx == nil || locale == "" {
		return 0, contracts.ErrInvalidOperation
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	catalog.mu.RLock()
	source, sourceCtx := catalog.source, catalog.ctx
	catalog.mu.RUnlock()
	if source == nil || sourceCtx == nil {
		return 0, errLocaleCatalogUnavailable
	}
	config, err := source.GetLocaleConfig(sourceCtx)
	if err != nil || config.Revision < 1 {
		return 0, errLocaleCatalogUnavailable
	}
	for _, configured := range config.Locales {
		if configured == locale {
			return config.Revision, nil
		}
	}
	return 0, errLocaleCatalogUnavailable
}
