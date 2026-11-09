package sitebuilder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	featuremedia "websiteengine/features/media"
	"websiteengine/pkg/environment"
	publicruntime "websiteengine/pkg/runtime"
)

var (
	ErrNotFound            = errors.New("site builder object not found")
	ErrConflict            = errors.New("site builder state changed")
	ErrInvalid             = errors.New("site builder validation failed")
	ErrSnapshotUnavailable = errors.New("no active site snapshot")
)

type SnapshotRecord struct {
	Warnings        []string                 `json:"warnings,omitempty"`
	ContentRevision int64                    `json:"contentRevision"`
	ID              uuid.UUID                `json:"id"`
	ParentID        *uuid.UUID               `json:"parentSnapshotId,omitempty"`
	ParentSequence  *int64                   `json:"parentSequence,omitempty"`
	ParentName      *string                  `json:"parentName,omitempty"`
	Sequence        int64                    `json:"sequence"`
	Name            string                   `json:"name"`
	Digest          string                   `json:"digest"`
	CreatedAt       time.Time                `json:"createdAt"`
	CreatedByName   *string                  `json:"createdByName,omitempty"`
	CreationSummary *SnapshotCreationSummary `json:"creationSummary,omitempty"`
}

// SnapshotEvent records an environment assignment separately from creation.
type SnapshotEvent struct {
	ID          uuid.UUID `json:"id"`
	SnapshotID  uuid.UUID `json:"snapshotId"`
	Sequence    int64     `json:"sequence"`
	Name        string    `json:"name"`
	Environment string    `json:"environment"`
	Action      string    `json:"action"`
	CreatedAt   time.Time `json:"createdAt"`
}

func (s *SnapshotService) Events(ctx context.Context, limit int) ([]SnapshotEvent, error) {
	rows, err := s.pool.Query(ctx, `SELECT e.id,COALESCE(e.snapshot_id,'00000000-0000-0000-0000-000000000000'::uuid),COALESCE(s.sequence,e.snapshot_sequence),COALESCE(s.name,e.snapshot_name),e.environment,e.action,e.created_at
		FROM site_snapshot_events e LEFT JOIN site_snapshots s ON s.id=e.snapshot_id
		ORDER BY e.created_at DESC,e.id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []SnapshotEvent{}
	for rows.Next() {
		var item SnapshotEvent
		if err := rows.Scan(&item.ID, &item.SnapshotID, &item.Sequence, &item.Name, &item.Environment, &item.Action, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// EventsPage uses a stable (timestamp, ID) cursor so concurrent deployments do
// not shift earlier pages. A cursor must belong to the requested environment.
func (s *SnapshotService) EventsPage(ctx context.Context, env environment.Environment, limit int, before *uuid.UUID) ([]SnapshotEvent, error) {
	var beforeTime *time.Time
	if before != nil {
		var t time.Time
		err := s.pool.QueryRow(ctx, `SELECT created_at FROM site_snapshot_events WHERE id=$1 AND environment=$2`, *before, env.String()).Scan(&t)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: unknown history cursor", ErrInvalid)
		}
		if err != nil {
			return nil, err
		}
		beforeTime = &t
	}
	rows, err := s.pool.Query(ctx, `SELECT e.id,COALESCE(e.snapshot_id,'00000000-0000-0000-0000-000000000000'::uuid),COALESCE(s.sequence,e.snapshot_sequence),COALESCE(s.name,e.snapshot_name),e.environment,e.action,e.created_at
		FROM site_snapshot_events e LEFT JOIN site_snapshots s ON s.id=e.snapshot_id
		WHERE e.environment=$1 AND ($2::timestamptz IS NULL OR (e.created_at,e.id)<($2::timestamptz,$3::uuid))
		ORDER BY e.created_at DESC,e.id DESC LIMIT $4`, env.String(), beforeTime, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []SnapshotEvent{}
	for rows.Next() {
		var item SnapshotEvent
		if err := rows.Scan(&item.ID, &item.SnapshotID, &item.Sequence, &item.Name, &item.Environment, &item.Action, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

type PinnedBuild struct {
	Key                       string                 `json:"key"`
	Name                      string                 `json:"name"`
	Version                   string                 `json:"version"`
	SubmissionSchemaVersionID *uuid.UUID             `json:"submissionSchemaVersionId,omitempty"`
	Digest                    string                 `json:"digest"`
	Manifest                  json.RawMessage        `json:"manifest"`
	Assets                    map[string]PinnedAsset `json:"assets"`
}

type PinnedAsset struct {
	ContentType string `json:"contentType"`
	Size        int64  `json:"size"`
	SHA256      string `json:"sha256"`
}

type SnapshotPayload struct {
	Metadata             *SiteMetadata            `json:"metadata,omitempty"`
	FormatVersion        int                      `json:"formatVersion"`
	Config               SiteConfig               `json:"config"`
	Builds               []PinnedBuild            `json:"builds"`
	DesignSystemRevision int64                    `json:"designSystemRevision,omitempty"`
	DesignSystem         *DesignSystem            `json:"designSystem,omitempty"`
	CreationSummary      *SnapshotCreationSummary `json:"creationSummary,omitempty"`
}

type PlacementRuntime struct {
	ID         string          `json:"id"`
	Slot       string          `json:"slot"`
	FeatureKey string          `json:"featureKey"`
	Version    string          `json:"version"`
	Digest     string          `json:"digest"`
	Mode       string          `json:"mode"`
	RuntimeABI string          `json:"runtimeAbi"`
	EntryURL   string          `json:"entryUrl"`
	StyleURLs  []string        `json:"styleUrls,omitempty"`
	Arguments  json.RawMessage `json:"arguments"`
	Content    json.RawMessage `json:"content,omitempty"`
	DetailLink *DetailLink     `json:"detailLink,omitempty"`
}

type RuntimeComposition struct {
	systemPage404Override     bool
	externalBackendOrigins    []string
	Snapshot                  SnapshotRecord             `json:"snapshot"`
	Metadata                  publicruntime.HeadMetadata `json:"metadata"`
	Route                     Route                      `json:"route"`
	RouteArgs                 map[string]string          `json:"routeArgs,omitempty"`
	Page                      Page                       `json:"page"`
	SharedStyleURLs           []string                   `json:"sharedStyleUrls,omitempty"`
	ThemeStyleURL             string                     `json:"themeStyleUrl,omitempty"`
	SystemAssets              RuntimeSystemAssets        `json:"systemAssets,omitempty"`
	Placements                []PlacementRuntime         `json:"placements"`
	ExternalBackendConfigured bool                       `json:"-"`
}

type RuntimeSystemAssets struct {
	LogoURL string `json:"logoUrl,omitempty"`
}

type SnapshotService struct {
	pool          *pgxpool.Pool
	builds        *BuildService
	media         *featuremedia.Service
	seoIndexMu    sync.RWMutex
	seoIndexCache map[string]seoIndexCacheEntry
	previewMu     sync.Mutex
	previews      map[string]designPreview
	previewTTL    time.Duration
}

func NewSnapshotService(pool *pgxpool.Pool, builds *BuildService) *SnapshotService {
	return &SnapshotService{pool: pool, builds: builds, previews: map[string]designPreview{}, seoIndexCache: map[string]seoIndexCacheEntry{}}
}

func (s *SnapshotService) SetMediaService(service *featuremedia.Service) {
	s.media = service
}

func (s *SnapshotService) SetDesignPreviewTTL(ttl time.Duration) error {
	if ttl <= 0 {
		return fmt.Errorf("design preview TTL must be explicitly configured")
	}
	s.previewTTL = ttl
	return nil
}

func (s *SnapshotService) List(ctx context.Context, limit int, beforeSequence *int64) ([]SnapshotRecord, error) {
	if limit < 1 {
		return nil, fmt.Errorf("%w: snapshot page size must be positive", ErrInvalid)
	}
	rows, err := s.pool.Query(ctx, `SELECT s.id,s.parent_snapshot_id,parent.sequence,parent.name,s.sequence,s.name,s.digest,s.created_at,s.content_revision,
	       s.payload->'creationSummary',creator.name
		FROM site_snapshots s
		LEFT JOIN site_snapshots parent ON parent.id=s.parent_snapshot_id
		LEFT JOIN access_tokens creator ON creator.id=s.created_by AND creator.revoked_at IS NULL
		WHERE ($1::bigint IS NULL OR s.sequence < $1)
		ORDER BY s.sequence DESC LIMIT $2`, beforeSequence, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SnapshotRecord{}
	for rows.Next() {
		var r SnapshotRecord
		var parentSequence pgtype.Int8
		var parentName pgtype.Text
		var summaryRaw []byte
		var createdByName pgtype.Text
		if err := rows.Scan(&r.ID, &r.ParentID, &parentSequence, &parentName, &r.Sequence, &r.Name, &r.Digest, &r.CreatedAt, &r.ContentRevision, &summaryRaw, &createdByName); err != nil {
			return nil, err
		}
		if parentSequence.Valid {
			r.ParentSequence = &parentSequence.Int64
		}
		if parentName.Valid {
			r.ParentName = &parentName.String
		}
		r.CreationSummary = decodeSnapshotCreationSummary(summaryRaw)
		if createdByName.Valid {
			r.CreatedByName = &createdByName.String
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *SnapshotService) Active(ctx context.Context, env environment.Environment) (SnapshotRecord, SnapshotPayload, error) {
	var record SnapshotRecord
	var payloadRaw []byte
	err := s.pool.QueryRow(ctx, `SELECT s.id,s.sequence,s.name,s.digest,s.created_at,s.content_revision,s.payload FROM site_environments e JOIN site_snapshots s ON s.id=e.active_snapshot_id WHERE e.key=$1 AND e.enabled`, env.String()).Scan(&record.ID, &record.Sequence, &record.Name, &record.Digest, &record.CreatedAt, &record.ContentRevision, &payloadRaw)
	if errors.Is(err, pgx.ErrNoRows) {
		return SnapshotRecord{}, SnapshotPayload{}, ErrSnapshotUnavailable
	}
	if err != nil {
		return SnapshotRecord{}, SnapshotPayload{}, err
	}
	var payload SnapshotPayload
	if err := json.Unmarshal(payloadRaw, &payload); err != nil {
		return SnapshotRecord{}, SnapshotPayload{}, err
	}
	return record, payload, nil
}

func (s *SnapshotService) Runtime(ctx context.Context, env environment.Environment, requestPath string, query url.Values) (RuntimeComposition, error) {
	if query == nil {
		query = url.Values{}
	}
	query = cloneURLValues(query)
	record, payload, err := s.Active(ctx, env)
	if err != nil {
		return RuntimeComposition{}, err
	}
	routes, err := CompileRouteTree(payload.Config.RouteTree)
	if err != nil {
		return RuntimeComposition{}, err
	}
	return s.runtimeForSnapshotPath(ctx, env, record, payload, routes, requestPath, query, &runtimeReadState{})
}

// runtimeReadState caches immutable read-only state while composing multiple
// candidate paths against one snapshot. PageIndex uses one instance per build
// so all candidates share the same branch policy and published model records.
// Ordinary Runtime requests allocate a fresh instance for their single path.
type runtimeReadState struct {
	branchLoaded         bool
	branchIndexable      bool
	modelLoaded          bool
	modelRecords         []ModelContentRecord
	modelSelections      []ModelContentSelection
	modelReadIndex       *modelContentReadIndex
	publicMediaReadiness map[uuid.UUID]bool
}

func (s *SnapshotService) runtimeForSnapshotPath(ctx context.Context, env environment.Environment, record SnapshotRecord, payload SnapshotPayload, routes []CompiledRoute, requestPath string, query url.Values, state *runtimeReadState) (RuntimeComposition, error) {
	if query == nil {
		query = url.Values{}
	}
	hasQuery := len(query) > 0
	previewToken := query.Get("__designPreview")
	query.Del("__designPreview")
	route, args, ok, err := MatchRoute(routes, requestPath)
	if err != nil {
		return RuntimeComposition{}, err
	}
	if !ok {
		return RuntimeComposition{}, ErrNotFound
	}
	return s.runtimeForMatchedRoute(ctx, env, record, payload, route, args, requestPath, query, hasQuery, previewToken, state)
}

func (s *SnapshotService) runtimeForMatchedRoute(ctx context.Context, env environment.Environment, record SnapshotRecord, payload SnapshotPayload, route Route, args map[string]string, requestPath string, query url.Values, hasQuery bool, previewToken string, state *runtimeReadState) (RuntimeComposition, error) {
	if state == nil {
		state = &runtimeReadState{}
	}
	systemPage404Override := routeTreeNodeIsCatchAll(payload.Config.RouteTree, route.ID) && payload.Config.SystemPages["404"] == route.PageID
	var page Page
	for _, candidate := range payload.Config.Pages {
		if candidate.ID == route.PageID {
			page = candidate
			break
		}
	}
	metadata := SiteMetadata{FormatVersion: 1}
	if payload.Metadata != nil {
		metadata = *payload.Metadata
	}
	if !state.branchLoaded {
		branchID, parseErr := uuid.Parse(env.String())
		if parseErr != nil {
			return RuntimeComposition{}, fmt.Errorf("%w: runtime requires a configured branch ID", ErrInvalid)
		}
		branch, branchErr := s.Branch(ctx, branchID)
		if branchErr != nil {
			return RuntimeComposition{}, branchErr
		}
		state.branchIndexable = branch.Indexable
		state.branchLoaded = true
	}
	result := RuntimeComposition{systemPage404Override: systemPage404Override, Snapshot: record,
		Metadata: metadataWithQueryRobots(metadata.ForPath(page.ID, requestPath, state.branchIndexable), hasQuery),
		Route:    route, RouteArgs: args, Page: page, Placements: []PlacementRuntime{}}
	if payload.DesignSystem != nil {
		designSystem, normalizeErr := NormalizeSnapshotDesignSystem(*payload.DesignSystem, payload.Builds)
		if normalizeErr != nil {
			return RuntimeComposition{}, normalizeErr
		}
		result.ThemeStyleURL = "/api/v2/site-themes/snapshots/" + record.ID.String()
		result.SystemAssets.LogoURL = systemAssetURL(designSystem.BrandLogoID)
	}
	// A preview is a temporary override of the active page composition. It must
	// also work while previewing a legacy snapshot that predates pinned themes.
	if previewToken != "" {
		preview, exists := s.designPreview(previewToken)
		if !exists {
			return RuntimeComposition{}, fmt.Errorf("%w: design preview expired", ErrNotFound)
		}
		result.ThemeStyleURL = "/api/v2/site-themes/previews/" + previewToken
		result.SystemAssets.LogoURL = systemAssetURL(preview.BrandLogoID)
	}
	sharedStyleSeen := make(map[string]bool)
	registry := payloadBuildRegistry(payload.Builds)
	if !state.modelLoaded {
		if payload.Config.DataModel != nil {
			contentState, err := readStoredModelContent(ctx, s.pool, record.ID, false)
			if err != nil {
				return RuntimeComposition{}, err
			}
			state.modelRecords = contentState.PublishedRecords
			state.modelSelections = contentState.PublishedSelection
		}
		state.modelLoaded = true
	}
	if payload.Config.DataModel != nil && state.modelReadIndex == nil {
		state.modelReadIndex = newModelContentReadIndex(state.modelRecords)
	}
	for _, placement := range page.Placements {
		info, ok := registry.Build(placement.Build.FeatureKey, placement.Build.Version)
		if !ok {
			return RuntimeComposition{}, fmt.Errorf("snapshot references missing build %s@%s", placement.Build.FeatureKey, placement.Build.Version)
		}
		arguments, err := ResolveArguments(info.Manifest.Arguments, placement.Arguments, args, query)
		if err != nil {
			return RuntimeComposition{}, err
		}
		if err := validatePublicMediaArguments(ctx, s.pool, info.Manifest.Arguments, arguments, state.publicMediaReadiness); err != nil {
			return RuntimeComposition{}, fmt.Errorf("placement %q media arguments: %w", placement.ID, err)
		}
		arguments, externalBackend, err := resolvePlacementBackendRequests(info.Manifest, payload.Config, placement, arguments, args, query)
		if err != nil {
			return RuntimeComposition{}, err
		}
		if externalBackend {
			result.ExternalBackendConfigured = true
			result.externalBackendOrigins = append(result.externalBackendOrigins, publicBackendOriginsForPlacement(info.Manifest, payload.Config, placement)...)
		}
		var content json.RawMessage
		var detailSEOContent json.RawMessage
		contentSchema, err := dataInputsRuntimeSchema(info.Manifest.DataInputs)
		if err != nil {
			return RuntimeComposition{}, fmt.Errorf("build runtime input schema for %s: %w", info.Manifest.Key, err)
		}
		modelBacked := payload.Config.DataModel != nil
		if len(info.Manifest.DataInputs) > 0 && !modelBacked {
			return RuntimeComposition{}, fmt.Errorf("snapshot has no data model for feature %s inputs", info.Manifest.Key)
		}
		if modelBacked {
			content, contentSchema, detailSEOContent, err = projectModelContentWithIndex(*payload.Config.DataModel, info.Manifest, placement, arguments, state.modelRecords, state.modelSelections, state.modelReadIndex)
			if err != nil {
				return RuntimeComposition{}, err
			}
		}
		if len(detailSEOContent) > 0 {
			result.Metadata, err = applyDetailSEOMetadata(result.Metadata, metadata, page.ID, detailSEOContent)
			if err != nil {
				return RuntimeComposition{}, err
			}
		}
		if info.Manifest.EngineAPI == "2" && s.media != nil && len(content) > 0 {
			content, err = s.media.HydrateContent(ctx, contentSchema, content)
			if err != nil {
				return RuntimeComposition{}, fmt.Errorf("hydrate media for feature %s: %w", info.Manifest.Key, err)
			}
		}
		if info.Manifest.EngineAPI == "2" && s.media != nil && len(arguments) > 0 {
			arguments, err = s.media.HydrateContent(ctx, info.Manifest.Arguments, arguments)
			if err != nil {
				return RuntimeComposition{}, fmt.Errorf("hydrate argument media for feature %s: %w", info.Manifest.Key, err)
			}
		}
		runtimeEntry := info.Manifest.RuntimeEntry()
		runtimeStyles := info.Manifest.RuntimeStyles()
		styleURLs := make([]string, 0, len(runtimeStyles))
		for _, style := range info.Manifest.RuntimeSharedStyles() {
			url := "/feature-assets/" + info.Digest + "/" + style
			if !sharedStyleSeen[style] {
				result.SharedStyleURLs = append(result.SharedStyleURLs, url)
				sharedStyleSeen[style] = true
			}
		}
		for _, style := range runtimeStyles {
			styleURLs = append(styleURLs, "/feature-assets/"+info.Digest+"/"+style)
		}
		runtimePlacement := PlacementRuntime{ID: placement.ID, Slot: placement.Slot, FeatureKey: info.Manifest.Key, Version: info.Version, Digest: info.Digest, Mode: info.Manifest.RuntimeInput.Mode,
			RuntimeABI: info.Manifest.RuntimeABI(), EntryURL: "/feature-assets/" + info.Digest + "/" + runtimeEntry, StyleURLs: styleURLs, Arguments: arguments, Content: content, DetailLink: placement.DetailLink,
		}
		result.Placements = append(result.Placements, runtimePlacement)
	}
	return result, nil
}

func systemAssetURL(id string) string {
	if id == "" {
		return ""
	}
	return "/api/v2/system-assets/" + id
}

func cloneURLValues(source url.Values) url.Values {
	clone := make(url.Values, len(source))
	for key, values := range source {
		clone[key] = append([]string(nil), values...)
	}
	return clone
}

func validatePublicMediaArguments(ctx context.Context, queryer metadataQueryer, schema, data json.RawMessage, readinessCaches ...map[uuid.UUID]bool) error {
	var readiness map[uuid.UUID]bool
	if len(readinessCaches) > 0 {
		readiness = readinessCaches[0]
	}
	ids, err := publicMediaIDsFromSchema(schema, data)
	if err != nil {
		return fmt.Errorf("invalid media argument: %w", err)
	}
	seen := make(map[uuid.UUID]struct{}, len(ids))
	for _, id := range ids {
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		readyPublic, checked := readiness[id]
		if !checked {
			if err := queryer.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM media_objects WHERE id=$1 AND status='ready' AND visibility='public')`, id).Scan(&readyPublic); err != nil {
				return err
			}
			if readiness != nil {
				readiness[id] = readyPublic
			}
		}
		if !readyPublic {
			return fmt.Errorf("media arguments must reference ready public files")
		}
	}
	return nil
}

func publicMediaIDsFromSchema(schema, data json.RawMessage) ([]uuid.UUID, error) {
	if strings.TrimSpace(string(schema)) == "" {
		return nil, nil
	}
	if strings.TrimSpace(string(data)) == "" {
		return nil, nil
	}
	var schemaValue, dataValue any
	if err := json.Unmarshal(schema, &schemaValue); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &dataValue); err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, 0)
	if err := collectPublicMediaIDs(schemaValue, dataValue, &ids); err != nil {
		return nil, err
	}
	return ids, nil
}

func collectPublicMediaIDs(schema, data any, ids *[]uuid.UUID) error {
	shape, ok := schema.(map[string]any)
	if !ok {
		return nil
	}
	if shape["format"] == "media" {
		if data == nil {
			return nil
		}
		reference, ok := data.(string)
		if !ok {
			return fmt.Errorf("expected a media ID")
		}
		if strings.TrimSpace(reference) == "" {
			return nil
		}
		id, err := uuid.Parse(reference)
		if err != nil {
			return fmt.Errorf("expected a media ID")
		}
		*ids = append(*ids, id)
		return nil
	}
	if properties, ok := shape["properties"].(map[string]any); ok {
		values, _ := data.(map[string]any)
		for key, childSchema := range properties {
			if value, exists := values[key]; exists {
				if err := collectPublicMediaIDs(childSchema, value, ids); err != nil {
					return err
				}
			}
		}
	}
	if itemSchema, ok := shape["items"]; ok {
		items, _ := data.([]any)
		for _, item := range items {
			if err := collectPublicMediaIDs(itemSchema, item, ids); err != nil {
				return err
			}
		}
	}
	return nil
}

// MediaReferenced prevents deletion of media used by a retained snapshot.
func (s *SnapshotService) MediaReferenced(ctx context.Context, id uuid.UUID) (bool, error) {
	var referenced bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM site_snapshots WHERE payload::text LIKE '%' || $1 || '%'
			UNION ALL SELECT 1 FROM site_snapshot_model_workspaces WHERE draft_records::text LIKE '%' || $1 || '%'
			UNION ALL SELECT 1 FROM site_snapshot_model_workspaces WHERE published_records::text LIKE '%' || $1 || '%'
			UNION ALL SELECT 1 FROM snapshot_model_content_publications WHERE records::text LIKE '%' || $1 || '%'
		)
	`, id.String()).Scan(&referenced)
	return referenced, err
}

func (s *SnapshotService) FormSchema(ctx context.Context, env environment.Environment, key string) (uuid.UUID, json.RawMessage, error) {
	_, payload, err := s.Active(ctx, env)
	if err != nil {
		return uuid.Nil, nil, err
	}
	return formSchemaFromPayload(payload, key)
}

// FormSchemaTx keeps the assignment stable until the submission commits.
// The caller acquires the file-reference advisory lock before this row lock.
func (s *SnapshotService) FormSchemaTx(ctx context.Context, tx pgx.Tx, env environment.Environment, key string) (uuid.UUID, json.RawMessage, error) {
	var snapshotID *uuid.UUID
	err := tx.QueryRow(ctx, `SELECT active_snapshot_id FROM site_environments WHERE key=$1 AND enabled FOR SHARE`, env.String()).Scan(&snapshotID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, nil, ErrSnapshotUnavailable
	}
	if err != nil {
		return uuid.Nil, nil, err
	}
	if snapshotID == nil {
		return uuid.Nil, nil, ErrSnapshotUnavailable
	}
	var raw []byte
	if err := tx.QueryRow(ctx, `SELECT payload FROM site_snapshots WHERE id=$1`, *snapshotID).Scan(&raw); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, nil, ErrSnapshotUnavailable
		}
		return uuid.Nil, nil, err
	}
	var payload SnapshotPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return uuid.Nil, nil, err
	}
	return formSchemaFromPayload(payload, key)
}

func formSchemaFromPayload(payload SnapshotPayload, key string) (uuid.UUID, json.RawMessage, error) {
	for _, build := range payload.Builds {
		if build.Key != key {
			continue
		}
		var manifest Manifest
		if err := json.Unmarshal(build.Manifest, &manifest); err != nil {
			return uuid.Nil, nil, err
		}
		if manifest.Form == nil {
			return uuid.Nil, nil, ErrNotFound
		}
		submissionSchema := manifest.SubmissionSchema()
		if len(submissionSchema) == 0 || build.SubmissionSchemaVersionID == nil {
			return uuid.Nil, nil, fmt.Errorf("form feature %q has no pinned submission schema version", key)
		}
		return *build.SubmissionSchemaVersionID, submissionSchema, nil
	}
	return uuid.Nil, nil, ErrNotFound
}

// FormSchemaForListing returns no schema when the selected environment has
// no active snapshot or the feature is not installed in that snapshot. Old
// submissions remain queryable even after an environment has been cleared.
func (s *SnapshotService) FormSchemaForListing(ctx context.Context, env environment.Environment, key string) (uuid.UUID, json.RawMessage, error) {
	id, schema, err := s.FormSchema(ctx, env, key)
	if errors.Is(err, ErrSnapshotUnavailable) || errors.Is(err, ErrNotFound) {
		return uuid.Nil, nil, nil
	}
	return id, schema, err
}

// ContentSchema returns the editor schema pinned by the active site snapshot.
// It makes content authoring follow the version that is actually composed on
// the site, even if newer feature builds have been uploaded but not promoted.

type MediaProfileLookup struct {
	BuildDigest string
	Profile     featuremedia.FieldProfile
	HasProfile  bool
}

// MediaProfile resolves a field path against the feature build pinned by the
// selected immutable snapshot. It does not depend on a branch assignment: the
// editor may prepare media before that snapshot is assigned to any branch.
func (s *SnapshotService) MediaProfile(ctx context.Context, snapshotID uuid.UUID, key, buildVersion, schemaKind, fieldPath, expectedDigest string) (MediaProfileLookup, error) {
	if snapshotID == uuid.Nil || key == "" || buildVersion == "" || fieldPath == "" || expectedDigest == "" {
		return MediaProfileLookup{}, fmt.Errorf("%w: snapshot, feature build, field path and digest are required", ErrInvalid)
	}
	_, payload, err := s.snapshotPayload(ctx, snapshotID)
	if err != nil {
		return MediaProfileLookup{}, err
	}
	build, ok := pinnedBuildForKey(payload.Builds, key)
	if !ok {
		return MediaProfileLookup{}, ErrNotFound
	}
	if build.Version != buildVersion || build.Digest != expectedDigest {
		return MediaProfileLookup{}, fmt.Errorf("%w: feature build changed; reload the editor", ErrConflict)
	}
	var manifest Manifest
	if err := json.Unmarshal(build.Manifest, &manifest); err != nil {
		return MediaProfileLookup{}, err
	}
	digest := build.Digest
	var profile featuremedia.FieldProfile
	var exists bool
	switch schemaKind {
	case "arguments":
		if len(manifest.Arguments) == 0 {
			return MediaProfileLookup{}, ErrNotFound
		}
		profile, exists, err = featuremedia.ProfileAtSchema(manifest.Arguments, fieldPath)
		if err != nil {
			return MediaProfileLookup{}, fmt.Errorf("%w: %v", ErrInvalid, err)
		}
	case "dataInputs":
		profile, exists, err = dataInputMediaProfile(manifest.DataInputs, fieldPath)
		if err != nil {
			return MediaProfileLookup{}, fmt.Errorf("%w: %v", ErrInvalid, err)
		}
	default:
		return MediaProfileLookup{}, fmt.Errorf("%w: media rendition profiles are available only for arguments and dataInputs", ErrInvalid)
	}
	return MediaProfileLookup{BuildDigest: digest, Profile: profile, HasProfile: exists}, nil
}

// dataInputMediaProfile resolves the combined pointer /<inputKey>/<field...>
// against the typed media profiles in a format-2 feature build.
func dataInputMediaProfile(inputs []DataInputPort, fieldPath string) (featuremedia.FieldProfile, bool, error) {
	segments, err := parseJSONPointer(fieldPath)
	if err != nil || len(segments) == 0 {
		return featuremedia.FieldProfile{}, false, fmt.Errorf("invalid dataInputs media path %q", fieldPath)
	}
	for _, input := range inputs {
		if input.Key != segments[0] {
			continue
		}
		if input.Kind == "scalar" {
			if len(segments) != 1 || input.Type != "media" {
				return featuremedia.FieldProfile{}, false, fmt.Errorf("dataInputs path %q does not identify a scalar media input", fieldPath)
			}
			if input.Profile == nil {
				return featuremedia.FieldProfile{}, false, nil
			}
			return *input.Profile, true, nil
		}
		if len(segments) < 2 {
			return featuremedia.FieldProfile{}, false, fmt.Errorf("dataInputs path %q must identify a media field", fieldPath)
		}
		outputPath := pointerKey(segments[1:])
		for _, field := range input.Fields {
			if field.Path != outputPath {
				continue
			}
			if field.Type != "media" {
				return featuremedia.FieldProfile{}, false, fmt.Errorf("dataInputs path %q is not a media field", fieldPath)
			}
			if field.Profile == nil {
				return featuremedia.FieldProfile{}, false, nil
			}
			return *field.Profile, true, nil
		}
		return featuremedia.FieldProfile{}, false, fmt.Errorf("dataInputs path %q is not declared by the feature build", fieldPath)
	}
	return featuremedia.FieldProfile{}, false, ErrNotFound
}

// SettingsSchema returns collection-level editorial settings pinned by an
// active site snapshot. It is separate from the per-record collection schema.

type buildRegistry struct{ items map[string]BuildInfo }

func (r buildRegistry) Build(key, version string) (BuildInfo, bool) {
	value, ok := r.items[buildID(key, version)]
	return value, ok
}

func payloadBuildRegistry(builds []PinnedBuild) buildRegistry {
	registry := buildRegistry{items: map[string]BuildInfo{}}
	for _, build := range builds {
		var manifest Manifest
		if json.Unmarshal(build.Manifest, &manifest) == nil {
			assetDigests := make(map[string]string, len(build.Assets))
			for assetPath, asset := range build.Assets {
				assetDigests[assetPath] = asset.SHA256
			}
			registry.items[buildID(build.Key, build.Version)] = BuildInfo{Manifest: manifest, Digest: build.Digest, Version: build.Version, AssetDigests: assetDigests}
		}
	}
	return registry
}
