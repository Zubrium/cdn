package composition

import (
	"context"
	"errors"
	"sync"

	platform "github.com/Zubrium/cdn/core/presentation"
	contentapi "github.com/Zubrium/cdn/engines/content/presentation"
	websiteworkflow "github.com/Zubrium/cdn/engines/website/workflow"
	"github.com/google/uuid"
)

var (
	errWebsiteBuildUnavailable  = errors.New("Website build capability is not initialized")
	errLocaleCatalogUnavailable = errors.New("Website locale catalog is not initialized")
)

type workflowStarter interface {
	Start(context.Context, platform.WorkflowStartRequest) (platform.WorkflowReceipt, error)
}

type workflowReader interface {
	Get(context.Context, uuid.UUID) (platform.Workflow, error)
}

type taskResultReader interface {
	GetTaskResult(context.Context, uuid.UUID, uuid.UUID) (platform.TaskResult, error)
}

type workflowDetailReader interface {
	Get(context.Context, uuid.UUID, platform.WorkflowDetailFilter) (platform.WorkflowDetail, error)
}

// websiteBuildWorkflowClient is the narrow, owner-scoped adapter supplied to
// Website application. Task IDs are resolved from the workflow task graph
// through the trusted composition root; results themselves are read only by
// Core's owner-scoped GetTaskResult capability.
type websiteBuildWorkflowClient struct {
	start    workflowStarter
	read     workflowReader
	results  taskResultReader
	details  workflowDetailReader
	maxPages int
}

func newWebsiteBuildWorkflowClient(start workflowStarter, read workflowReader, results taskResultReader, details workflowDetailReader, maxWorkflowTasks int) *websiteBuildWorkflowClient {
	pages := (maxWorkflowTasks + workflowTaskPageSize - 1) / workflowTaskPageSize
	if pages < 1 {
		pages = 1
	}
	return &websiteBuildWorkflowClient{start: start, read: read, results: results, details: details, maxPages: pages}
}

const workflowTaskPageSize = 1000

func (client *websiteBuildWorkflowClient) Start(ctx context.Context, request platform.WorkflowStartRequest) (platform.WorkflowReceipt, error) {
	if client == nil || client.start == nil {
		return platform.WorkflowReceipt{}, errWebsiteBuildUnavailable
	}
	return client.start.Start(ctx, request)
}

func (client *websiteBuildWorkflowClient) Get(ctx context.Context, workflowID uuid.UUID) (platform.Workflow, error) {
	if client == nil || client.read == nil {
		return platform.Workflow{}, errWebsiteBuildUnavailable
	}
	return client.read.Get(ctx, workflowID)
}

func (client *websiteBuildWorkflowClient) GetTaskResult(ctx context.Context, workflowID, taskID uuid.UUID) (platform.TaskResult, error) {
	if client == nil || client.results == nil {
		return platform.TaskResult{}, errWebsiteBuildUnavailable
	}
	return client.results.GetTaskResult(ctx, workflowID, taskID)
}

func (client *websiteBuildWorkflowClient) ResolveTaskID(ctx context.Context, workflowID uuid.UUID, emissionKey string) (uuid.UUID, error) {
	if client == nil || client.details == nil || ctx == nil || workflowID == uuid.Nil || emissionKey == "" {
		return uuid.Nil, platform.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return uuid.Nil, err
	}
	var before *platform.TaskCursor
	for page := 0; page < client.maxPages; page++ {
		detail, err := client.details.Get(ctx, workflowID, platform.WorkflowDetailFilter{Limit: workflowTaskPageSize, Before: before})
		if err != nil {
			return uuid.Nil, err
		}
		if detail.Workflow.ID != uuid.Nil && detail.Workflow.ID != workflowID ||
			detail.Workflow.OwnerClassID != "" && detail.Workflow.OwnerClassID != websiteworkflow.ComponentID {
			return uuid.Nil, platform.ErrTaskMissing
		}
		for _, task := range detail.TaskPage.Items {
			if task.EmissionKey == emissionKey && task.ID != uuid.Nil {
				return task.ID, nil
			}
		}
		if detail.TaskPage.Next == nil {
			break
		}
		cursor := *detail.TaskPage.Next
		if cursor.ID == uuid.Nil || cursor.CreatedAt.IsZero() {
			return uuid.Nil, platform.ErrInvalid
		}
		before = &cursor
	}
	return uuid.Nil, platform.ErrTaskMissing
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
		return 0, platform.ErrInvalid
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
