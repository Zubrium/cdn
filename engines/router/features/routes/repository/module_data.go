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
	"github.com/Zubrium/cdn/engines/router/features/routes/model"
	recorddata "github.com/Zubrium/cdn/pkg/contracts"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
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
	Get(context.Context, recorddata.RecordKey) (recorddata.Record, error)
	Put(context.Context, recorddata.RecordKey, recorddata.RecordValue, int64) (recorddata.Record, error)
	Delete(context.Context, recorddata.RecordKey, int64) error
	Batch(context.Context, []recorddata.RecordMutation) error
	List(context.Context, string, string, string, string, []recorddata.RecordSelector, int) ([]recorddata.RecordKey, []recorddata.Record, string, error)
	MaxPageSize() int
}

type LocaleTargetUpdate struct {
	Locale                 string
	ArtifactManifestFileID string
	ExpectedRevision       int64
	IdempotencyKey         string
}

type Repository interface {
	Create(context.Context, model.Route, string, string) (model.Route, error)
	GetByID(context.Context, string, string) (model.Route, error)
	List(context.Context, string, string, string, int) ([]model.Route, string, error)
	FindMatch(context.Context, string, string, string) (model.Route, error)
	ReplaceTarget(context.Context, string, string, model.Target, int64, time.Time) (model.Route, error)
	ReplaceLocaleTarget(context.Context, string, string, LocaleTargetUpdate, time.Time) (model.Route, error)
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
	mutations := []recorddata.RecordMutation{
		{Key: dataKey(route.BranchID, routeKey), ExpectedRevision: 0, Value: routeValue},
		{Key: dataKey(route.BranchID, idemKey), ExpectedRevision: 0, Value: &recorddata.RecordValue{Payload: idemEncoded, PayloadVersion: dataVersion, IndexData: idemIndex}},
	}
	if err := repository.data.Batch(ctx, mutations); err != nil {
		if !errors.Is(err, recorddata.ErrRecordConflict) {
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
	keys, records, _, err := repository.data.List(ctx, routeNamespace, "branch", branchID, "", []recorddata.RecordSelector{
		{Field: "record_type", Operator: recorddata.RecordSelectorEqual, Values: []any{"route"}},
		{Field: "route_id", Operator: recorddata.RecordSelectorEqual, Values: []any{id}},
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
	selectors := []recorddata.RecordSelector{{Field: "record_type", Operator: recorddata.RecordSelectorEqual, Values: []any{"route"}}}
	if host != "" {
		selectors = append(selectors, recorddata.RecordSelector{Field: "host", Operator: recorddata.RecordSelectorEqual, Values: []any{host}})
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
	selectors := []recorddata.RecordSelector{
		{Field: "record_type", Operator: recorddata.RecordSelectorEqual, Values: []any{"route"}},
		{Field: "host", Operator: recorddata.RecordSelectorEqual, Values: []any{host}},
		{Field: "path", Operator: recorddata.RecordSelectorIn, Values: pathValues},
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
			if err != nil {
				return model.Route{}, ErrInvalid
			}
			// The path selector is a prefix filter: sibling routes with an exact
			// path that is a prefix of the request are valid records and must be
			// skipped instead of failing the whole lookup.
			if route.BranchID != branchID || route.Host != host || !model.PathMatches(route.Path, path, route.Match) {
				continue
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
	key, observed, current, err := repository.findRecord(ctx, branchID, id)
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
	if _, err := repository.data.Put(ctx, key, *value, observed.Revision); err != nil {
		return model.Route{}, mapDataError(err)
	}
	return current, nil
}

// ReplaceLocaleTarget advances one (route, locale) slot without touching the
// revisions of sibling locales or requiring the whole route target to be
// replaced. The idempotency receipt makes a retried activation safe after a
// restart or an ambiguous Core write.
func (repository *ModuleDataRepository) ReplaceLocaleTarget(ctx context.Context, branchID, id string, update LocaleTargetUpdate, now time.Time) (model.Route, error) {
	if repository == nil || repository.data == nil || ctx == nil || branchID == "" || id == "" ||
		update.Locale == "" || update.ArtifactManifestFileID == "" || update.ExpectedRevision < 1 ||
		update.ExpectedRevision == math.MaxInt64 || update.IdempotencyKey == "" || now.IsZero() {
		return model.Route{}, ErrInvalid
	}
	canonical, err := model.CanonicalLocale(update.Locale)
	if err != nil || canonical != update.Locale {
		return model.Route{}, ErrInvalid
	}
	fingerprint, err := localeTargetFingerprint(id, update)
	if err != nil {
		return model.Route{}, ErrInvalid
	}
	key, observed, current, err := repository.findRecord(ctx, branchID, id)
	if err != nil {
		return model.Route{}, err
	}
	if prior, err := repository.readIdempotency(ctx, branchID, idempotencyRecordKey(update.IdempotencyKey)); err == nil {
		if prior.Fingerprint != fingerprint || prior.RouteKey != localeReceiptRouteKey(id, update.Locale) {
			return model.Route{}, ErrConflict
		}
		return current, nil
	} else if !errors.Is(err, ErrNotFound) {
		return model.Route{}, err
	}
	index := -1
	for position, localeTarget := range current.Target.LocaleTargets {
		if localeTarget.Locale == update.Locale {
			index = position
			break
		}
	}
	if index < 0 || current.Target.Kind != model.TargetStaticSiteArtifact {
		return model.Route{}, ErrNotFound
	}
	if current.Target.LocaleTargets[index].Revision != update.ExpectedRevision {
		return model.Route{}, ErrConflict
	}
	if now.Before(current.CreatedAt) {
		return model.Route{}, ErrInvalid
	}
	updated := current
	updated.Target.LocaleTargets = append([]model.LocaleArtifactTarget(nil), current.Target.LocaleTargets...)
	updated.Target.LocaleTargets[index].ArtifactManifestFileID = update.ArtifactManifestFileID
	updated.Target.LocaleTargets[index].Revision = update.ExpectedRevision + 1
	updated.UpdatedAt = now.UTC()
	routeValue, err := encodeRoute(updated)
	if err != nil {
		return model.Route{}, err
	}
	receipt, err := json.Marshal(idempotencyPayload{
		Kind: "idempotency", Fingerprint: fingerprint,
		RouteKey: localeReceiptRouteKey(id, update.Locale), RouteID: id,
	})
	if err != nil {
		return model.Route{}, ErrInvalid
	}
	receiptIndex, _ := json.Marshal(map[string]any{"record_type": "idempotency"})
	mutations := []recorddata.RecordMutation{
		{Key: key, ExpectedRevision: observed.Revision, Value: routeValue},
		{Key: dataKey(branchID, idempotencyRecordKey(update.IdempotencyKey)), ExpectedRevision: 0,
			Value: &recorddata.RecordValue{Payload: receipt, PayloadVersion: dataVersion, IndexData: receiptIndex}},
	}
	if err := repository.data.Batch(ctx, mutations); err != nil {
		if !errors.Is(err, recorddata.ErrRecordConflict) {
			return model.Route{}, mapDataError(err)
		}
		if prior, readErr := repository.readIdempotency(ctx, branchID, idempotencyRecordKey(update.IdempotencyKey)); readErr == nil {
			if prior.Fingerprint != fingerprint || prior.RouteKey != localeReceiptRouteKey(id, update.Locale) {
				return model.Route{}, ErrConflict
			}
			_, _, persisted, readErr := repository.findRecord(ctx, branchID, id)
			if readErr != nil {
				return model.Route{}, readErr
			}
			return persisted, nil
		} else if !errors.Is(readErr, ErrNotFound) {
			return model.Route{}, readErr
		}
		return model.Route{}, ErrConflict
	}
	return updated, nil
}

func (repository *ModuleDataRepository) Delete(ctx context.Context, branchID, id string, expectedRevision int64) error {
	if repository == nil || repository.data == nil || ctx == nil || branchID == "" || id == "" || expectedRevision < 1 {
		return ErrInvalid
	}
	key, observed, current, err := repository.findRecord(ctx, branchID, id)
	if err != nil {
		return err
	}
	if current.Revision != expectedRevision {
		return ErrConflict
	}
	return mapDataError(repository.data.Delete(ctx, key, observed.Revision))
}

func (repository *ModuleDataRepository) findRecord(ctx context.Context, branchID, id string) (recorddata.RecordKey, recorddata.Record, model.Route, error) {
	keys, records, _, err := repository.data.List(ctx, routeNamespace, "branch", branchID, "", []recorddata.RecordSelector{
		{Field: "record_type", Operator: recorddata.RecordSelectorEqual, Values: []any{"route"}},
		{Field: "route_id", Operator: recorddata.RecordSelectorEqual, Values: []any{id}},
	}, 2)
	if err != nil {
		return recorddata.RecordKey{}, recorddata.Record{}, model.Route{}, mapDataError(err)
	}
	if len(keys) == 0 || len(records) == 0 {
		return recorddata.RecordKey{}, recorddata.Record{}, model.Route{}, ErrNotFound
	}
	if len(keys) != 1 || len(records) != 1 {
		return recorddata.RecordKey{}, recorddata.Record{}, model.Route{}, ErrInvalid
	}
	route, err := decodeRoute(records[0])
	if err != nil || route.ID != id || route.BranchID != branchID {
		return recorddata.RecordKey{}, recorddata.Record{}, model.Route{}, ErrInvalid
	}
	return keys[0], records[0], route, nil
}

func (repository *ModuleDataRepository) readIdempotency(ctx context.Context, branchID, key string) (idempotencyPayload, error) {
	record, err := repository.data.Get(ctx, dataKey(branchID, key))
	if errors.Is(err, recorddata.ErrRecordNotFound) {
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

func encodeRoute(route model.Route) (*recorddata.RecordValue, error) {
	payload, err := json.Marshal(routePayload{Kind: "route", Route: route})
	if err != nil {
		return nil, ErrInvalid
	}
	index, err := json.Marshal(map[string]any{
		"record_type": "route", "route_id": route.ID, "host": route.Host, "path": route.Path,
		"state": route.State, "locale_revisions": encodeLocaleRevisions(route.Target.LocaleTargets),
	})
	if err != nil {
		return nil, ErrInvalid
	}
	return &recorddata.RecordValue{Payload: payload, PayloadVersion: dataVersion, IndexData: index}, nil
}

func decodeRoute(record recorddata.Record) (model.Route, error) {
	var payload routePayload
	// Core's record revision is global and opaque; Router's route revision is
	// a logical CAS token stored in the payload and advanced only by Router.
	if record.PayloadVersion != dataVersion || json.Unmarshal(record.Payload, &payload) != nil || payload.Kind != "route" ||
		payload.Route.Revision < 1 || payload.Route.State != model.StateActive {
		return model.Route{}, ErrInvalid
	}
	var index struct {
		LocaleRevisions string `json:"locale_revisions"`
	}
	if json.Unmarshal(record.IndexData, &index) != nil {
		return model.Route{}, ErrInvalid
	}
	revisions, err := decodeLocaleRevisions(index.LocaleRevisions)
	if err != nil || len(revisions) != len(payload.Route.Target.LocaleTargets) {
		return model.Route{}, ErrInvalid
	}
	for position := range payload.Route.Target.LocaleTargets {
		item := &payload.Route.Target.LocaleTargets[position]
		revision, found := revisions[item.Locale]
		if !found || revision < 1 {
			return model.Route{}, ErrInvalid
		}
		item.Revision = revision
	}
	if payload.Route.ValidateForStorage() != nil {
		return model.Route{}, ErrInvalid
	}
	return payload.Route, nil
}

// encodeLocaleRevisions stores opaque per-locale CAS revisions in the one
// scalar projection field allowed by Core's module-data contract. The payload
// omits revisions so callers never see them, while the durable index keeps
// them independent from the route's own revision.
func encodeLocaleRevisions(items []model.LocaleArtifactTarget) string {
	entries := make([]string, 0, len(items))
	for _, item := range items {
		entries = append(entries, item.Locale+"="+strconv.FormatInt(item.Revision, 10))
	}
	sort.Strings(entries)
	return strings.Join(entries, ",")
}

func decodeLocaleRevisions(encoded string) (map[string]int64, error) {
	result := make(map[string]int64)
	if encoded == "" {
		return result, nil
	}
	for _, entry := range strings.Split(encoded, ",") {
		locale, revisionText, found := strings.Cut(entry, "=")
		if !found || locale == "" {
			return nil, ErrInvalid
		}
		revision, err := strconv.ParseInt(revisionText, 10, 64)
		if err != nil || revision < 1 {
			return nil, ErrInvalid
		}
		if _, duplicate := result[locale]; duplicate {
			return nil, ErrInvalid
		}
		result[locale] = revision
	}
	return result, nil
}

func dataKey(branchID, key string) recorddata.RecordKey {
	return recorddata.RecordKey{Namespace: routeNamespace, ScopeType: "branch", ScopeID: branchID, RecordKey: key}
}

func routeRecordKey(host, path string) string {
	digest := sha256.Sum256([]byte(host + "\x00" + path))
	return "route:" + hex.EncodeToString(digest[:])
}

func localeReceiptRouteKey(routeID, locale string) string {
	return "locale:" + routeID + ":" + locale
}

func localeTargetFingerprint(routeID string, update LocaleTargetUpdate) (string, error) {
	encoded, err := json.Marshal(struct {
		RouteID                string `json:"route_id"`
		Locale                 string `json:"locale"`
		ArtifactManifestFileID string `json:"artifact_manifest_file_id"`
		ExpectedRevision       int64  `json:"expected_revision"`
	}{routeID, update.Locale, update.ArtifactManifestFileID, update.ExpectedRevision})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func idempotencyRecordKey(value string) string {
	digest := sha256.Sum256([]byte(value))
	return "idem:" + hex.EncodeToString(digest[:])
}

func mapDataError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, recorddata.ErrRecordNotFound):
		return ErrNotFound
	case errors.Is(err, recorddata.ErrRecordConflict):
		return ErrConflict
	case errors.Is(err, recorddata.ErrRecordInvalid), errors.Is(err, recorddata.ErrRecordLimit):
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
