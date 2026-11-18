package postgres

import (
	"context"
	"time"

	"github.com/Zubrium/cdn/core/domain/maintenance"
	"github.com/jackc/pgx/v5/pgxpool"
)

type MaintenanceRepository struct {
	Pool  *pgxpool.Pool
	Store ObjectWriter
}

func (r MaintenanceRepository) RecoverPending(ctx context.Context, before time.Time, limit int) error {
	return RecoverPending(ctx, r.Pool, r.Store, before, limit)
}

func (r MaintenanceRepository) PruneWorkflows(ctx context.Context, limit int) (int64, error) {
	return PruneCoordinatorWorkflows(ctx, r.Pool, limit)
}

func (r MaintenanceRepository) PruneSessions(ctx context.Context, before time.Time, limit int) (int64, error) {
	return PruneSessions(ctx, r.Pool, before, limit)
}

var _ maintenance.Repository = MaintenanceRepository{}
