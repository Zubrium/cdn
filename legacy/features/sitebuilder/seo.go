package sitebuilder

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/google/uuid"
	"websiteengine/pkg/environment"
	publicruntime "websiteengine/pkg/runtime"
)

// PageDocument resolves real runtime data before returning HTML, so detail
// records and route errors have the same status with and without JavaScript.
func (s *SnapshotService) PageDocument(ctx context.Context, env environment.Environment, requestPath string, query url.Values, indexable bool) (publicruntime.Document, error) {
	composition, status, err := s.PublicRuntime(ctx, env, requestPath, query)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, ErrNotFound) {
			status = http.StatusNotFound
		}
		if errors.Is(err, ErrSnapshotUnavailable) {
			status = http.StatusServiceUnavailable
		}
		return publicruntime.SystemDocument(status), nil
	}
	metadata := composition.Metadata
	if !indexable {
		metadata.Robots = "noindex,nofollow"
	}
	if status != http.StatusOK {
		metadata.Robots = "noindex,nofollow"
		if metadata.Title == "" {
			metadata.Title = publicruntime.ErrorPageTitle(status)
		}
	}
	metadata = metadataWithQueryRobots(metadata, len(query) > 0)
	staticFeatures := make([]publicruntime.StaticHTMLFeature, 0)
	for _, placement := range composition.Placements {
		if placement.RuntimeABI != RuntimeABIStaticHTML {
			continue
		}
		const prefix = "/feature-assets/"
		if !strings.HasPrefix(placement.EntryURL, prefix) {
			return publicruntime.Document{}, fmt.Errorf("static HTML feature %q has an invalid entry URL", placement.FeatureKey)
		}
		pinnedAsset := strings.TrimPrefix(placement.EntryURL, prefix)
		digest, entry, ok := strings.Cut(pinnedAsset, "/")
		if !ok || len(digest) != 64 || entry == "" {
			return publicruntime.Document{}, fmt.Errorf("static HTML feature %q has an invalid pinned entry", placement.FeatureKey)
		}
		asset, _, err := s.builds.Asset(ctx, digest, entry)
		if err != nil {
			return publicruntime.Document{}, fmt.Errorf("load static HTML feature %q: %w", placement.FeatureKey, err)
		}
		fragment, readErr := io.ReadAll(io.LimitReader(asset, maxStaticHTMLBytes+1))
		closeErr := asset.Close()
		if readErr != nil {
			return publicruntime.Document{}, fmt.Errorf("read static HTML feature %q: %w", placement.FeatureKey, readErr)
		}
		if closeErr != nil {
			return publicruntime.Document{}, fmt.Errorf("close static HTML feature %q: %w", placement.FeatureKey, closeErr)
		}
		if len(fragment) > maxStaticHTMLBytes {
			return publicruntime.Document{}, fmt.Errorf("static HTML feature %q exceeds the runtime size limit", placement.FeatureKey)
		}
		staticFeatures = append(staticFeatures, publicruntime.StaticHTMLFeature{
			FeatureKey:  placement.FeatureKey,
			PlacementID: placement.ID,
			Slot:        placement.Slot,
			Digest:      digest,
			Entry:       entry,
			HTML:        string(fragment),
			StyleURLs:   placement.StyleURLs,
		})
	}
	rootPlacements := make([]publicruntime.PlacementRoot, 0, len(composition.Placements))
	for _, placement := range composition.Placements {
		rootPlacements = append(rootPlacements, publicruntime.PlacementRoot{
			ID: placement.ID, Slot: placement.Slot, FeatureKey: placement.FeatureKey,
		})
	}
	bootstrap, err := json.Marshal(composition)
	if err != nil {
		return publicruntime.Document{}, fmt.Errorf("encode resolved page composition: %w", err)
	}
	shell, err := publicruntime.RenderPageDocument(metadata, composition.Page.Kind, composition.Snapshot.ID.String(), composition.Snapshot.ContentRevision, rootPlacements, bootstrap)
	if err != nil {
		return publicruntime.Document{}, fmt.Errorf("render resolved page composition: %w", err)
	}
	if len(staticFeatures) > 0 {
		staticStyles := append([]string(nil), composition.SharedStyleURLs...)
		if composition.ThemeStyleURL != "" {
			staticStyles = append(staticStyles, composition.ThemeStyleURL)
		}
		shell, err = publicruntime.RenderStaticHTMLFallback(shell, staticStyles, staticFeatures)
		if err != nil {
			return publicruntime.Document{}, err
		}
	}
	// Cache identity follows the resolved runtime output, not the raw query
	// string. This keeps tracking parameters and query ordering from creating
	// duplicate entries while retaining every bound argument and theme change.
	identity, err := json.Marshal(struct {
		BranchID         string                     `json:"branchId"`
		SnapshotID       uuid.UUID                  `json:"snapshotId"`
		ContentRevision  int64                      `json:"contentRevision"`
		StructuralDigest string                     `json:"structuralDigest"`
		Path             string                     `json:"path"`
		Status           int                        `json:"status"`
		Metadata         publicruntime.HeadMetadata `json:"metadata"`
		RouteArgs        map[string]string          `json:"routeArgs,omitempty"`
		ThemeStyleURL    string                     `json:"themeStyleUrl,omitempty"`
		SystemAssets     RuntimeSystemAssets        `json:"systemAssets,omitempty"`
		Placements       []PlacementRuntime         `json:"placements"`
	}{
		BranchID: env.String(), SnapshotID: composition.Snapshot.ID,
		ContentRevision: composition.Snapshot.ContentRevision, StructuralDigest: composition.Snapshot.Digest,
		Path: requestPath, Status: status, Metadata: metadata, RouteArgs: composition.RouteArgs,
		ThemeStyleURL: composition.ThemeStyleURL, SystemAssets: composition.SystemAssets, Placements: composition.Placements,
	})
	if err != nil {
		return publicruntime.Document{}, err
	}
	hash := sha256.Sum256(identity)
	return publicruntime.Document{
		HTML: shell, Status: status, CacheKey: hex.EncodeToString(hash[:]),
		SnapshotID: composition.Snapshot.ID.String(), ContentRevision: composition.Snapshot.ContentRevision,
		Cacheable:      !composition.ExternalBackendConfigured && status == http.StatusOK && indexable && len(query) == 0 && metadata.Robots == "index,follow",
		SkipPrerender:  composition.ExternalBackendConfigured,
		ConnectOrigins: composition.externalBackendOrigins,
	}, nil
}

// metadataWithQueryRobots applies the query-variant indexing policy shared by
// the server shell and the interactive runtime. A query must not turn a
// preview or explicitly noindex page into a followable document.
func metadataWithQueryRobots(metadata publicruntime.HeadMetadata, hasQuery bool) publicruntime.HeadMetadata {
	if hasQuery && metadata.Robots == "index,follow" {
		metadata.Robots = "noindex,follow"
	}
	return metadata
}

// applyDetailSEOMetadata overlays configured model fields from the published
// record selected by a detail route. Empty fields inherit the page/site SEO
// settings, and a derived OG title/description follows the corresponding
// dynamic title/description unless an explicit static value is configured.
func applyDetailSEOMetadata(head publicruntime.HeadMetadata, site SiteMetadata, pageID string, raw json.RawMessage) (publicruntime.HeadMetadata, error) {
	var values struct {
		Title         string `json:"title"`
		Description   string `json:"description"`
		OGTitle       string `json:"ogTitle"`
		OGDescription string `json:"ogDescription"`
		OGImage       string `json:"ogImage"`
	}
	if err := json.Unmarshal(raw, &values); err != nil {
		return head, fmt.Errorf("decode published detail SEO fields: %w", err)
	}
	if values.Title != "" {
		head.Title = values.Title
		if values.OGTitle == "" && !hasExplicitPageMetadata(site, pageID, "ogTitle") {
			head.OGTitle = values.Title
		}
	}
	if values.Description != "" {
		head.Description = values.Description
		if values.OGDescription == "" && !hasExplicitPageMetadata(site, pageID, "ogDescription") {
			head.OGDescription = values.Description
		}
	}
	if values.OGTitle != "" {
		head.OGTitle = values.OGTitle
	}
	if values.OGDescription != "" {
		head.OGDescription = values.OGDescription
	}
	if values.OGImage != "" {
		id, err := uuid.Parse(values.OGImage)
		if err != nil {
			return head, fmt.Errorf("published detail OG image is not a media ID")
		}
		head.OGImageURL = absoluteMetadataURL(site.CanonicalDomain, mediaURL(id.String()))
	}
	return head, nil
}

func hasExplicitPageMetadata(metadata SiteMetadata, pageID, field string) bool {
	switch field {
	case "ogTitle":
		if strings.TrimSpace(metadata.OGTitle) != "" {
			return true
		}
		if page, ok := metadata.Pages[pageID]; ok && page.OGTitle != nil {
			return strings.TrimSpace(*page.OGTitle) != ""
		}
	case "ogDescription":
		if strings.TrimSpace(metadata.OGDescription) != "" {
			return true
		}
		if page, ok := metadata.Pages[pageID]; ok && page.OGDescription != nil {
			return strings.TrimSpace(*page.OGDescription) != ""
		}
	}
	return false
}

// PublicRuntime can substitute an explicitly configured system page. The
// override must have a concrete route; a dynamic-only page cannot be supplied
// with invented route arguments. Errors in the override return the safe core
// fallback rather than recursing through the same failing feature.
func (s *SnapshotService) PublicRuntime(ctx context.Context, env environment.Environment, requestPath string, query url.Values) (RuntimeComposition, int, error) {
	composition, err := s.Runtime(ctx, env, requestPath, query)
	if err == nil {
		if composition.systemPage404Override {
			composition.Metadata.Robots = "noindex,nofollow"
			return composition, http.StatusNotFound, nil
		}
		return composition, http.StatusOK, nil
	}
	status := http.StatusInternalServerError
	if errors.Is(err, ErrNotFound) {
		status = http.StatusNotFound
	}
	if errors.Is(err, ErrSnapshotUnavailable) {
		status = http.StatusServiceUnavailable
	}
	_, payload, payloadErr := s.Active(ctx, env)
	if payloadErr != nil {
		return RuntimeComposition{}, status, err
	}
	pageID := payload.Config.SystemPages[fmt.Sprint(status)]
	if pageID == "" {
		return RuntimeComposition{}, status, err
	}
	paths, _ := staticPagePaths(payload.Config.RouteTree)
	routes, routeErr := CompileRouteTree(payload.Config.RouteTree)
	if routeErr != nil {
		return RuntimeComposition{}, status, err
	}
	for _, candidate := range paths {
		route, _, matched, matchErr := MatchRoute(routes, candidate)
		if matchErr != nil || !matched || route.PageID != pageID {
			continue
		}
		override, overrideErr := s.Runtime(ctx, env, candidate, nil)
		if overrideErr != nil {
			return RuntimeComposition{}, status, err
		}
		override.Metadata.Robots = "noindex,nofollow"
		return override, status, nil
	}
	return RuntimeComposition{}, status, err
}

func routeTreeNodeIsCatchAll(root RouteTreeNode, routeID string) bool {
	var walk func(RouteTreeNode) bool
	walk = func(node RouteTreeNode) bool {
		if node.ID == routeID {
			if node.Type == "catchAll" {
				return true
			}
			if node.Type == "path" {
				for _, segment := range strings.Split(strings.TrimPrefix(node.Path, "/"), "/") {
					if strings.HasPrefix(segment, "*") {
						return true
					}
				}
			}
			return false
		}
		for _, child := range node.Children {
			if walk(child) {
				return true
			}
		}
		return false
	}
	return walk(root)
}

type PageIndex struct {
	CanonicalDomain string   `json:"canonicalDomain"`
	Indexable       bool     `json:"indexable"`
	URLs            []string `json:"urls"`
	Warnings        []string `json:"warnings"`
}

type seoIndexCacheEntry struct {
	key   string
	value PageIndex
}

func (s *SnapshotService) PageIndex(ctx context.Context, env environment.Environment, indexable bool) (PageIndex, error) {
	record, payload, err := s.Active(ctx, env)
	result := PageIndex{URLs: []string{}, Warnings: []string{}}
	if errors.Is(err, ErrSnapshotUnavailable) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	key, err := seoIndexCacheKey(env, record, payload, indexable)
	if err != nil {
		return result, err
	}
	s.seoIndexMu.RLock()
	cached, ok := s.seoIndexCache[env.String()]
	s.seoIndexMu.RUnlock()
	if ok && cached.key == key {
		return clonePageIndex(cached.value), nil
	}
	result, err = s.pageIndexForSnapshot(ctx, env, record, payload, indexable)
	if err != nil {
		return result, err
	}
	currentRecord, currentPayload, err := s.Active(ctx, env)
	if err != nil {
		return result, err
	}
	currentKey, err := seoIndexCacheKey(env, currentRecord, currentPayload, indexable)
	if err != nil {
		return result, err
	}
	if currentKey != key {
		return PageIndex{}, fmt.Errorf("%w: branch state changed while enumerating sitemap", ErrConflict)
	}
	s.seoIndexMu.Lock()
	if s.seoIndexCache == nil {
		s.seoIndexCache = map[string]seoIndexCacheEntry{}
	}
	s.seoIndexCache[env.String()] = seoIndexCacheEntry{key: key, value: clonePageIndex(result)}
	s.seoIndexMu.Unlock()
	return result, nil
}

func seoIndexCacheKey(env environment.Environment, record SnapshotRecord, payload SnapshotPayload, branchIndexable bool) (string, error) {
	canonicalDomain := ""
	noIndex := false
	if payload.Metadata != nil {
		canonicalDomain = payload.Metadata.CanonicalDomain
		noIndex = payload.Metadata.NoIndex
	}
	identity, err := json.Marshal(struct {
		Branch          string    `json:"branch"`
		SnapshotID      uuid.UUID `json:"snapshotId"`
		ContentRevision int64     `json:"contentRevision"`
		Digest          string    `json:"digest"`
		Canonical       string    `json:"canonical"`
		BranchIndexable bool      `json:"branchIndexable"`
		NoIndex         bool      `json:"noIndex"`
	}{env.String(), record.ID, record.ContentRevision, record.Digest, canonicalDomain, branchIndexable, noIndex})
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(identity)
	return hex.EncodeToString(hash[:]), nil
}

func clonePageIndex(source PageIndex) PageIndex {
	return PageIndex{
		CanonicalDomain: source.CanonicalDomain,
		Indexable:       source.Indexable,
		URLs:            append([]string(nil), source.URLs...),
		Warnings:        append([]string(nil), source.Warnings...),
	}
}

func detailRouteFieldValue(record ModelContentRecord, fieldKey string) (string, bool) {
	value, exists := record.Values[fieldKey]
	if !exists || value == nil {
		return "", false
	}
	text := fmt.Sprint(value)
	if strings.TrimSpace(text) == "" || text == "<nil>" {
		return "", false
	}
	return text, true
}

func (s *SnapshotService) pageIndexForSnapshot(ctx context.Context, env environment.Environment, record SnapshotRecord, payload SnapshotPayload, indexable bool) (PageIndex, error) {
	result := PageIndex{URLs: []string{}, Warnings: []string{}}
	if payload.Metadata == nil || payload.Metadata.CanonicalDomain == "" {
		result.Warnings = append(result.Warnings, "Canonical-домен сайта не настроен")
		return result, nil
	}
	result.CanonicalDomain = strings.TrimRight(payload.Metadata.CanonicalDomain, "/")
	result.Indexable = indexable && !payload.Metadata.NoIndex
	if !result.Indexable {
		return result, nil
	}
	candidates, warnings := staticPageRouteCandidates(payload.Config.RouteTree)
	result.Warnings = append(result.Warnings, warnings...)
	var publishedRecords []ModelContentRecord
	var publishedSelection []ModelContentSelection
	if payload.Config.DataModel != nil {
		state, err := readStoredModelContent(ctx, s.pool, record.ID, false)
		if err != nil {
			return result, err
		}
		publishedRecords = state.PublishedRecords
		publishedSelection = state.PublishedSelection
	}
	registry := payloadBuildRegistry(payload.Builds)
	for _, page := range payload.Config.Pages {
		for _, placement := range page.Placements {
			if placement.DetailLink == nil {
				continue
			}
			link := placement.DetailLink
			build, ok := registry.Build(placement.Build.FeatureKey, placement.Build.Version)
			if !ok || payload.Config.DataModel == nil {
				continue
			}
			entityID, fieldID, ok := modelCollectionFieldForDetail(placement, build.Manifest, link.Field)
			if !ok {
				result.Warnings = append(result.Warnings, "Не удалось сопоставить поле маршрута деталей с моделью данных")
				continue
			}
			var entity *DataModelEntity
			for i := range payload.Config.DataModel.Entities {
				if payload.Config.DataModel.Entities[i].ID == entityID {
					entity = &payload.Config.DataModel.Entities[i]
					break
				}
			}
			if entity == nil {
				continue
			}
			field, ok := dataModelFieldByID(*entity, fieldID)
			if !ok {
				continue
			}
			for _, modelRecord := range publishedRecords {
				if modelRecord.EntityID != entityID {
					continue
				}
				value, present := detailRouteFieldValue(modelRecord, field.Key)
				if !present {
					continue
				}
				candidate := strings.ReplaceAll(link.PathTemplate, "{"+link.Field+"}", url.PathEscape(value))
				if strings.ContainsAny(candidate, "{}") {
					result.Warnings = append(result.Warnings, "Не удалось перечислить шаблон "+link.PathTemplate)
					continue
				}
				candidates = append(candidates, pageIndexRouteCandidate{path: candidate, routeID: link.RouteID})
			}
		}
	}
	compiledRoutes, err := CompileRouteTree(payload.Config.RouteTree)
	if err != nil {
		return result, err
	}
	routeIndexes := make(map[string]int, len(compiledRoutes))
	for i, route := range compiledRoutes {
		routeIndexes[route.ID] = i
	}
	seen := map[string]bool{}
	readState := &runtimeReadState{
		modelLoaded:          true,
		modelRecords:         publishedRecords,
		modelSelections:      publishedSelection,
		publicMediaReadiness: make(map[uuid.UUID]bool),
	}
	// System pages are status fallbacks rather than indexable destinations. A
	// concrete route for one (for example /not-found) is still useful when
	// previewing the configured feature, but must never be advertised in the
	// sitemap even when the page has no explicit noindex metadata override.
	systemPages := make(map[string]struct{}, len(payload.Config.SystemPages))
	for _, pageID := range payload.Config.SystemPages {
		if pageID != "" {
			systemPages[pageID] = struct{}{}
		}
	}
	pageIDs := make(map[string]struct{}, len(payload.Config.Pages))
	for _, page := range payload.Config.Pages {
		pageIDs[page.ID] = struct{}{}
	}
	for _, candidate := range candidates {
		if seen[candidate.path] || !strings.HasPrefix(candidate.path, "/") || strings.ContainsAny(candidate.path, "?#") {
			continue
		}
		seen[candidate.path] = true
		decodedPath, decodeErr := url.PathUnescape(candidate.path)
		if decodeErr != nil {
			continue
		}
		route, args, matched, matchErr := matchPageIndexRoute(compiledRoutes, routeIndexes, candidate.routeID, decodedPath)
		if matchErr != nil {
			return result, matchErr
		}
		if !matched {
			continue
		}
		// The compiled route already carries its page ID. Reuse that metadata
		// to reject configured system/noindex pages before runtime composition
		// would scan the page list and resolve their placements. Keep malformed
		// route references on the original composition path to preserve its
		// behavior for snapshots with a missing page definition.
		if _, pageExists := pageIDs[route.PageID]; pageExists {
			if _, isSystemPage := systemPages[route.PageID]; isSystemPage {
				continue
			}
			metadata := payload.Metadata.ForPath(route.PageID, decodedPath, indexable)
			if strings.HasPrefix(metadata.Robots, "noindex") {
				continue
			}
		}
		composition, err := s.runtimeForMatchedRoute(ctx, env, record, payload, route, args, decodedPath, nil, false, "", readState)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return result, err
		}
		if composition.Snapshot.ID != record.ID || composition.Snapshot.ContentRevision != record.ContentRevision {
			return result, fmt.Errorf("%w: branch assignment changed while enumerating URLs", ErrConflict)
		}
		if _, isSystemPage := systemPages[composition.Page.ID]; isSystemPage {
			continue
		}
		metadata := payload.Metadata.ForPath(composition.Page.ID, decodedPath, indexable)
		if strings.HasPrefix(metadata.Robots, "noindex") {
			continue
		}
		result.URLs = append(result.URLs, metadata.CanonicalURL)
	}
	sort.Strings(result.URLs)
	return result, nil
}

type pageIndexRouteCandidate struct {
	path    string
	routeID string
}

func staticPageRouteCandidates(root RouteTreeNode) ([]pageIndexRouteCandidate, []string) {
	paths := []pageIndexRouteCandidate{}
	warnings := []string{}
	var walk func(RouteTreeNode, string, bool)
	walk = func(node RouteTreeNode, prefix string, dynamic bool) {
		switch node.Type {
		case "site":
		case "index":
			prefix = ""
		case "path":
			prefix = node.Path
			dynamic = strings.ContainsAny(node.Path, ":~*")
		case "static":
			prefix = strings.TrimRight(prefix, "/") + "/" + node.Value
		default:
			dynamic = true
		}
		if node.PageID != "" {
			if dynamic {
				warnings = append(warnings, "Динамический маршрут требует ссылки коллекции: "+node.ID)
			} else if prefix == "" {
				paths = append(paths, pageIndexRouteCandidate{path: "/", routeID: node.ID})
			} else {
				paths = append(paths, pageIndexRouteCandidate{path: prefix, routeID: node.ID})
			}
		}
		for _, child := range node.Children {
			walk(child, prefix, dynamic)
		}
	}
	walk(root, "", false)
	return paths, warnings
}

func staticPagePaths(root RouteTreeNode) ([]string, []string) {
	candidates, warnings := staticPageRouteCandidates(root)
	paths := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		paths = append(paths, candidate.path)
	}
	return paths, warnings
}

// matchPageIndexRoute starts at the route known to have contributed this
// concrete sitemap path, but still evaluates every earlier route first. This
// preserves MatchRoute's first-match behavior (including captures and regex
// routes that shadow a static path) while avoiding checks against routes that
// come after the known matching route.
func matchPageIndexRoute(routes []CompiledRoute, routeIndexes map[string]int, hintedRouteID, requestPath string) (Route, map[string]string, bool, error) {
	if len(requestPath) > 4096 {
		return Route{}, nil, false, fmt.Errorf("request path exceeds 4096 characters")
	}
	hintedIndex, ok := routeIndexes[hintedRouteID]
	if hintedRouteID == "" || !ok {
		return MatchRoute(routes, requestPath)
	}
	// Reuse the normal first-match implementation on the prefix through the
	// known route. This keeps its optimized capture handling and preserves
	// earlier dynamic/regex routes that can shadow the candidate.
	matchedRoute, args, matched, err := MatchRoute(routes[:hintedIndex+1], requestPath)
	if err != nil || matched {
		return matchedRoute, args, matched, err
	}
	// A route hint describes where the candidate came from, but malformed
	// snapshots or encoded path values can fail to match it. Fall through to
	// later routes instead of changing MatchRoute semantics.
	return MatchRoute(routes[hintedIndex+1:], requestPath)
}

// MetadataForSnapshot allows the graphical site-settings editor to read the
// immutable configuration pinned by the selected snapshot.
func (s *SnapshotService) MetadataForSnapshot(ctx context.Context, id uuid.UUID) (SiteMetadata, error) {
	_, payload, err := s.snapshotPayload(ctx, id)
	if err != nil {
		return SiteMetadata{}, err
	}
	if payload.Metadata == nil {
		return SiteMetadata{FormatVersion: 1, Pages: map[string]SiteMetadataOverride{}}, nil
	}
	return *payload.Metadata, nil
}
