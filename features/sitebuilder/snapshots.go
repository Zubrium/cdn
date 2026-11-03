package sitebuilder

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	featuremedia "websiteengine/features/media"
	"websiteengine/pkg/environment"
	"websiteengine/pkg/platform/validate"
	publicruntime "websiteengine/pkg/runtime"
)

var (
	ErrNotFound            = errors.New("site builder object not found")
	ErrConflict            = errors.New("site builder state changed")
	ErrInvalid             = errors.New("site builder validation failed")
	ErrSnapshotUnavailable = errors.New("no active site snapshot")
)

type DraftState struct {
	Revision  int64           `json:"revision"`
	Config    json.RawMessage `json:"config"`
	UpdatedAt time.Time       `json:"updatedAt"`
}

type SnapshotRecord struct {
	ID        uuid.UUID  `json:"id"`
	ParentID  *uuid.UUID `json:"parentSnapshotId,omitempty"`
	Sequence  int64      `json:"sequence"`
	Name      string     `json:"name"`
	Digest    string     `json:"digest"`
	CreatedAt time.Time  `json:"createdAt"`
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
	rows, err := s.pool.Query(ctx, `SELECT e.id,e.snapshot_id,s.sequence,s.name,e.environment,e.action,e.created_at
		FROM site_snapshot_events e JOIN site_snapshots s ON s.id=e.snapshot_id
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
	rows, err := s.pool.Query(ctx, `SELECT e.id,e.snapshot_id,s.sequence,s.name,e.environment,e.action,e.created_at
		FROM site_snapshot_events e JOIN site_snapshots s ON s.id=e.snapshot_id
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
	SchemaVersionID           uuid.UUID              `json:"schemaVersionId"`
	SettingsSchemaVersionID   *uuid.UUID             `json:"settingsSchemaVersionId,omitempty"`
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
	FormatVersion        int           `json:"formatVersion"`
	Config               SiteConfig    `json:"config"`
	Builds               []PinnedBuild `json:"builds"`
	DesignSystemRevision int64         `json:"designSystemRevision,omitempty"`
	DesignSystem         *DesignSystem `json:"designSystem,omitempty"`
}

type SnapshotContent struct {
	EntryID    uuid.UUID       `json:"entryId"`
	VersionID  uuid.UUID       `json:"versionId"`
	Version    int             `json:"version"`
	FeatureKey string          `json:"featureKey"`
	SortOrder  int             `json:"sortOrder"`
	SingleSlot bool            `json:"singleSlot,omitempty"`
	Data       json.RawMessage `json:"data"`
}

type PlacementRuntime struct {
	ID         string          `json:"id"`
	Slot       string          `json:"slot"`
	FeatureKey string          `json:"featureKey"`
	Version    string          `json:"version"`
	Digest     string          `json:"digest"`
	Mode       string          `json:"mode"`
	EntryURL   string          `json:"entryUrl"`
	StyleURLs  []string        `json:"styleUrls,omitempty"`
	Arguments  json.RawMessage `json:"arguments"`
	Content    json.RawMessage `json:"content,omitempty"`
	Collection string          `json:"collectionEndpoint,omitempty"`
	DetailLink *DetailLink     `json:"detailLink,omitempty"`
}

type RuntimeComposition struct {
	Snapshot        SnapshotRecord             `json:"snapshot"`
	Metadata        publicruntime.HeadMetadata `json:"metadata"`
	Route           Route                      `json:"route"`
	RouteArgs       map[string]string          `json:"routeArgs,omitempty"`
	Page            Page                       `json:"page"`
	SharedStyleURLs []string                   `json:"sharedStyleUrls,omitempty"`
	ThemeStyleURL   string                     `json:"themeStyleUrl,omitempty"`
	SystemAssets    RuntimeSystemAssets        `json:"systemAssets,omitempty"`
	Placements      []PlacementRuntime         `json:"placements"`
}

type RuntimeSystemAssets struct {
	LogoURL string `json:"logoUrl,omitempty"`
}

type CollectionPage struct {
	Items      []SnapshotContent `json:"items"`
	NextOffset int               `json:"nextOffset,omitempty"`
	Total      int               `json:"total"`
}

// ContentFeature describes an editable feature pinned by the selected snapshot.
// It deliberately exposes the schema shipped with the pinned build rather
// than a mutable feature draft.
type ContentFeature struct {
	ID            uuid.UUID       `json:"id"`
	Key           string          `json:"key"`
	Name          string          `json:"name"`
	Category      string          `json:"category,omitempty"`
	DraftRevision int64           `json:"draftRevision,omitempty"`
	Mode          string          `json:"mode"`
	Version       string          `json:"version"`
	BuildDigest   string          `json:"buildDigest"`
	Schema        json.RawMessage `json:"schema"`
	Settings      json.RawMessage `json:"settingsSchema,omitempty"`
	Submission    json.RawMessage `json:"submissionSchema,omitempty"`
}

type SnapshotService struct {
	pool      *pgxpool.Pool
	builds    *BuildService
	media     *featuremedia.Service
	previewMu sync.Mutex
	previews  map[string]designPreview
}

func NewSnapshotService(pool *pgxpool.Pool, builds *BuildService) *SnapshotService {
	return &SnapshotService{pool: pool, builds: builds, previews: map[string]designPreview{}}
}

func (s *SnapshotService) SetMediaService(service *featuremedia.Service) {
	s.media = service
}

func (s *SnapshotService) GetDraft(ctx context.Context) (DraftState, error) {
	if _, err := s.pool.Exec(ctx, `INSERT INTO site_draft_config (id,config,revision) VALUES (TRUE,'{"formatVersion":3,"pages":[{"id":"home","name":"Главная","kind":"page","placements":[]}],"routeTree":{"id":"site","type":"site","children":[{"id":"home-route","type":"path","path":"/","pageId":"home","children":[]}]}}'::jsonb,1) ON CONFLICT (id) DO NOTHING`); err != nil {
		return DraftState{}, err
	}
	var state DraftState
	if err := s.pool.QueryRow(ctx, `SELECT revision,config,updated_at FROM site_draft_config WHERE id=TRUE`).Scan(&state.Revision, &state.Config, &state.UpdatedAt); err != nil {
		return DraftState{}, err
	}
	return state, nil
}

func (s *SnapshotService) SaveDraft(ctx context.Context, actor uuid.UUID, expectedRevision int64, raw json.RawMessage) (DraftState, error) {
	cfg, err := ParseSiteConfig(raw)
	if err != nil {
		return DraftState{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	registry, err := s.registryForConfig(ctx, cfg)
	if err != nil {
		return DraftState{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if err := cfg.Validate(registry); err != nil {
		return DraftState{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return DraftState{}, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(ctx, `INSERT INTO site_draft_config (id,config,revision,updated_by) VALUES (TRUE,'{"formatVersion":3,"pages":[{"id":"home","name":"Главная","kind":"page","placements":[]}],"routeTree":{"id":"site","type":"site","children":[{"id":"home-route","type":"path","path":"/","pageId":"home","children":[]}]}}'::jsonb,1,$1) ON CONFLICT (id) DO NOTHING`, actor); err != nil {
		return DraftState{}, err
	}
	var current int64
	if err := tx.QueryRow(ctx, `SELECT revision FROM site_draft_config WHERE id=TRUE FOR UPDATE`).Scan(&current); err != nil {
		return DraftState{}, err
	}
	if current != expectedRevision {
		return DraftState{}, fmt.Errorf("%w: expected revision %d, current %d", ErrConflict, expectedRevision, current)
	}
	var state DraftState
	err = tx.QueryRow(ctx, `UPDATE site_draft_config SET config=$1::jsonb,revision=revision+1,updated_by=$2,updated_at=now() WHERE id=TRUE RETURNING revision,config,updated_at`, string(raw), actor).Scan(&state.Revision, &state.Config, &state.UpdatedAt)
	if err != nil {
		return DraftState{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return DraftState{}, err
	}
	return state, nil
}

func (s *SnapshotService) Create(ctx context.Context, actor uuid.UUID, name string) (SnapshotRecord, error) {
	if _, err := s.GetMetadata(ctx); err != nil {
		return SnapshotRecord{}, err
	}
	if _, err := s.GetDesignSystem(ctx); err != nil {
		return SnapshotRecord{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return SnapshotRecord{}, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var metadataRevision int64
	if err := tx.QueryRow(ctx, `SELECT revision FROM site_metadata_state WHERE id=TRUE FOR UPDATE`).Scan(&metadataRevision); err != nil {
		return SnapshotRecord{}, err
	}
	var designRevision int64
	if err := tx.QueryRow(ctx, `SELECT revision FROM site_design_system_state WHERE id=TRUE FOR UPDATE`).Scan(&designRevision); err != nil {
		return SnapshotRecord{}, err
	}
	var raw []byte
	if err := tx.QueryRow(ctx, `SELECT config FROM site_draft_config WHERE id=TRUE FOR UPDATE`).Scan(&raw); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return SnapshotRecord{}, fmt.Errorf("%w: site draft is empty", ErrInvalid)
		}
		return SnapshotRecord{}, err
	}
	cfg, err := ParseSiteConfig(raw)
	if err != nil {
		return SnapshotRecord{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	registry, err := s.registryForConfig(ctx, cfg)
	if err != nil {
		return SnapshotRecord{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if err := cfg.Validate(registry); err != nil {
		return SnapshotRecord{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	builds, err := s.pinnedBuilds(ctx, cfg)
	if err != nil {
		return SnapshotRecord{}, err
	}
	content, err := s.captureContent(ctx, tx, builds)
	if err != nil {
		return SnapshotRecord{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if err := validateDetailKeys(cfg, builds, content); err != nil {
		return SnapshotRecord{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	designState, err := readDesignSystem(ctx, tx)
	if err != nil {
		return SnapshotRecord{}, err
	}
	if err := validateDesignSystemMedia(ctx, tx, designState.Draft); err != nil {
		return SnapshotRecord{}, fmt.Errorf("%w: design system: %v", ErrInvalid, err)
	}
	if err := s.prepareSnapshotMedia(ctx, cfg, builds, content); err != nil {
		return SnapshotRecord{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	_ = designRevision // The row lock binds the saved theme revision to the snapshot.
	payload := SnapshotPayload{FormatVersion: 3, Config: cfg, Builds: builds, DesignSystemRevision: designState.Revision, DesignSystem: &designState.Draft}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return SnapshotRecord{}, err
	}
	checksumInput, err := json.Marshal(struct {
		Payload SnapshotPayload   `json:"payload"`
		Content []SnapshotContent `json:"content"`
	}{Payload: payload, Content: content})
	if err != nil {
		return SnapshotRecord{}, err
	}
	hash := sha256.Sum256(checksumInput)
	digest := hex.EncodeToString(hash[:])
	if name == "" {
		name = "Snapshot " + time.Now().UTC().Format("2006-01-02 15:04 UTC")
	}
	if len([]rune(name)) > 160 {
		return SnapshotRecord{}, fmt.Errorf("%w: snapshot name is too long", ErrInvalid)
	}
	var record SnapshotRecord
	record.ID = uuid.New()
	record.Name = name
	record.Digest = digest
	err = tx.QueryRow(ctx, `INSERT INTO site_snapshots (id,name,digest,payload,created_by) VALUES ($1,$2,$3,$4::jsonb,$5) RETURNING sequence,created_at`, record.ID, name, digest, string(payloadJSON), actor).Scan(&record.Sequence, &record.CreatedAt)
	if err != nil {
		return SnapshotRecord{}, err
	}
	for _, item := range content {
		if _, err := tx.Exec(ctx, `INSERT INTO site_snapshot_content (snapshot_id,feature_key,entry_id,version_id,sort_order,data) VALUES ($1,$2,$3,$4,$5,$6::jsonb)`, record.ID, item.FeatureKey, item.EntryID, item.VersionID, item.SortOrder, string(item.Data)); err != nil {
			return SnapshotRecord{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return SnapshotRecord{}, err
	}
	return record, nil
}

func (s *SnapshotService) List(ctx context.Context, limit int, beforeSequence *int64) ([]SnapshotRecord, error) {
	if limit < 1 || limit > 201 {
		limit = 51
	}
	rows, err := s.pool.Query(ctx, `SELECT id,parent_snapshot_id,sequence,name,digest,created_at FROM site_snapshots
		WHERE ($1::bigint IS NULL OR sequence < $1)
		ORDER BY sequence DESC LIMIT $2`, beforeSequence, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SnapshotRecord{}
	for rows.Next() {
		var r SnapshotRecord
		if err := rows.Scan(&r.ID, &r.ParentID, &r.Sequence, &r.Name, &r.Digest, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *SnapshotService) Promote(ctx context.Context, actor, snapshotID uuid.UUID) (SnapshotRecord, error) {
	return s.setEnvironment(ctx, actor, snapshotID, environment.Prod, "promote", true)
}

// Deploy sets either environment directly to an immutable snapshot. It does
// not require that the snapshot passed through dev first.
func (s *SnapshotService) Deploy(ctx context.Context, actor, snapshotID uuid.UUID, env environment.Environment) (SnapshotRecord, error) {
	return s.setEnvironment(ctx, actor, snapshotID, env, "deploy", false)
}

func (s *SnapshotService) Rollback(ctx context.Context, actor, snapshotID uuid.UUID, env environment.Environment) (SnapshotRecord, error) {
	return s.setEnvironment(ctx, actor, snapshotID, env, "rollback", false)
}

func (s *SnapshotService) setEnvironment(ctx context.Context, actor, snapshotID uuid.UUID, env environment.Environment, action string, requireDev bool) (SnapshotRecord, error) {
	if env != environment.Dev && env != environment.Prod {
		return SnapshotRecord{}, fmt.Errorf("%w: unsupported environment", ErrInvalid)
	}
	if _, err := s.GetMetadata(ctx); err != nil {
		return SnapshotRecord{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return SnapshotRecord{}, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var metadataRevision int64
	if err := tx.QueryRow(ctx, `SELECT revision FROM site_metadata_state WHERE id=TRUE FOR UPDATE`).Scan(&metadataRevision); err != nil {
		return SnapshotRecord{}, err
	}
	var record SnapshotRecord
	if err := tx.QueryRow(ctx, `SELECT id,parent_snapshot_id,sequence,name,digest,created_at FROM site_snapshots WHERE id=$1`, snapshotID).Scan(&record.ID, &record.ParentID, &record.Sequence, &record.Name, &record.Digest, &record.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return SnapshotRecord{}, ErrNotFound
		}
		return SnapshotRecord{}, err
	}
	if requireDev {
		var active *uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT active_snapshot_id FROM site_environments WHERE key='dev'`).Scan(&active); err != nil {
			return SnapshotRecord{}, err
		}
		if active == nil || *active != snapshotID {
			return SnapshotRecord{}, fmt.Errorf("%w: only the active dev snapshot can be promoted", ErrConflict)
		}
	}
	var payloadRaw []byte
	if err := tx.QueryRow(ctx, `SELECT payload FROM site_snapshots WHERE id=$1`, snapshotID).Scan(&payloadRaw); err != nil {
		return SnapshotRecord{}, err
	}
	var payload SnapshotPayload
	if err := json.Unmarshal(payloadRaw, &payload); err != nil {
		return SnapshotRecord{}, err
	}
	if payload.FormatVersion >= 3 {
		if err := s.restoreSnapshotContentTx(ctx, tx, actor, snapshotID, payload, env); err != nil {
			return SnapshotRecord{}, err
		}
	}
	metadataState, err := readMetadata(ctx, tx)
	if err != nil {
		return SnapshotRecord{}, err
	}
	metadata, revision := metadataState.Dev, metadataState.DevRevision
	if env == environment.Prod {
		metadata, revision = metadataState.Prod, metadataState.ProdRevision
	}
	if err := s.rebuildShellsTx(ctx, tx, env, snapshotID, payload, metadata, revision); err != nil {
		return SnapshotRecord{}, err
	}
	tag, err := tx.Exec(ctx, `UPDATE site_environments SET active_snapshot_id=$1,revision=revision+1,updated_at=now() WHERE key=$2`, snapshotID, env.String())
	if err != nil {
		return SnapshotRecord{}, err
	}
	if tag.RowsAffected() != 1 {
		return SnapshotRecord{}, fmt.Errorf("%w: environment is not initialized", ErrInvalid)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO site_snapshot_events (id,snapshot_id,environment,action,actor_token_id) VALUES ($1,$2,$3,$4,$5)`, uuid.New(), snapshotID, env.String(), action, actor); err != nil {
		return SnapshotRecord{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return SnapshotRecord{}, err
	}
	return record, nil
}

type snapshotEntryState struct {
	id    uuid.UUID
	draft *uuid.UUID
	dev   *uuid.UUID
	prod  *uuid.UUID
}

func (s *SnapshotService) restoreSnapshotContentTx(ctx context.Context, tx pgx.Tx, actor, snapshotID uuid.UUID, payload SnapshotPayload, env environment.Environment) error {
	keys := make([]string, 0, len(payload.Builds))
	buildByKey := make(map[string]PinnedBuild, len(payload.Builds))
	for _, build := range payload.Builds {
		if _, exists := buildByKey[build.Key]; exists {
			continue
		}
		keys = append(keys, build.Key)
		buildByKey[build.Key] = build
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return nil
	}

	contentRows, err := tx.Query(ctx, `SELECT sc.feature_key,sc.entry_id,sc.version_id,sc.sort_order,e.single_slot,sc.data FROM site_snapshot_content sc JOIN feature_entries e ON e.id=sc.entry_id WHERE sc.snapshot_id=$1 AND sc.feature_key=ANY($2::text[]) ORDER BY sc.feature_key,sc.sort_order,sc.entry_id`, snapshotID, keys)
	if err != nil {
		return err
	}
	contentByEntry := make(map[uuid.UUID]SnapshotContent)
	for contentRows.Next() {
		var item SnapshotContent
		var data []byte
		if err := contentRows.Scan(&item.FeatureKey, &item.EntryID, &item.VersionID, &item.SortOrder, &item.SingleSlot, &data); err != nil {
			contentRows.Close()
			return err
		}
		item.Data = data
		contentByEntry[item.EntryID] = item
	}
	if err := contentRows.Err(); err != nil {
		contentRows.Close()
		return err
	}
	contentRows.Close()

	column := "dev_version_id"
	if env == environment.Prod {
		column = "prod_version_id"
	}
	for _, key := range keys {
		rows, err := tx.Query(ctx, `SELECT e.id,e.draft_version_id,e.dev_version_id,e.prod_version_id FROM features f JOIN feature_entries e ON e.feature_id=f.id WHERE f.key=$1 ORDER BY e.id FOR UPDATE OF e`, key)
		if err != nil {
			return err
		}
		entries := make([]snapshotEntryState, 0)
		for rows.Next() {
			var entry snapshotEntryState
			if err := rows.Scan(&entry.id, &entry.draft, &entry.dev, &entry.prod); err != nil {
				rows.Close()
				return err
			}
			entries = append(entries, entry)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()

		build := buildByKey[key]
		for _, entry := range entries {
			item, included := contentByEntry[entry.id]
			var nextVersion *uuid.UUID
			if included {
				if item.FeatureKey != key {
					return fmt.Errorf("snapshot content entry %s does not match feature %q", entry.id, key)
				}
				versionID, err := s.snapshotVersionForEntryTx(ctx, tx, actor, item, build, entry, env)
				if err != nil {
					return err
				}
				nextVersion = &versionID
			}
			if _, err := tx.Exec(ctx, `UPDATE feature_entries SET `+column+`=$2,revision=revision+1,updated_at=now() WHERE id=$1`, entry.id, nextVersion); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *SnapshotService) snapshotVersionForEntryTx(ctx context.Context, tx pgx.Tx, actor uuid.UUID, item SnapshotContent, build PinnedBuild, entry snapshotEntryState, env environment.Environment) (uuid.UUID, error) {
	schemaVersionID := build.SchemaVersionID
	if item.SingleSlot && build.SettingsSchemaVersionID != nil {
		schemaVersionID = *build.SettingsSchemaVersionID
	}
	current := entry.dev
	if env == environment.Prod {
		current = entry.prod
	}
	if current != nil {
		var currentSchema uuid.UUID
		var currentData []byte
		err := tx.QueryRow(ctx, `SELECT schema_version_id,data FROM feature_entry_versions WHERE id=$1 AND entry_id=$2`, *current, entry.id).Scan(&currentSchema, &currentData)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, err
		}
		if err == nil && currentSchema == schemaVersionID && string(currentData) == string(item.Data) {
			return *current, nil
		}
	}

	var sourceSchema uuid.UUID
	var sourceStatus string
	var sourceData []byte
	if err := tx.QueryRow(ctx, `SELECT schema_version_id,status,data FROM feature_entry_versions WHERE id=$1 AND entry_id=$2`, item.VersionID, entry.id).Scan(&sourceSchema, &sourceStatus, &sourceData); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, fmt.Errorf("%w: snapshot content version not found", ErrNotFound)
		}
		return uuid.Nil, err
	}
	if sourceStatus == "published" && sourceSchema == schemaVersionID && string(sourceData) == string(item.Data) {
		return item.VersionID, nil
	}

	versionID := uuid.New()
	err := tx.QueryRow(ctx, `INSERT INTO feature_entry_versions (id,entry_id,version,schema_version_id,data,status,created_by,published_at)
		SELECT $1,$2,COALESCE(MAX(version),0)+1,$3,$4::jsonb,'published',$5,now()
		FROM feature_entry_versions WHERE entry_id=$2 RETURNING id`, versionID, entry.id, schemaVersionID, string(item.Data), actor).Scan(&versionID)
	if err != nil {
		return uuid.Nil, err
	}
	return versionID, nil
}

func (s *SnapshotService) Active(ctx context.Context, env environment.Environment) (SnapshotRecord, SnapshotPayload, error) {
	var record SnapshotRecord
	var payloadRaw []byte
	err := s.pool.QueryRow(ctx, `SELECT s.id,s.sequence,s.name,s.digest,s.created_at,s.payload FROM site_environments e JOIN site_snapshots s ON s.id=e.active_snapshot_id WHERE e.key=$1`, env.String()).Scan(&record.ID, &record.Sequence, &record.Name, &record.Digest, &record.CreatedAt, &payloadRaw)
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
	previewToken := query.Get("__designPreview")
	query.Del("__designPreview")
	record, payload, err := s.Active(ctx, env)
	if err != nil {
		return RuntimeComposition{}, err
	}
	routes, err := CompileRouteTree(payload.Config.RouteTree)
	if err != nil {
		return RuntimeComposition{}, err
	}
	route, args, ok, err := MatchRoute(routes, requestPath)
	if err != nil {
		return RuntimeComposition{}, err
	}
	if !ok {
		return RuntimeComposition{}, ErrNotFound
	}
	var page Page
	for _, candidate := range payload.Config.Pages {
		if candidate.ID == route.PageID {
			page = candidate
			break
		}
	}
	metadataState, err := s.GetMetadata(ctx)
	if err != nil {
		return RuntimeComposition{}, err
	}
	metadata := metadataState.Dev
	if env == environment.Prod {
		metadata = metadataState.Prod
	}
	result := RuntimeComposition{Snapshot: record, Metadata: metadata.ForPage(page.ID), Route: route, RouteArgs: args, Page: page, Placements: []PlacementRuntime{}}
	if payload.DesignSystem != nil {
		designSystem, normalizeErr := NormalizeDesignSystem(*payload.DesignSystem)
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
	for _, placement := range page.Placements {
		info, ok := registry.Build(placement.Build.FeatureKey, placement.Build.Version)
		if !ok {
			return RuntimeComposition{}, fmt.Errorf("snapshot references missing build %s@%s", placement.Build.FeatureKey, placement.Build.Version)
		}
		arguments, err := ResolveArguments(info.Manifest.Arguments, placement.Arguments, args, query)
		if err != nil {
			return RuntimeComposition{}, err
		}
		var content json.RawMessage
		var collection string
		contentSchema := info.Manifest.Content.Schema
		if placement.CollectionSource != nil {
			var argumentMap map[string]json.RawMessage
			_ = json.Unmarshal(arguments, &argumentMap)
			lookupValue := scalarJSON(argumentMap[placement.CollectionSource.Argument])
			var entry SnapshotContent
			if payload.FormatVersion >= 3 {
				entry, err = s.findSnapshotRecord(ctx, record.ID, placement.CollectionSource.FeatureKey, placement.CollectionSource.Field, lookupValue)
			} else {
				entry, err = s.findEnvironmentRecord(ctx, env, placement.CollectionSource.FeatureKey, placement.CollectionSource.Field, lookupValue)
			}
			if err != nil {
				return RuntimeComposition{}, err
			}
			content = entry.Data
		} else if (info.Manifest.Content.Mode == "single" && manifestHasEditableContent(info.Manifest.Content.Schema)) || info.Manifest.Content.Mode == "form" {
			var data []byte
			var err error
			if payload.FormatVersion >= 3 {
				err = s.pool.QueryRow(ctx, `SELECT sc.data FROM site_snapshot_content sc JOIN feature_entries e ON e.id=sc.entry_id WHERE sc.snapshot_id=$1 AND sc.feature_key=$2 AND e.single_slot=TRUE ORDER BY sc.sort_order,sc.entry_id LIMIT 1`, record.ID, info.Manifest.Key).Scan(&data)
			} else {
				column := environmentContentColumn(env)
				err = s.pool.QueryRow(ctx, `SELECT v.data FROM features f JOIN feature_entries e ON e.feature_id=f.id JOIN feature_entry_versions v ON v.id=e.`+column+` WHERE f.key=$1 AND e.single_slot=TRUE AND e.archived_at IS NULL ORDER BY e.created_at,e.id LIMIT 1`, info.Manifest.Key).Scan(&data)
			}
			if err == nil {
				content = data
			} else if !errors.Is(err, pgx.ErrNoRows) {
				return RuntimeComposition{}, err
			}
		} else if info.Manifest.Content.Mode == "collection" {
			if len(info.Manifest.Content.SettingsSchema) > 0 {
				contentSchema = info.Manifest.Content.SettingsSchema
				var data []byte
				var err error
				if payload.FormatVersion >= 3 {
					err = s.pool.QueryRow(ctx, `SELECT sc.data FROM site_snapshot_content sc JOIN feature_entries e ON e.id=sc.entry_id WHERE sc.snapshot_id=$1 AND sc.feature_key=$2 AND e.single_slot=TRUE ORDER BY sc.sort_order,sc.entry_id LIMIT 1`, record.ID, info.Manifest.Key).Scan(&data)
				} else {
					column := environmentContentColumn(env)
					err = s.pool.QueryRow(ctx, `SELECT v.data FROM features f JOIN feature_entries e ON e.feature_id=f.id JOIN feature_entry_versions v ON v.id=e.`+column+` WHERE f.key=$1 AND e.single_slot=TRUE AND e.archived_at IS NULL ORDER BY e.created_at,e.id LIMIT 1`, info.Manifest.Key).Scan(&data)
				}
				if err == nil {
					content = data
				} else if !errors.Is(err, pgx.ErrNoRows) {
					return RuntimeComposition{}, err
				}
			}
			collection = "/api/v2/content/" + info.Manifest.Key + "/records?snapshot=" + record.ID.String()
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
		styleURLs := make([]string, 0, len(info.Manifest.Styles))
		for _, style := range info.Manifest.SharedStyles {
			url := "/feature-assets/" + info.Digest + "/" + style
			if !sharedStyleSeen[style] {
				result.SharedStyleURLs = append(result.SharedStyleURLs, url)
				sharedStyleSeen[style] = true
			}
		}
		for _, style := range info.Manifest.Styles {
			styleURLs = append(styleURLs, "/feature-assets/"+info.Digest+"/"+style)
		}
		result.Placements = append(result.Placements, PlacementRuntime{ID: placement.ID, Slot: placement.Slot, FeatureKey: info.Manifest.Key, Version: info.Version, Digest: info.Digest, Mode: info.Manifest.Content.Mode,
			EntryURL: "/feature-assets/" + info.Digest + "/" + info.Manifest.Entry, StyleURLs: styleURLs, Arguments: arguments, Content: content, Collection: collection, DetailLink: placement.DetailLink})
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

// MediaReferenced prevents deletion of media pinned by the draft theme,
// route placements, or any retained snapshot.
func (s *SnapshotService) MediaReferenced(ctx context.Context, id uuid.UUID) (bool, error) {
	var referenced bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM site_design_system_state WHERE draft::text LIKE '%' || $1 || '%'
			UNION ALL SELECT 1 FROM site_draft_config WHERE config::text LIKE '%' || $1 || '%'
			UNION ALL SELECT 1 FROM site_snapshots WHERE payload::text LIKE '%' || $1 || '%'
			UNION ALL SELECT 1 FROM site_snapshot_content WHERE data::text LIKE '%' || $1 || '%'
		)
	`, id.String()).Scan(&referenced)
	return referenced, err
}

func (s *SnapshotService) Collection(ctx context.Context, env environment.Environment, requestedSnapshot *uuid.UUID, key, field, value string, offset, limit int) (CollectionPage, error) {
	record, payload, err := s.Active(ctx, env)
	if err != nil {
		return CollectionPage{}, err
	}
	if requestedSnapshot != nil && *requestedSnapshot != record.ID {
		return CollectionPage{}, ErrNotFound
	}
	var collectionBuild *PinnedBuild
	for i := range payload.Builds {
		if payload.Builds[i].Key == key {
			collectionBuild = &payload.Builds[i]
			break
		}
	}
	if collectionBuild == nil {
		return CollectionPage{}, ErrNotFound
	}
	var manifest Manifest
	if err := json.Unmarshal(collectionBuild.Manifest, &manifest); err != nil {
		return CollectionPage{}, err
	}
	if manifest.Content.Mode != "collection" {
		return CollectionPage{}, ErrNotFound
	}
	if field != "" && !hasSchemaProperty(manifest.Content.Schema, field) {
		return CollectionPage{}, fmt.Errorf("%w: unknown collection field %q", ErrInvalid, field)
	}
	if limit < 1 || limit > 50 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	var total int
	var rows pgx.Rows
	if requestedSnapshot != nil && payload.FormatVersion >= 3 {
		if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM site_snapshot_content sc JOIN feature_entries e ON e.id=sc.entry_id WHERE sc.snapshot_id=$1 AND sc.feature_key=$2 AND e.single_slot=FALSE AND ($3='' OR sc.data->>$3=$4)`, record.ID, key, field, value).Scan(&total); err != nil {
			return CollectionPage{}, err
		}
		rows, err = s.pool.Query(ctx, `SELECT sc.entry_id,sc.version_id,sc.sort_order,v.version,sc.data FROM site_snapshot_content sc JOIN feature_entries e ON e.id=sc.entry_id JOIN feature_entry_versions v ON v.id=sc.version_id WHERE sc.snapshot_id=$1 AND sc.feature_key=$2 AND e.single_slot=FALSE AND ($3='' OR sc.data->>$3=$4) ORDER BY sc.sort_order,sc.entry_id OFFSET $5 LIMIT $6`, record.ID, key, field, value, offset, limit)
	} else {
		column := environmentContentColumn(env)
		if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM features f JOIN feature_entries e ON e.feature_id=f.id JOIN feature_entry_versions v ON v.id=e.`+column+` WHERE f.key=$1 AND e.single_slot=FALSE AND e.archived_at IS NULL AND ($2='' OR v.data->>$2=$3)`, key, field, value).Scan(&total); err != nil {
			return CollectionPage{}, err
		}
		rows, err = s.pool.Query(ctx, `SELECT e.id,v.id,e.sort_order,v.version,v.data FROM features f JOIN feature_entries e ON e.feature_id=f.id JOIN feature_entry_versions v ON v.id=e.`+column+` WHERE f.key=$1 AND e.single_slot=FALSE AND e.archived_at IS NULL AND ($2='' OR v.data->>$2=$3) ORDER BY e.sort_order,e.created_at,e.id OFFSET $4 LIMIT $5`, key, field, value, offset, limit)
	}
	if err != nil {
		return CollectionPage{}, err
	}
	defer rows.Close()
	page := CollectionPage{Items: []SnapshotContent{}, Total: total}
	for rows.Next() {
		var item SnapshotContent
		item.FeatureKey = key
		var raw []byte
		if err := rows.Scan(&item.EntryID, &item.VersionID, &item.SortOrder, &item.Version, &raw); err != nil {
			return CollectionPage{}, err
		}
		item.Data = raw
		if manifest.EngineAPI == "2" && s.media != nil {
			item.Data, err = s.media.HydrateContent(ctx, manifest.Content.Schema, item.Data)
			if err != nil {
				return CollectionPage{}, fmt.Errorf("hydrate media for collection %s: %w", key, err)
			}
		}
		page.Items = append(page.Items, item)
	}
	if err := rows.Err(); err != nil {
		return CollectionPage{}, err
	}
	if offset+len(page.Items) < total {
		page.NextOffset = offset + len(page.Items)
	}
	return page, nil
}

func (s *SnapshotService) FormSchema(ctx context.Context, env environment.Environment, key string) (uuid.UUID, json.RawMessage, error) {
	_, payload, err := s.Active(ctx, env)
	if err != nil {
		return uuid.Nil, nil, err
	}
	for _, build := range payload.Builds {
		if build.Key != key {
			continue
		}
		var manifest Manifest
		if err := json.Unmarshal(build.Manifest, &manifest); err != nil {
			return uuid.Nil, nil, err
		}
		if manifest.Content.Mode != "form" {
			return uuid.Nil, nil, ErrNotFound
		}
		if submissionSchema := manifest.SubmissionSchema(); len(submissionSchema) > 0 {
			if build.SubmissionSchemaVersionID == nil {
				return uuid.Nil, nil, fmt.Errorf("form feature %q has no pinned submission schema version", key)
			}
			return *build.SubmissionSchemaVersionID, submissionSchema, nil
		}
		return build.SchemaVersionID, manifest.Content.Schema, nil
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
func (s *SnapshotService) ContentSchema(ctx context.Context, env environment.Environment, key string) (uuid.UUID, string, json.RawMessage, error) {
	_, payload, err := s.Active(ctx, env)
	if err != nil {
		return uuid.Nil, "", nil, err
	}
	for _, build := range payload.Builds {
		if build.Key != key {
			continue
		}
		var manifest Manifest
		if err := json.Unmarshal(build.Manifest, &manifest); err != nil {
			return uuid.Nil, "", nil, err
		}
		return build.SchemaVersionID, manifest.Content.Mode, manifest.Content.Schema, nil
	}
	return uuid.Nil, "", nil, ErrNotFound
}

type MediaProfileLookup struct {
	BuildDigest string
	Profile     featuremedia.FieldProfile
	HasProfile  bool
}

// MediaProfile resolves a field path against the feature build pinned by an
// environment snapshot. The digest protects an open editor from a concurrent
// switch to another feature build.
func (s *SnapshotService) MediaProfile(ctx context.Context, env environment.Environment, key, buildVersion, schemaKind, fieldPath, expectedDigest string) (MediaProfileLookup, error) {
	var manifest Manifest
	var digest string
	if buildVersion != "" {
		build, exists, err := s.builds.Build(ctx, key, buildVersion)
		if err != nil {
			return MediaProfileLookup{}, err
		}
		if !exists {
			return MediaProfileLookup{}, ErrNotFound
		}
		if expectedDigest != "" && build.Digest != expectedDigest {
			return MediaProfileLookup{}, fmt.Errorf("%w: feature build changed; reload the editor", ErrConflict)
		}
		if err := json.Unmarshal(build.Manifest, &manifest); err != nil {
			return MediaProfileLookup{}, err
		}
		digest = build.Digest
	} else {
		_, payload, err := s.Active(ctx, env)
		if err != nil {
			return MediaProfileLookup{}, err
		}
		found := false
		for _, build := range payload.Builds {
			if build.Key != key {
				continue
			}
			if expectedDigest != "" && build.Digest != expectedDigest {
				return MediaProfileLookup{}, fmt.Errorf("%w: feature build changed; reload the editor", ErrConflict)
			}
			if err := json.Unmarshal(build.Manifest, &manifest); err != nil {
				return MediaProfileLookup{}, err
			}
			digest = build.Digest
			found = true
			break
		}
		if !found {
			return MediaProfileLookup{}, ErrNotFound
		}
	}
	var schema json.RawMessage
	switch schemaKind {
	case "content":
		schema = manifest.Content.Schema
	case "settings":
		schema = manifest.Content.SettingsSchema
	case "arguments":
		schema = manifest.Arguments
	default:
		return MediaProfileLookup{}, fmt.Errorf("%w: schemaKind must be content, settings or arguments", ErrInvalid)
	}
	if len(schema) == 0 {
		return MediaProfileLookup{}, ErrNotFound
	}
	profile, exists, err := featuremedia.ProfileAtSchema(schema, fieldPath)
	if err != nil {
		return MediaProfileLookup{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return MediaProfileLookup{BuildDigest: digest, Profile: profile, HasProfile: exists}, nil
}

func (s *SnapshotService) prepareSnapshotMedia(ctx context.Context, cfg SiteConfig, builds []PinnedBuild, content []SnapshotContent) error {
	if s.media == nil {
		return nil
	}
	schemas := make(map[string]json.RawMessage, len(builds))
	settingsSchemas := make(map[string]json.RawMessage, len(builds))
	abi := make(map[string]string, len(builds))
	for _, build := range builds {
		var manifest Manifest
		if err := json.Unmarshal(build.Manifest, &manifest); err != nil {
			return err
		}
		schemas[build.Key] = manifest.Content.Schema
		settingsSchemas[build.Key] = manifest.Content.SettingsSchema
		abi[build.Key] = manifest.EngineAPI
	}
	var failures []string
	for _, item := range content {
		if abi[item.FeatureKey] != "2" {
			continue
		}
		schema := schemas[item.FeatureKey]
		if item.SingleSlot && len(settingsSchemas[item.FeatureKey]) > 0 {
			schema = settingsSchemas[item.FeatureKey]
		}
		if err := s.media.ValidateContent(ctx, schema, item.Data, true); err != nil {
			failures = append(failures, item.FeatureKey+" entry "+item.EntryID.String()+": "+err.Error())
		}
	}
	buildByID := make(map[string]Manifest, len(builds))
	for _, build := range builds {
		var manifest Manifest
		if err := json.Unmarshal(build.Manifest, &manifest); err != nil {
			return err
		}
		buildByID[buildID(build.Key, build.Version)] = manifest
	}
	for _, page := range cfg.Pages {
		for _, placement := range page.Placements {
			manifest, exists := buildByID[buildID(placement.Build.FeatureKey, placement.Build.Version)]
			if !exists || manifest.EngineAPI != "2" {
				continue
			}
			literal := map[string]json.RawMessage{}
			for name, binding := range placement.Arguments {
				if binding.Source == "literal" && len(binding.Value) > 0 {
					literal[name] = binding.Value
				}
			}
			if len(literal) == 0 {
				continue
			}
			data, err := json.Marshal(literal)
			if err != nil {
				return err
			}
			if err := s.media.ValidateContent(ctx, manifest.Arguments, data, true); err != nil {
				failures = append(failures, page.ID+"/"+placement.ID+" arguments: "+err.Error())
			}
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("media renditions are not ready: %s", strings.Join(failures, "; "))
	}
	return nil
}

// SettingsSchema returns collection-level editorial settings pinned by an
// active site snapshot. It is separate from the per-record collection schema.
func (s *SnapshotService) SettingsSchema(ctx context.Context, env environment.Environment, key string) (uuid.UUID, string, json.RawMessage, error) {
	_, payload, err := s.Active(ctx, env)
	if err != nil {
		return uuid.Nil, "", nil, err
	}
	for _, build := range payload.Builds {
		if build.Key != key {
			continue
		}
		var manifest Manifest
		if err := json.Unmarshal(build.Manifest, &manifest); err != nil {
			return uuid.Nil, "", nil, err
		}
		if len(manifest.Content.SettingsSchema) == 0 || build.SettingsSchemaVersionID == nil {
			return uuid.Nil, "", nil, ErrNotFound
		}
		return *build.SettingsSchemaVersionID, manifest.Content.Mode, manifest.Content.SettingsSchema, nil
	}
	return uuid.Nil, "", nil, ErrNotFound
}

// ContentCatalog lists only features pinned by the active dev site snapshot.
func (s *SnapshotService) ContentCatalog(ctx context.Context) ([]ContentFeature, error) {
	_, payload, err := s.Active(ctx, environment.Dev)
	if errors.Is(err, ErrSnapshotUnavailable) {
		return []ContentFeature{}, nil
	}
	if err != nil {
		return nil, err
	}
	items := make([]ContentFeature, 0, len(payload.Builds))
	for _, build := range payload.Builds {
		var manifest Manifest
		if err := json.Unmarshal(build.Manifest, &manifest); err != nil {
			return nil, err
		}
		if !manifestHasEditableContent(manifest.Content.Schema) && len(manifest.Content.SettingsSchema) == 0 && manifest.Content.Mode != "form" {
			continue
		}
		var id uuid.UUID
		if err := s.pool.QueryRow(ctx, `SELECT id FROM features WHERE key=$1`, build.Key).Scan(&id); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			return nil, err
		}
		item := ContentFeature{ID: id, Key: build.Key, Name: build.Name, Category: manifest.Category, Mode: manifest.Content.Mode, Version: build.Version, BuildDigest: build.Digest, Schema: manifest.Content.Schema, Settings: manifest.Content.SettingsSchema}
		if submissionSchema := manifest.SubmissionSchema(); len(submissionSchema) > 0 {
			item.Submission = submissionSchema
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items, nil
}

func manifestHasEditableContent(raw json.RawMessage) bool {
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		return len(raw) > 0
	}
	return len(schema.Properties) > 0
}

func (s *SnapshotService) findSnapshotRecord(ctx context.Context, snapshotID uuid.UUID, key, field, value string) (SnapshotContent, error) {
	rows, err := s.pool.Query(ctx, `SELECT sc.entry_id,sc.version_id,sc.sort_order,v.version,sc.data FROM site_snapshot_content sc JOIN feature_entries e ON e.id=sc.entry_id JOIN feature_entry_versions v ON v.id=sc.version_id WHERE sc.snapshot_id=$1 AND sc.feature_key=$2 AND e.single_slot=FALSE AND ($3='' OR sc.data->>$3=$4) ORDER BY sc.sort_order,sc.entry_id LIMIT 2`, snapshotID, key, field, value)
	if err != nil {
		return SnapshotContent{}, err
	}
	defer rows.Close()
	var found *SnapshotContent
	for rows.Next() {
		var item SnapshotContent
		var raw []byte
		item.FeatureKey = key
		if err := rows.Scan(&item.EntryID, &item.VersionID, &item.SortOrder, &item.Version, &raw); err != nil {
			return SnapshotContent{}, err
		}
		item.Data = raw
		if found != nil {
			return SnapshotContent{}, fmt.Errorf("%w: detail lookup matched multiple records", ErrConflict)
		}
		found = &item
	}
	if err := rows.Err(); err != nil {
		return SnapshotContent{}, err
	}
	if found == nil {
		return SnapshotContent{}, ErrNotFound
	}
	return *found, nil
}

func (s *SnapshotService) findEnvironmentRecord(ctx context.Context, env environment.Environment, key, field, value string) (SnapshotContent, error) {
	column := environmentContentColumn(env)
	rows, err := s.pool.Query(ctx, `SELECT e.id,v.id,e.sort_order,v.version,v.data FROM features f JOIN feature_entries e ON e.feature_id=f.id JOIN feature_entry_versions v ON v.id=e.`+column+` WHERE f.key=$1 AND e.single_slot=FALSE AND e.archived_at IS NULL AND ($2='' OR v.data->>$2=$3) ORDER BY e.sort_order,e.created_at,e.id LIMIT 2`, key, field, value)
	if err != nil {
		return SnapshotContent{}, err
	}
	defer rows.Close()
	var found *SnapshotContent
	for rows.Next() {
		var item SnapshotContent
		var raw []byte
		item.FeatureKey = key
		if err := rows.Scan(&item.EntryID, &item.VersionID, &item.SortOrder, &item.Version, &raw); err != nil {
			return SnapshotContent{}, err
		}
		item.Data = raw
		if found != nil {
			return SnapshotContent{}, fmt.Errorf("%w: detail lookup matched multiple records", ErrConflict)
		}
		found = &item
	}
	if err := rows.Err(); err != nil {
		return SnapshotContent{}, err
	}
	if found == nil {
		return SnapshotContent{}, ErrNotFound
	}
	return *found, nil
}

func (s *SnapshotService) registryForConfig(ctx context.Context, cfg SiteConfig) (buildRegistry, error) {
	refs := configBuildRefs(cfg)
	registry := buildRegistry{items: map[string]BuildInfo{}}
	for key, version := range refs {
		record, ok, err := s.builds.Build(ctx, key, version)
		if err != nil {
			return registry, err
		}
		if !ok {
			return registry, fmt.Errorf("feature build %s@%s was not found", key, version)
		}
		var manifest Manifest
		if err := json.Unmarshal(record.Manifest, &manifest); err != nil {
			return registry, err
		}
		assetDigests := make(map[string]string, len(record.Assets))
		for assetPath, asset := range record.Assets {
			assetDigests[assetPath] = asset.SHA256
		}
		registry.items[buildID(key, version)] = BuildInfo{Manifest: manifest, Digest: record.Digest, Version: record.Version, AssetDigests: assetDigests}
	}
	return registry, nil
}

func (s *SnapshotService) pinnedBuilds(ctx context.Context, cfg SiteConfig) ([]PinnedBuild, error) {
	refs := configBuildRefs(cfg)
	keys := make([]string, 0, len(refs))
	for key := range refs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]PinnedBuild, 0, len(keys))
	for _, key := range keys {
		version := refs[key]
		record, ok, err := s.builds.Build(ctx, key, version)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("feature build %s@%s was not found", key, version)
		}
		var manifestJSON json.RawMessage
		if err := json.Unmarshal(record.Manifest, &manifestJSON); err != nil {
			return nil, err
		}
		assets := make(map[string]PinnedAsset, len(record.Assets))
		for name, a := range record.Assets {
			assets[name] = PinnedAsset{ContentType: a.ContentType, Size: a.Size, SHA256: a.SHA256}
		}
		out = append(out, PinnedBuild{Key: record.Key, Name: record.Name, Version: record.Version, SchemaVersionID: record.SchemaVersionID, SettingsSchemaVersionID: record.SettingsSchemaVersionID, SubmissionSchemaVersionID: record.SubmissionSchemaVersionID, Digest: record.Digest, Manifest: manifestJSON, Assets: assets})
	}
	return out, nil
}

func (s *SnapshotService) captureContent(ctx context.Context, tx pgx.Tx, builds []PinnedBuild) ([]SnapshotContent, error) {
	var all []SnapshotContent
	for _, build := range builds {
		var manifest Manifest
		if err := json.Unmarshal(build.Manifest, &manifest); err != nil {
			return nil, err
		}
		// A single-mode feature may intentionally contain no editorial data and
		// receive all of its inputs through placement arguments (for example a
		// page background). Ignore any historical single record left by an older
		// manifest so that it cannot break publication after that transition.
		if manifest.Content.Mode == "single" && !manifestHasEditableContent(manifest.Content.Schema) {
			continue
		}
		singleSlot := manifest.Content.Mode == "single" || manifest.Content.Mode == "form"
		if manifest.Content.Mode == "collection" && len(manifest.Content.SettingsSchema) > 0 {
			var settingsCount int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM features f JOIN feature_entries e ON e.feature_id=f.id WHERE f.key=$1 AND f.status='active' AND e.archived_at IS NULL AND COALESCE(e.draft_version_id,e.dev_version_id,e.prod_version_id) IS NOT NULL AND e.single_slot=TRUE`, build.Key).Scan(&settingsCount); err != nil {
				return nil, err
			}
			if settingsCount > 1 {
				return nil, fmt.Errorf("collection feature %q has more than one saved settings entry", build.Key)
			}
			rows, err := tx.Query(ctx, `SELECT e.id,e.sort_order,v.id,v.version,v.data FROM features f JOIN feature_entries e ON e.feature_id=f.id JOIN feature_entry_versions v ON v.id=COALESCE(e.draft_version_id,e.dev_version_id,e.prod_version_id) WHERE f.key=$1 AND f.status='active' AND e.archived_at IS NULL AND e.single_slot=TRUE ORDER BY e.created_at,e.id`, build.Key)
			if err != nil {
				return nil, err
			}
			for rows.Next() {
				var item SnapshotContent
				var raw []byte
				item.FeatureKey = build.Key
				item.SingleSlot = true
				if err := rows.Scan(&item.EntryID, &item.SortOrder, &item.VersionID, &item.Version, &raw); err != nil {
					rows.Close()
					return nil, err
				}
				fieldErrors, err := validate.Validate(manifest.Content.SettingsSchema, raw)
				if err != nil {
					rows.Close()
					return nil, err
				}
				if len(fieldErrors) > 0 {
					rows.Close()
					return nil, fmt.Errorf("collection feature %q settings are incompatible with build %s: %s", build.Key, build.Version, fieldErrors[0].Message)
				}
				item.Data = raw
				all = append(all, item)
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				return nil, err
			}
			rows.Close()
		}
		rows, err := tx.Query(ctx, `SELECT e.id,e.sort_order,v.id,v.version,v.data FROM features f JOIN feature_entries e ON e.feature_id=f.id JOIN feature_entry_versions v ON v.id=COALESCE(e.draft_version_id,e.dev_version_id,e.prod_version_id) WHERE f.key=$1 AND f.status='active' AND e.archived_at IS NULL AND e.single_slot=$2 ORDER BY e.sort_order,e.created_at,e.id`, build.Key, singleSlot)
		if err != nil {
			return nil, err
		}
		var featureContent []SnapshotContent
		for rows.Next() {
			var item SnapshotContent
			var raw []byte
			item.FeatureKey = build.Key
			item.SingleSlot = singleSlot
			if err := rows.Scan(&item.EntryID, &item.SortOrder, &item.VersionID, &item.Version, &raw); err != nil {
				rows.Close()
				return nil, err
			}
			fieldErrors, err := validate.Validate(manifest.Content.Schema, raw)
			if err != nil {
				rows.Close()
				return nil, err
			}
			if len(fieldErrors) > 0 {
				rows.Close()
				return nil, fmt.Errorf("feature %s entry %s is incompatible with build %s: %s", build.Key, item.EntryID, build.Version, fieldErrors[0].Message)
			}
			item.Data = raw
			featureContent = append(featureContent, item)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
		if singleSlot && len(featureContent) > 1 {
			return nil, fmt.Errorf("single feature %q has more than one saved content entry", build.Key)
		}
		all = append(all, featureContent...)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].FeatureKey != all[j].FeatureKey {
			return all[i].FeatureKey < all[j].FeatureKey
		}
		if all[i].SingleSlot != all[j].SingleSlot {
			return !all[i].SingleSlot
		}
		if all[i].SortOrder != all[j].SortOrder {
			return all[i].SortOrder < all[j].SortOrder
		}
		return all[i].EntryID.String() < all[j].EntryID.String()
	})
	return all, nil
}

func validateDetailKeys(cfg SiteConfig, builds []PinnedBuild, content []SnapshotContent) error {
	buildByKey := map[string]PinnedBuild{}
	for _, build := range builds {
		buildByKey[build.Key] = build
	}
	dataByFeature := map[string][]json.RawMessage{}
	for _, item := range content {
		if item.SingleSlot {
			continue
		}
		dataByFeature[item.FeatureKey] = append(dataByFeature[item.FeatureKey], item.Data)
	}
	for _, page := range cfg.Pages {
		for _, placement := range page.Placements {
			if placement.CollectionSource != nil {
				source := placement.CollectionSource
				if _, ok := buildByKey[source.FeatureKey]; !ok {
					return fmt.Errorf("detail collection %q is not included in the snapshot", source.FeatureKey)
				}
				seen := map[string]bool{}
				for _, raw := range dataByFeature[source.FeatureKey] {
					var data map[string]json.RawMessage
					if err := json.Unmarshal(raw, &data); err != nil {
						return err
					}
					key, ok := data[source.Field]
					if !ok || string(key) == "null" {
						return fmt.Errorf("detail lookup field %q is missing from a published record", source.Field)
					}
					var scalar any
					if err := json.Unmarshal(key, &scalar); err != nil {
						return err
					}
					switch scalar.(type) {
					case string, float64, bool:
					default:
						return fmt.Errorf("detail lookup field %q must contain a scalar value", source.Field)
					}
					normalized, _ := json.Marshal(scalar)
					if seen[string(normalized)] {
						return fmt.Errorf("detail lookup field %q is ambiguous in collection %q", source.Field, source.FeatureKey)
					}
					seen[string(normalized)] = true
				}
			}
			if placement.DetailLink != nil {
				sourceFound := false
				for _, candidatePage := range cfg.Pages {
					for _, detailPlacement := range candidatePage.Placements {
						if detailPlacement.CollectionSource != nil && detailPlacement.CollectionSource.FeatureKey == placement.Build.FeatureKey && detailPlacement.CollectionSource.Field == placement.DetailLink.Field {
							sourceFound = true
						}
					}
				}
				if !sourceFound {
					return fmt.Errorf("detail link field %q has no matching detail-page collection binding", placement.DetailLink.Field)
				}
			}
		}
	}
	return nil
}

func configBuildRefs(cfg SiteConfig) map[string]string {
	refs := map[string]string{}
	for _, page := range cfg.Pages {
		for _, placement := range page.Placements {
			refs[placement.Build.FeatureKey] = placement.Build.Version
			if placement.CollectionSource != nil {
				refs[placement.CollectionSource.FeatureKey] = placement.CollectionSource.Version
			}
		}
	}
	return refs
}
func buildID(key, version string) string { return key + "@" + version }
func scalarJSON(value json.RawMessage) string {
	var v any
	if json.Unmarshal(value, &v) != nil {
		return ""
	}
	switch x := v.(type) {
	case string:
		return x
	case nil:
		return ""
	default:
		return strings.TrimSpace(string(value))
	}
}

// The environment name is an enum controlled by the listener, so using the
// corresponding fixed column here is safe and keeps the query parameterized.
func environmentContentColumn(env environment.Environment) string {
	if env == environment.Prod {
		return "prod_version_id"
	}
	return "dev_version_id"
}

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
