// Package repository adapts Router route records to Core's module-scoped
// record capability. It has no database or storage access of its own.
package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"time"

	core "github.com/Zubrium/cdn/core/presentation"
	"github.com/Zubrium/cdn/engines/router/features/routes/model"
)

const (
	routeNamespace = "router/routes"
	dataVersion    = 1
)

var (
	ErrNotFound    = errors.New("Router route not found")
	ErrConflict    = errors.New("Router route revision or key conflict")
	ErrUnavailable = errors.New("Router route storage unavailable")
	ErrInvalid     = errors.New("invalid Router route storage data")
)

type ModuleDataStore interface {
	Get(context.Context, core.ModuleDataKey) (core.ModuleDataRecord, error)
	Put(context.Context, core.ModuleDataKey, core.ModuleDataValue, int64) (core.ModuleDataRecord, error)
	Delete(context.Context, core.ModuleDataKey, int64) error
	Batch(context.Context, []core.ModuleDataMutation) error
	List(context.Context, string, string, string, string, []core.ModuleDataSelector, int) ([]core.ModuleDataKey, []core.ModuleDataRecord, string, error)
	MaxPageSize() int
}

type Repository interface {
	Create(context.Context, model.Route, string, string) (model.Route, error)
	GetByID(context.Context, string, string) (model.Route, error)
	List(context.Context, string, string, string, int) ([]model.Route, string, error)
	FindMatch(context.Context, string, string, string) (model.Route, error)
	ReplaceTarget(context.Context, string, string, model.Target, int64, time.Time) (model.Route, error)
	Delete(context.Context, string, string, int64) error
}

type ModuleDataRepository struct {
	data ModuleDataStore
}

func NewModuleDataRepository(data ModuleDataStore) (*ModuleDataRepository, error) {
	if isNil(data) {
		return nil, ErrUnavailable
	}
	return &ModuleDataRepository{data: data}, nil
}

type routePayload struct {
	Kind  string      `json:"kind"`
	Route model.Route `json:"route"`
}

type idempotencyPayload struct {
	Kind        string `json:"kind"`
	Fingerprint string `json:"fingerprint"`
	RouteKey    string `json:"route_key"`
	RouteID     string `json:"route_id"`
}

func (repository *ModuleDataRepository) Create(ctx context.Context, route model.Route, idempotencyKey, fingerprint string) (model.Route, error) {
	if repository == nil || repository.data == nil || ctx == nil || route.ID == "" || route.BranchID == "" || idempotencyKey == "" || fingerprint == "" {
		return model.Route{}, ErrInvalid
	}
	if route.Revision != 1 || route.ValidateForStorage() != nil {
		return model.Route{}, ErrInvalid
	}
	routeKey := routeRecordKey(route.Host, route.Path)
	idemKey := idempotencyRecordKey(idempotencyKey)
	if prior, err := repository.readIdempotency(ctx, route.BranchID, idemKey); err == nil {
		if prior.Fingerprint != fingerprint {
			return model.Route{}, ErrConflict
		}
		return repository.GetByID(ctx, route.BranchID, prior.RouteID)
	} else if !errors.Is(err, ErrNotFound) {
		return model.Route{}, err
	}

	idemEncoded, err := json.Marshal(idempotencyPayload{Kind: "idempotency", Fingerprint: fingerprint, RouteKey: routeKey, RouteID: route.ID})
	if err != nil {
		return model.Route{}, ErrInvalid
	}
	routeValue, err := encodeRoute(route)
	if err != nil {
		return model.Route{}, err
	}
	idemIndex, _ := json.Marshal(map[string]any{"record_type": "idempotency"})
	mutations := []core.ModuleDataMutation{
		{Key: dataKey(route.BranchID, routeKey), ExpectedRevision: 0, Value: routeValue},
		{Key: dataKey(route.BranchID, idemKey), ExpectedRevision: 0, Value: &core.ModuleDataValue{Payload: idemEncoded, PayloadVersion: dataVersion, IndexData: idemIndex}},
	}
	if err := repository.data.Batch(ctx, mutations); err != nil {
		if !errors.Is(err, core.ErrModuleDataConflict) {
			return model.Route{}, mapDataError(err)
		}
		if prior, readErr := repository.readIdempotency(ctx, route.BranchID, idemKey); readErr == nil {
			if prior.Fingerprint != fingerprint {
				return model.Route{}, ErrConflict
			}
			return repository.GetByID(ctx, route.BranchID, prior.RouteID)
		} else if !errors.Is(readErr, ErrNotFound) {
			return model.Route{}, readErr
		}
		return model.Route{}, ErrConflict
	}
	return route, nil
}

func (repository *ModuleDataRepository) GetByID(ctx context.Context, branchID, id string) (model.Route, error) {
	if repository == nil || repository.data == nil || ctx == nil || branchID == "" || id == "" {
		return model.Route{}, ErrInvalid
	}
	keys, records, _, err := repository.data.List(ctx, routeNamespace, "branch", branchID, "", []core.ModuleDataSelector{
		{Field: "record_type", Operator: core.SelectorEqual, Values: []any{"route"}},
		{Field: "route_id", Operator: core.SelectorEqual, Values: []any{id}},
	}, 2)
	if err != nil {
		return model.Route{}, mapDataError(err)
	}
	if len(keys) == 0 || len(records) == 0 {
		return model.Route{}, ErrNotFound
	}
	if len(keys) != 1 || len(records) != 1 {
		return model.Route{}, ErrInvalid
	}
	route, err := decodeRoute(records[0])
	if err != nil || route.ID != id || route.BranchID != branchID {
		return model.Route{}, ErrInvalid
	}
	return route, nil
}

func (repository *ModuleDataRepository) List(ctx context.Context, branchID, host, cursor string, limit int) ([]model.Route, string, error) {
	if repository == nil || repository.data == nil || ctx == nil || branchID == "" || limit < 1 || len(cursor) > 256 {
		return nil, "", ErrInvalid
	}
	selectors := []core.ModuleDataSelector{{Field: "record_type", Operator: core.SelectorEqual, Values: []any{"route"}}}
	if host != "" {
		selectors = append(selectors, core.ModuleDataSelector{Field: "host", Operator: core.SelectorEqual, Values: []any{host}})
	}
	keys, records, next, err := repository.data.List(ctx, routeNamespace, "branch", branchID, cursor, selectors, limit)
	if err != nil {
		return nil, "", mapDataError(err)
	}
	if len(keys) != len(records) {
		return nil, "", ErrInvalid
	}
	items := make([]model.Route, 0, len(records))
	for _, record := range records {
		route, err := decodeRoute(record)
		if err != nil || route.BranchID != branchID || (host != "" && route.Host != host) {
			return nil, "", ErrInvalid
		}
		items = append(items, route)
	}
	return items, next, nil
}

func (repository *ModuleDataRepository) FindMatch(ctx context.Context, branchID, host, path string) (model.Route, error) {
	if repository == nil || repository.data == nil || ctx == nil || branchID == "" || host == "" || path == "" {
		return model.Route{}, ErrInvalid
	}
	prefixes, err := model.PathPrefixes(path)
	if err != nil {
		return model.Route{}, ErrInvalid
	}
	pageSize := repository.data.MaxPageSize()
	if pageSize < 1 {
		return model.Route{}, ErrUnavailable
	}
	pathValues := make([]any, len(prefixes))
	for index, prefix := range prefixes {
		pathValues[index] = prefix
	}
	selectors := []core.ModuleDataSelector{
		{Field: "record_type", Operator: core.SelectorEqual, Values: []any{"route"}},
		{Field: "host", Operator: core.SelectorEqual, Values: []any{host}},
		{Field: "path", Operator: core.SelectorIn, Values: pathValues},
	}
	cursor := ""
	var best model.Route
	for {
		keys, records, next, err := repository.data.List(ctx, routeNamespace, "branch", branchID, cursor, selectors, pageSize)
		if err != nil {
			return model.Route{}, mapDataError(err)
		}
		if len(keys) != len(records) {
			return model.Route{}, ErrInvalid
		}
		for _, record := range records {
			route, err := decodeRoute(record)
			if err != nil || route.BranchID != branchID || route.Host != host || !model.PathMatches(route.Path, path, route.Match) {
				return model.Route{}, ErrInvalid
			}
			if best.ID == "" || route.Match == model.MatchExact ||
				(best.Match != model.MatchExact && len(route.Path) > len(best.Path)) {
				best = route
			}
		}
		if next == "" {
			break
		}
		if next == cursor {
			return model.Route{}, ErrInvalid
		}
		cursor = next
	}
	if best.ID == "" {
		return model.Route{}, ErrNotFound
	}
	return best, nil
}

func (repository *ModuleDataRepository) ReplaceTarget(ctx context.Context, branchID, id string, target model.Target, expectedRevision int64, now time.Time) (model.Route, error) {
	if repository == nil || repository.data == nil || ctx == nil || branchID == "" || id == "" || expectedRevision < 1 || expectedRevision == math.MaxInt64 || now.IsZero() {
		return model.Route{}, ErrInvalid
	}
	if err := target.Validate(); err != nil {
		return model.Route{}, ErrInvalid
	}
	key, current, err := repository.findRecord(ctx, branchID, id)
	if err != nil {
		return model.Route{}, err
	}
	if current.Revision != expectedRevision {
		return model.Route{}, ErrConflict
	}
	if now.Before(current.CreatedAt) {
		return model.Route{}, ErrInvalid
	}
	current.Target = target
	current.Revision = expectedRevision + 1
	current.UpdatedAt = now.UTC()
	value, err := encodeRoute(current)
	if err != nil {
		return model.Route{}, err
	}
	stored, err := repository.data.Put(ctx, key, *value, expectedRevision)
	if err != nil {
		return model.Route{}, mapDataError(err)
	}
	if stored.Revision != current.Revision {
		return model.Route{}, ErrInvalid
	}
	return current, nil
}

func (repository *ModuleDataRepository) Delete(ctx context.Context, branchID, id string, expectedRevision int64) error {
	if repository == nil || repository.data == nil || ctx == nil || branchID == "" || id == "" || expectedRevision < 1 {
		return ErrInvalid
	}
	key, current, err := repository.findRecord(ctx, branchID, id)
	if err != nil {
		return err
	}
	if current.Revision != expectedRevision {
		return ErrConflict
	}
	return mapDataError(repository.data.Delete(ctx, key, expectedRevision))
}

func (repository *ModuleDataRepository) findRecord(ctx context.Context, branchID, id string) (core.ModuleDataKey, model.Route, error) {
	keys, records, _, err := repository.data.List(ctx, routeNamespace, "branch", branchID, "", []core.ModuleDataSelector{
		{Field: "record_type", Operator: core.SelectorEqual, Values: []any{"route"}},
		{Field: "route_id", Operator: core.SelectorEqual, Values: []any{id}},
	}, 2)
	if err != nil {
		return core.ModuleDataKey{}, model.Route{}, mapDataError(err)
	}
	if len(keys) == 0 || len(records) == 0 {
		return core.ModuleDataKey{}, model.Route{}, ErrNotFound
	}
	if len(keys) != 1 || len(records) != 1 {
		return core.ModuleDataKey{}, model.Route{}, ErrInvalid
	}
	route, err := decodeRoute(records[0])
	if err != nil || route.ID != id || route.BranchID != branchID {
		return core.ModuleDataKey{}, model.Route{}, ErrInvalid
	}
	return keys[0], route, nil
}

func (repository *ModuleDataRepository) readIdempotency(ctx context.Context, branchID, key string) (idempotencyPayload, error) {
	record, err := repository.data.Get(ctx, dataKey(branchID, key))
	if errors.Is(err, core.ErrModuleDataNotFound) {
		return idempotencyPayload{}, ErrNotFound
	}
	if err != nil {
		return idempotencyPayload{}, mapDataError(err)
	}
	var payload idempotencyPayload
	if json.Unmarshal(record.Payload, &payload) != nil || payload.Kind != "idempotency" || payload.Fingerprint == "" || payload.RouteKey == "" || payload.RouteID == "" {
		return idempotencyPayload{}, ErrInvalid
	}
	return payload, nil
}

func encodeRoute(route model.Route) (*core.ModuleDataValue, error) {
	payload, err := json.Marshal(routePayload{Kind: "route", Route: route})
	if err != nil {
		return nil, ErrInvalid
	}
	index, err := json.Marshal(map[string]any{"record_type": "route", "route_id": route.ID, "host": route.Host, "path": route.Path, "state": route.State})
	if err != nil {
		return nil, ErrInvalid
	}
	return &core.ModuleDataValue{Payload: payload, PayloadVersion: dataVersion, IndexData: index}, nil
}

func decodeRoute(record core.ModuleDataRecord) (model.Route, error) {
	var payload routePayload
	if record.PayloadVersion != dataVersion || json.Unmarshal(record.Payload, &payload) != nil || payload.Kind != "route" ||
		payload.Route.Revision != record.Revision || payload.Route.State != model.StateActive || payload.Route.ValidateForStorage() != nil {
		return model.Route{}, ErrInvalid
	}
	return payload.Route, nil
}

func dataKey(branchID, key string) core.ModuleDataKey {
	return core.ModuleDataKey{Namespace: routeNamespace, ScopeType: "branch", ScopeID: branchID, RecordKey: key}
}

func routeRecordKey(host, path string) string {
	digest := sha256.Sum256([]byte(host + "\x00" + path))
	return "route:" + hex.EncodeToString(digest[:])
}

func idempotencyRecordKey(value string) string {
	digest := sha256.Sum256([]byte(value))
	return "idem:" + hex.EncodeToString(digest[:])
}

func mapDataError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, core.ErrModuleDataNotFound):
		return ErrNotFound
	case errors.Is(err, core.ErrModuleDataConflict):
		return ErrConflict
	case errors.Is(err, core.ErrModuleDataInvalid), errors.Is(err, core.ErrModuleDataLimitExceeded):
		return ErrInvalid
	default:
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
}

func isNil(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
