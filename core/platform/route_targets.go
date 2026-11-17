package platform

import (
	"context"

	"github.com/Zubrium/cdn/core/internal/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RouterTargetStore is the narrow Core capability used by Router to read and
// explicitly change the active file for a route. It does not expose storage,
// SQL, or publication-generation writes.
type RouterTargetStore struct {
	pool    *pgxpool.Pool
	changes <-chan string
}

func (r *Runtime) RouterTargetStore() (*RouterTargetStore, error) {
	if err := r.valid(); err != nil {
		return nil, err
	}
	if r.Registry == nil {
		return nil, ErrCoordinatorInvalid
	}
	if _, err := r.Registry.Lookup("router"); err != nil {
		return nil, ErrCoordinatorInvalid
	}
	r.moduleDataMu.Lock()
	defer r.moduleDataMu.Unlock()
	if r.routeTargetStoreIssued {
		return nil, ErrCoordinatorInvalid
	}
	r.routeTargetStoreIssued = true
	return &RouterTargetStore{pool: r.database, changes: r.routeTargetChanges}, nil
}

func (s *RouterTargetStore) Create(ctx context.Context, systemID string, routeID, fileID uuid.UUID) (RouteTarget, error) {
	if s == nil {
		return RouteTarget{}, ErrCoordinatorInvalid
	}
	return postgres.CreateCoordinatorRouteTarget(ctx, s.pool, systemID, routeID, fileID)
}

func (s *RouterTargetStore) Get(ctx context.Context, systemID string, routeID uuid.UUID) (RouteTarget, error) {
	if s == nil {
		return RouteTarget{}, ErrCoordinatorInvalid
	}
	return postgres.GetCoordinatorRouteTarget(ctx, s.pool, systemID, routeID)
}

func (s *RouterTargetStore) Replace(ctx context.Context, systemID string, routeID uuid.UUID, expectedRevision int64, fileID uuid.UUID) (RouteTarget, error) {
	if s == nil {
		return RouteTarget{}, ErrCoordinatorInvalid
	}
	return postgres.ReplaceCoordinatorRouteTarget(ctx, s.pool, systemID, routeID, expectedRevision, fileID)
}

// Changes returns route invalidations. An empty wildcard notification means a
// complete reload is required (startup, reconnect, or buffer overflow).
func (s *RouterTargetStore) Changes() <-chan string {
	if s == nil {
		ch := make(chan string)
		close(ch)
		return ch
	}
	return s.changes
}
