package postgres

import (
	"context"

	"github.com/Zubrium/cdn/core/domain/engine_data"
	"github.com/jackc/pgx/v5/pgxpool"
)

// EngineDataRepository implements the shared scoped JSON record port over
// engine_data.records. The caller must obtain a per-owner application
// capability before any operation reaches this adapter.
type EngineDataRepository struct{ Pool *pgxpool.Pool }

func (r EngineDataRepository) Get(ctx context.Context, engineID string, key engine_data.Key) (engine_data.Record, error) {
	return GetModuleData(ctx, r.Pool, engineID, key)
}

func (r EngineDataRepository) GetExpired(ctx context.Context, engineID string, key engine_data.Key) (engine_data.Record, error) {
	return GetExpiredModuleData(ctx, r.Pool, engineID, key)
}

func (r EngineDataRepository) Put(ctx context.Context, engineID string, key engine_data.Key, value engine_data.Value, expected int64, maxPayload, maxIndex int) (engine_data.Record, error) {
	return PutModuleData(ctx, r.Pool, engineID, key, value, expected, maxPayload, maxIndex)
}

func (r EngineDataRepository) Delete(ctx context.Context, engineID string, key engine_data.Key, expected int64) error {
	return DeleteModuleData(ctx, r.Pool, engineID, key, expected)
}

func (r EngineDataRepository) Batch(ctx context.Context, engineID string, mutations []engine_data.Mutation, maxOperations, maxBatchBytes, maxPayload, maxIndex int) error {
	return BatchModuleData(ctx, r.Pool, engineID, mutations, maxOperations, maxBatchBytes, maxPayload, maxIndex)
}

func (r EngineDataRepository) List(ctx context.Context, engineID, namespace, scopeType, scopeID, after string, selectors []engine_data.ContainmentSelector, limit int) ([]engine_data.Key, []engine_data.Record, string, error) {
	return ListModuleData(ctx, r.Pool, engineID, namespace, scopeType, scopeID, after, selectors, limit)
}

func (r EngineDataRepository) ListExpired(ctx context.Context, engineID, namespace, scopeType, scopeID, after string, limit int) ([]engine_data.Key, []engine_data.Record, string, error) {
	return ListExpiredModuleData(ctx, r.Pool, engineID, namespace, scopeType, scopeID, after, limit)
}

var _ engine_data.Repository = EngineDataRepository{}
