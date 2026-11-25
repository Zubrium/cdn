package transport

import (
	"context"
	"sync"

	"github.com/Zubrium/cdn/pkg/observability"
)

// SPEC-concurrency.md §12 fixes both the series and their label vocabulary.
// Every label below is a static, low-cardinality value: no route, operation,
// file, principal or visitor identifier reaches a metric series.
var (
	concurrencyScopes  = []string{"HTTP", "BUS", "STORAGE", "LOADER"}
	concurrencyReasons = []string{"GLOBAL", "PER_ROUTE", "PER_OPERATION", "DB_RESERVE"}
	coalesceCaches     = []string{"core.bytes", "core.metadata", "router.manifests", "router.routes"}
	coalesceResults    = []string{"JOINED", "LEADER", "FAILED"}
	batchResults       = []string{"APPLIED", "OVERFLOW", "CLEARED"}
	workerPools        = []string{"ARCHIVE", "MEDIA", "PRERENDER", "PUBLICATION"}
	workerOutcomes     = []string{"OK", "ERROR"}
)

type concurrencyContractKey struct {
	observer *observability.Observer
	pool     string
}

// concurrencyContractPublisher keeps the set of initialised metric contracts
// scoped to a transport instance. Admin and Public servers must not share
// process-global synchronized state.
type concurrencyContractPublisher struct {
	mu        sync.Mutex
	published map[concurrencyContractKey]struct{}
}

func newConcurrencyContractPublisher() *concurrencyContractPublisher {
	return &concurrencyContractPublisher{published: make(map[concurrencyContractKey]struct{})}
}

// publish makes disabled or not-yet-wired groups visible as zero-valued
// series, as required by SPEC-concurrency.md §11. Live admission paths keep
// updating the same series through the Observer API. Each observer/pool pair
// is initialised once within this server instance.
func (publisher *concurrencyContractPublisher) publish(observer *observability.Observer, pool string) {
	if publisher == nil || observer == nil {
		return
	}
	key := concurrencyContractKey{observer: observer, pool: pool}
	publisher.mu.Lock()
	if _, done := publisher.published[key]; done {
		publisher.mu.Unlock()
		return
	}
	publisher.published[key] = struct{}{}
	publisher.mu.Unlock()

	httpContext := observer.WithIdentity(context.Background(), observability.Identity{
		ModuleKind: observability.ModuleKindPresentation, ModuleID: pool, ComponentID: "http-server",
	})
	observer.SetGaugeExact(httpContext, "zubrium_concurrency_in_flight", 0,
		observability.String("scope", "HTTP"))
	for _, reason := range concurrencyReasons {
		observer.IncCounterExact(httpContext, "zubrium_concurrency_rejected_total", 0,
			observability.String("scope", "HTTP"), observability.String("reason", reason))
	}

	busContext := observer.WithIdentity(context.Background(), observability.Identity{
		ModuleKind: observability.ModuleKindBus, ModuleID: "bus", ComponentID: "dispatcher",
	})
	publishScope(observer, busContext, "BUS")

	storageContext := observer.WithIdentity(context.Background(), observability.Identity{
		ModuleKind: observability.ModuleKindCore, ModuleID: "core", ComponentID: "storage",
	})
	publishScope(observer, storageContext, "STORAGE")

	loaderContext := observer.WithIdentity(context.Background(), observability.Identity{
		ModuleKind: observability.ModuleKindEngine, ModuleID: "loader", ComponentID: "files",
	})
	publishScope(observer, loaderContext, "LOADER")

	cacheContext := observer.WithIdentity(context.Background(), observability.Identity{
		ModuleKind: observability.ModuleKindCore, ModuleID: "core", ComponentID: "cache",
	})
	for _, cache := range coalesceCaches {
		for _, result := range coalesceResults {
			observer.IncCounterExact(cacheContext, "zubrium_cache_coalesced_total", 0,
				observability.String("cache", cache), observability.String("result", result))
		}
	}
	for _, result := range batchResults {
		observer.IncCounterExact(cacheContext, "zubrium_cache_invalidation_batch_total", 0,
			observability.String("result", result))
	}

	workerContext := observer.WithIdentity(context.Background(), observability.Identity{
		ModuleKind: observability.ModuleKindWorker, ModuleID: "worker-pools", ComponentID: "task-executor",
	})
	for _, poolName := range workerPools {
		observer.SetGaugeExact(workerContext, "zubrium_worker_pool_active", 0,
			observability.String("pool", poolName))
		for _, outcome := range workerOutcomes {
			observer.IncCounterExact(workerContext, "zubrium_worker_pool_tasks_total", 0,
				observability.String("pool", poolName), observability.String("outcome", outcome))
		}
	}
	observer.SetGaugeExact(context.Background(), "zubrium_db_pool_usage", 0)
}

func publishScope(observer *observability.Observer, ctx context.Context, scope string) {
	observer.SetGaugeExact(ctx, "zubrium_concurrency_in_flight", 0, observability.String("scope", scope))
	for _, reason := range concurrencyReasons {
		observer.IncCounterExact(ctx, "zubrium_concurrency_rejected_total", 0,
			observability.String("scope", scope), observability.String("reason", reason))
	}
}
