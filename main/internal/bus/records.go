package bus

import (
	"context"

	"github.com/Zubrium/cdn/pkg/contracts"
)

type RecordDataStoreResolver func(context.Context, string) (contracts.RecordDataStore, error)

// RegisterRecordDataStore registers Core record operations once. Each request
// selects only a capability already issued by the composition root for the
// module identity attached by NewModuleDispatcher.
func RegisterRecordDataStore(dispatcher *Dispatcher, resolve RecordDataStoreResolver) error {
	if dispatcher == nil || resolve == nil {
		return contracts.ErrInvalidOperation
	}
	getStore := func(ctx context.Context) (contracts.RecordDataStore, error) {
		moduleID := ModuleIDFromContext(ctx)
		if moduleID == "" {
			return nil, ErrInvalidModuleIdentity
		}
		store, err := resolve(ctx, moduleID)
		if err != nil {
			return nil, err
		}
		if store == nil {
			return nil, contracts.ErrDispatcherUnavailable
		}
		return store, nil
	}
	if err := Register(dispatcher, contracts.RecordGetOperation(), func(ctx context.Context, request contracts.RecordGetRequest) (contracts.RecordGetResponse, error) {
		store, err := getStore(ctx)
		if err != nil {
			return contracts.RecordGetResponse{}, err
		}
		record, err := store.Get(ctx, request.Key)
		return contracts.RecordGetResponse{Record: record}, err
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.RecordGetExpiredOperation(), func(ctx context.Context, request contracts.RecordGetRequest) (contracts.RecordGetResponse, error) {
		store, err := getStore(ctx)
		if err != nil {
			return contracts.RecordGetResponse{}, err
		}
		record, err := store.GetExpired(ctx, request.Key)
		return contracts.RecordGetResponse{Record: record}, err
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.RecordPutOperation(), func(ctx context.Context, request contracts.RecordPutRequest) (contracts.RecordGetResponse, error) {
		store, err := getStore(ctx)
		if err != nil {
			return contracts.RecordGetResponse{}, err
		}
		record, err := store.Put(ctx, request.Key, request.Value, request.ExpectedRevision)
		return contracts.RecordGetResponse{Record: record}, err
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.RecordDeleteOperation(), func(ctx context.Context, request contracts.RecordDeleteRequest) (contracts.RecordDeleteResponse, error) {
		store, err := getStore(ctx)
		if err != nil {
			return contracts.RecordDeleteResponse{}, err
		}
		return contracts.RecordDeleteResponse{}, store.Delete(ctx, request.Key, request.ExpectedRevision)
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.RecordBatchOperation(), func(ctx context.Context, request contracts.RecordBatchRequest) (contracts.RecordBatchResponse, error) {
		store, err := getStore(ctx)
		if err != nil {
			return contracts.RecordBatchResponse{}, err
		}
		return contracts.RecordBatchResponse{}, store.Batch(ctx, request.Mutations)
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.RecordListOperation(), func(ctx context.Context, request contracts.RecordListRequest) (contracts.RecordListResponse, error) {
		store, err := getStore(ctx)
		if err != nil {
			return contracts.RecordListResponse{}, err
		}
		keys, records, cursor, err := store.List(ctx, request.Namespace, request.ScopeType, request.ScopeID, request.After, request.Selectors, request.Limit)
		return contracts.RecordListResponse{Keys: keys, Records: records, NextCursor: cursor}, err
	}); err != nil {
		return err
	}
	return Register(dispatcher, contracts.RecordListExpiredOperation(), func(ctx context.Context, request contracts.RecordListExpiredRequest) (contracts.RecordListResponse, error) {
		store, err := getStore(ctx)
		if err != nil {
			return contracts.RecordListResponse{}, err
		}
		keys, records, cursor, err := store.ListExpired(ctx, request.Namespace, request.ScopeType, request.ScopeID, request.After, request.Limit)
		return contracts.RecordListResponse{Keys: keys, Records: records, NextCursor: cursor}, err
	})
}
