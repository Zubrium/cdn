package router

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Zubrium/cdn/pkg/contracts"
	httpjson "github.com/Zubrium/cdn/pkg/json"
	"github.com/google/uuid"
)

const (
	maxArtifactManifestBytes = 1 << 20
	maxArtifactAssets        = 4096
)

var (
	artifactSHA256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
	// An omitted cache_class is equivalent to "none" (no-store) in both modules.
	allowedArtifactCacheClasses = map[string]bool{"": true, "none": true, "short": true, "immutable": true}
	// Only these Core file media types may be served inline. HTML, SVG and
	// unknown types stay attachment so a configured value cannot turn a file
	// route into an executable document.
	allowedInlineFileMediaTypes = map[string]bool{
		"image/jpeg":      true,
		"image/png":       true,
		"image/webp":      true,
		"image/avif":      true,
		"image/gif":       true,
		"video/mp4":       true,
		"video/webm":      true,
		"audio/mpeg":      true,
		"application/pdf": true,
	}
)

type RouteResolver interface {
	ResolveRoute(context.Context, string, string) (contracts.Route, error)
}

type CoreFiles interface {
	StatFile(context.Context, string) (contracts.File, error)
	ReadFile(context.Context, string) (io.ReadCloser, contracts.File, error)
}

// CoreTargetValidator confirms that Router's configured Core identity can
// read every file referenced by a target before the route becomes active.
type CoreTargetValidator struct {
	files      CoreFiles
	operations contracts.PublicOperationCatalog
}

func NewCoreTargetValidator(files CoreFiles, operations contracts.PublicOperationCatalog) (*CoreTargetValidator, error) {
	if isNilAPI(files) {
		return nil, contracts.ErrPublicInvalid
	}
	return &CoreTargetValidator{files: files, operations: operations}, nil
}

func (validator *CoreTargetValidator) ValidateRouteTarget(ctx context.Context, target contracts.Target, match string) error {
	if validator == nil || ctx == nil || target.Validate() != nil {
		return contracts.ErrInvalidTarget
	}
	if target.Kind == contracts.TargetStaticSiteArtifact && target.FallbackEnabled {
		return contracts.ErrInvalidTarget
	}
	if target.Kind == contracts.TargetRegisteredOperation {
		if validator.operations == nil {
			return contracts.ErrPublicOperationNotRegistered
		}
		return mapOperationTargetValidationError(validator.operations.ValidatePublicOperationTarget(ctx, target))
	}
	if target.Kind == contracts.TargetCoreFile {
		_, err := validator.files.StatFile(ctx, target.FileID)
		return mapTargetCoreError(err)
	}
	for _, slot := range target.LocaleSlots() {
		manifest, _, err := readArtifactManifest(ctx, validator.files, slot.ArtifactManifestFileID)
		if err != nil {
			return mapTargetCoreError(err)
		}
		if len(manifest.assets) > 0 && match != contracts.MatchPrefix {
			return contracts.ErrInvalidTarget
		}
		if err := validateArtifactFiles(ctx, validator.files, slot.ArtifactManifestFileID, manifest); err != nil {
			return err
		}
	}
	return nil
}

func mapOperationTargetValidationError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	case errors.Is(err, contracts.ErrPublicForbidden):
		return contracts.ErrPublicForbidden
	case errors.Is(err, contracts.ErrPublicUnauthorized), errors.Is(err, contracts.ErrPublicUnavailable):
		return contracts.ErrPublicUnavailable
	case errors.Is(err, contracts.ErrPublicNotFound):
		return contracts.ErrPublicNotFound
	default:
		return contracts.ErrPublicInvalid
	}
}

type CoreDelivery struct {
	branchID   string
	files      CoreFiles
	routes     RouteResolver
	operations contracts.PublicOperationCatalog
	manifests  *manifestCache
	routeCache *routeCache
	observer   CacheObserver
}

func NewCoreDelivery(branchID string, files CoreFiles, routes RouteResolver, operations contracts.PublicOperationCatalog) (*CoreDelivery, error) {
	return newCoreDelivery(branchID, files, routes, operations, nil, nil, nil)
}

// NewCoreDeliveryWithCache enables both bounded Router caches. Every bound is
// mandatory: composition must pass explicit startup configuration, and an
// unavailable revision source never falls back to a stale cached match.
func NewCoreDeliveryWithCache(branchID string, files CoreFiles, routes RouteResolver, operations contracts.PublicOperationCatalog, config RouterCacheConfig) (*CoreDelivery, error) {
	if !validCacheSettings(config.Manifests) || !validCacheSettings(config.Routes) || config.Observer == nil {
		return nil, contracts.ErrPublicInvalid
	}
	fingerprints, ok := routes.(RouteFingerprintSource)
	if !ok || isNilAPI(fingerprints) {
		return nil, contracts.ErrPublicInvalid
	}
	return newCoreDelivery(branchID, files, routes, operations,
		newManifestCache(files, config.Manifests, config.Observer),
		newRouteCache(branchID, routes, fingerprints, config.Routes, config.Observer), config.Observer)
}

func newCoreDelivery(branchID string, files CoreFiles, routes RouteResolver, operations contracts.PublicOperationCatalog,
	manifests *manifestCache, routeCache *routeCache, observer CacheObserver) (*CoreDelivery, error) {
	if branchID == "" || len(branchID) > 128 || strings.TrimSpace(branchID) != branchID ||
		isNilAPI(files) || isNilAPI(routes) {
		return nil, contracts.ErrPublicInvalid
	}
	for _, char := range branchID {
		if char < 0x21 || char == 0x7f || char == '/' || char == '\\' {
			return nil, contracts.ErrPublicInvalid
		}
	}
	delivery := &CoreDelivery{
		branchID: branchID, files: files, routes: routes, operations: operations,
		manifests: manifests, routeCache: routeCache, observer: observer,
	}
	if observer != nil {
		delivery.initCacheMetrics()
	}
	return delivery, nil
}

func (delivery *CoreDelivery) Deliver(ctx context.Context, request contracts.DeliveryRequest) (contracts.DeliveryResponse, error) {
	if delivery == nil || ctx == nil || request.BranchID != delivery.branchID {
		return contracts.DeliveryResponse{}, contracts.ErrPublicNotFound
	}
	if request.Method != http.MethodGet && request.Method != http.MethodHead && request.Method != http.MethodPost &&
		request.Method != http.MethodPut && request.Method != http.MethodPatch && request.Method != http.MethodDelete {
		return contracts.DeliveryResponse{}, contracts.ErrPublicInvalid
	}
	host, hostErr := contracts.NormalizeRequestHost(request.Host)
	path, pathErr := contracts.NormalizePath(request.EscapedPath)
	if hostErr != nil || pathErr != nil {
		return contracts.DeliveryResponse{}, contracts.ErrPublicNotFound
	}
	route, err := delivery.resolveRoute(ctx, request, host, path)
	if err != nil {
		return contracts.DeliveryResponse{}, mapRouteError(err)
	}
	if route.BranchID != delivery.branchID || route.State != contracts.StateActive || route.ValidateForStorage() != nil {
		return contracts.DeliveryResponse{}, contracts.ErrPublicNotFound
	}
	if route.Host != host || !contracts.PathMatches(route.Path, path, route.Match) {
		return contracts.DeliveryResponse{}, contracts.ErrPublicNotFound
	}
	switch route.Target.Kind {
	case contracts.TargetCoreFile:
		if route.Match != contracts.MatchExact || request.Method != http.MethodGet && request.Method != http.MethodHead || request.ContentLength != 0 {
			return contracts.DeliveryResponse{}, contracts.ErrPublicNotFound
		}
		return delivery.deliverCoreFile(ctx, route.Target.FileID, route.Target.Disposition, request)
	case contracts.TargetStaticSiteArtifact:
		if request.Method != http.MethodGet && request.Method != http.MethodHead || request.ContentLength != 0 {
			return contracts.DeliveryResponse{}, contracts.ErrPublicNotFound
		}
		return delivery.deliverArtifact(ctx, route, request)
	case contracts.TargetRegisteredOperation:
		if delivery.operations == nil {
			return contracts.DeliveryResponse{}, contracts.ErrPublicUnavailable
		}
		return DispatchPublicOperation(ctx, delivery.operations, route.Target, contracts.PublicOperationRequest{
			Method: request.Method, Host: route.Host, EscapedPath: path, Query: request.Query,
			ContentType: request.ContentType, ContentLength: request.ContentLength,
			IdempotencyKey: request.IdempotencyKey, IfNoneMatch: request.IfNoneMatch,
			Body: request.Body, FormSessionTokens: cloneFormSessionTokens(request.FormSessionTokens),
			Streams: request.Streams,
		})
	default:
		return contracts.DeliveryResponse{}, contracts.ErrPublicNotFound
	}
}

func (delivery *CoreDelivery) resolveRoute(ctx context.Context, request contracts.DeliveryRequest, host, path string) (contracts.Route, error) {
	if delivery.routeCache == nil {
		return delivery.routes.ResolveRoute(ctx, request.Host, request.EscapedPath)
	}
	key := routeCacheKey{branch: delivery.branchID, host: host, path: path, method: request.Method}
	return delivery.routeCache.resolve(ctx, key, request.Host, request.EscapedPath)
}

func cloneFormSessionTokens(source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}
	clone := make(map[string]string, len(source))
	for formID, token := range source {
		clone[formID] = token
	}
	return clone
}

// dispositionForFile is fail-closed: only an allowlisted Core media type can
// become inline, and an explicit attachment always wins.
func dispositionForFile(contentType, configured string) string {
	if configured == contracts.DispositionAttachment {
		return "attachment"
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil || !allowedInlineFileMediaTypes[strings.ToLower(mediaType)] {
		return "attachment"
	}
	return "inline"
}

func (delivery *CoreDelivery) deliverCoreFile(ctx context.Context, fileID, disposition string, request contracts.DeliveryRequest) (contracts.DeliveryResponse, error) {
	var file contracts.File
	var body io.ReadCloser
	var err error
	if request.Method == http.MethodHead || request.IfNoneMatch != "" {
		file, err = delivery.files.StatFile(ctx, fileID)
		if err != nil {
			return contracts.DeliveryResponse{}, mapDeliveryFileError(err)
		}
		etag, etagErr := fileETag(file.SHA256)
		if etagErr != nil {
			return contracts.DeliveryResponse{}, contracts.ErrPublicUnavailable
		}
		if request.IfNoneMatch != "" && matchesIfNoneMatch(request.IfNoneMatch, etag) {
			return contracts.DeliveryResponse{StatusCode: http.StatusNotModified, ContentLength: -1, ETag: etag, CacheControl: "no-store",
				ContentDisposition: dispositionForFile(file.ContentType, disposition)}, nil
		}
		if request.Method == http.MethodHead {
			contentType, typeErr := normalizedContentType(file.ContentType)
			if typeErr != nil {
				return contracts.DeliveryResponse{}, contracts.ErrPublicUnavailable
			}
			return contracts.DeliveryResponse{StatusCode: http.StatusOK, ContentType: contentType, ContentLength: file.Size,
				ETag: etag, CacheControl: "no-store", ContentDisposition: dispositionForFile(file.ContentType, disposition)}, nil
		}
	}
	if body == nil {
		body, file, err = delivery.files.ReadFile(ctx, fileID)
		if err != nil {
			return contracts.DeliveryResponse{}, mapDeliveryFileError(err)
		}
	}
	if body == nil {
		return contracts.DeliveryResponse{}, contracts.ErrPublicUnavailable
	}
	etag, etagErr := fileETag(file.SHA256)
	if etagErr != nil {
		_ = body.Close()
		return contracts.DeliveryResponse{}, contracts.ErrPublicUnavailable
	}
	contentType, typeErr := normalizedContentType(file.ContentType)
	if typeErr != nil {
		_ = body.Close()
		return contracts.DeliveryResponse{}, contracts.ErrPublicUnavailable
	}
	return contracts.DeliveryResponse{StatusCode: http.StatusOK, ContentType: contentType, ContentLength: file.Size,
		ETag: etag, CacheControl: "no-store", ContentDisposition: dispositionForFile(file.ContentType, disposition), Body: body}, nil
}

type languageRange struct {
	value string
	order int
	q     float64
}

type localeSource uint8

const (
	localeDefault localeSource = iota
	localeFromCookie
	localeFromAcceptLanguage
)

// selectArtifactFileID resolves the server-owned locale catalog in the order
// declared by the Router contract: a supported preference cookie, then RFC 4647
// lookup over Accept-Language, then the configured default locale. Path and
// query never participate. The source is returned so a cookie-selected
// representation can be marked no-store and an Accept-Language representation
// can carry an explicit Vary.
func selectArtifactFileID(target contracts.Target, request contracts.DeliveryRequest) (string, localeSource, error) {
	supported := make(map[string]string, len(target.LocaleTargets))
	for _, item := range target.LocaleTargets {
		supported[item.Locale] = item.ArtifactManifestFileID
	}
	if request.LocalePreference != "" {
		if locale, err := contracts.CanonicalLocale(request.LocalePreference); err == nil {
			if fileID, found := supported[locale]; found {
				return fileID, localeFromCookie, nil
			}
		}
	}
	ranges := parseAcceptLanguage(request.AcceptLanguage)
	for _, item := range ranges {
		if item.value == "*" {
			break
		}
		candidate := item.value
		for {
			if locale, err := contracts.CanonicalLocale(candidate); err == nil {
				if fileID, found := supported[locale]; found {
					return fileID, localeFromAcceptLanguage, nil
				}
			}
			separator := strings.LastIndex(candidate, "-")
			if separator <= 0 {
				break
			}
			candidate = candidate[:separator]
		}
	}
	fileID, err := target.DefaultArtifactFileID()
	return fileID, localeDefault, err
}

func parseAcceptLanguage(header string) []languageRange {
	if strings.TrimSpace(header) == "" {
		return nil
	}
	parts := strings.Split(header, ",")
	if len(parts) > 64 {
		parts = parts[:64]
	}
	ranges := make([]languageRange, 0, len(parts))
	for order, part := range parts {
		fields := strings.Split(part, ";")
		value := strings.TrimSpace(fields[0])
		if value == "" || len(value) > 64 {
			continue
		}
		quality := 1.0
		valid := true
		for _, parameter := range fields[1:] {
			name, raw, found := strings.Cut(strings.TrimSpace(parameter), "=")
			if !found || !strings.EqualFold(strings.TrimSpace(name), "q") {
				valid = false
				break
			}
			parsed, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
			if err != nil || parsed < 0 || parsed > 1 {
				valid = false
				break
			}
			quality = parsed
		}
		if !valid || quality == 0 {
			continue
		}
		ranges = append(ranges, languageRange{value: value, order: order, q: quality})
	}
	sort.SliceStable(ranges, func(i, j int) bool { return ranges[i].q > ranges[j].q })
	return ranges
}

func (delivery *CoreDelivery) deliverArtifact(ctx context.Context, route contracts.Route, request contracts.DeliveryRequest) (contracts.DeliveryResponse, error) {
	if route.Target.FallbackEnabled {
		return contracts.DeliveryResponse{}, contracts.ErrPublicNotFound
	}
	artifactFileID, source, err := selectArtifactFileID(route.Target, request)
	if err != nil {
		return contracts.DeliveryResponse{}, contracts.ErrPublicNotFound
	}
	manifest, _, err := delivery.readManifest(ctx, artifactFileID)
	if err != nil {
		return contracts.DeliveryResponse{}, mapDeliveryFileError(err)
	}
	path, pathErr := contracts.NormalizePath(request.EscapedPath)
	if pathErr != nil {
		return contracts.DeliveryResponse{}, contracts.ErrPublicNotFound
	}
	var entry artifactFile
	cacheControl := "no-store"
	if path == route.Path {
		entry = manifest.document
	} else {
		localPath, ok := artifactRelativePath(route, path)
		if !ok {
			return contracts.DeliveryResponse{}, contracts.ErrPublicNotFound
		}
		asset, found := manifest.assets[localPath]
		if !found {
			return contracts.DeliveryResponse{}, contracts.ErrPublicNotFound
		}
		entry = asset.artifactFile
		cacheControl = artifactCacheControl(asset)
	}
	var vary []string
	switch source {
	case localeFromCookie:
		// Vary: Cookie would make every private cookie a shared-cache key.
		cacheControl = "no-store"
	case localeFromAcceptLanguage:
		vary = append(vary, "Accept-Language")
	}
	if request.Method == http.MethodHead || request.IfNoneMatch != "" {
		file, statErr := delivery.files.StatFile(ctx, entry.FileID)
		if statErr != nil {
			return contracts.DeliveryResponse{}, mapDeliveryFileError(statErr)
		}
		if !artifactMetadataMatches(entry, file) {
			return contracts.DeliveryResponse{}, contracts.ErrPublicUnavailable
		}
		etag, _ := fileETag(entry.SHA256)
		if request.IfNoneMatch != "" && matchesIfNoneMatch(request.IfNoneMatch, etag) {
			return contracts.DeliveryResponse{StatusCode: http.StatusNotModified, ContentLength: -1, ETag: etag,
				CacheControl: cacheControl, Vary: vary}, nil
		}
		contentType, _ := normalizedContentType(entry.ContentType)
		if request.Method == http.MethodHead {
			return contracts.DeliveryResponse{StatusCode: http.StatusOK, ContentType: contentType, ContentLength: entry.Size,
				ETag: etag, CacheControl: cacheControl, Vary: vary}, nil
		}
	}
	body, file, err := delivery.files.ReadFile(ctx, entry.FileID)
	if err != nil {
		return contracts.DeliveryResponse{}, mapDeliveryFileError(err)
	}
	if body == nil {
		return contracts.DeliveryResponse{}, contracts.ErrPublicUnavailable
	}
	if !artifactMetadataMatches(entry, file) {
		_ = body.Close()
		return contracts.DeliveryResponse{}, contracts.ErrPublicUnavailable
	}
	etag, _ := fileETag(entry.SHA256)
	contentType, _ := normalizedContentType(entry.ContentType)
	return contracts.DeliveryResponse{StatusCode: http.StatusOK, ContentType: contentType, ContentLength: entry.Size,
		ETag: etag, CacheControl: cacheControl, Vary: vary, Body: body}, nil
}

func (delivery *CoreDelivery) readManifest(ctx context.Context, fileID string) (parsedArtifact, contracts.File, error) {
	if delivery.manifests == nil {
		return readArtifactManifest(ctx, delivery.files, fileID)
	}
	return delivery.manifests.read(ctx, fileID)
}

// Struct field order follows the canonical JSON key order so a payload marshalled
// from this struct is byte-identical to the canonical payload computed from raw
// manifest bytes. Do not reorder these fields without updating the conformance test.
type artifactFile struct {
	ContentType string `json:"content_type"`
	FileID      string `json:"file_id"`
	SHA256      string `json:"sha256"`
	Size        int64  `json:"size"`
}

type artifactAsset struct {
	CacheClass string `json:"cache_class"`
	artifactFile
}

type artifactPayload struct {
	Assets   map[string]artifactAsset `json:"assets"`
	Document artifactFile             `json:"document"`
	Format   string                   `json:"format"`
}

type artifactEnvelope struct {
	Format   string                   `json:"format"`
	Digest   string                   `json:"digest"`
	Document artifactFile             `json:"document"`
	Assets   map[string]artifactAsset `json:"assets"`
}

type parsedArtifact struct {
	document artifactFile
	assets   map[string]artifactAsset
}

func readArtifactManifest(ctx context.Context, files CoreFiles, fileID string) (parsedArtifact, contracts.File, error) {
	manifestFile, err := files.StatFile(ctx, fileID)
	if err != nil {
		return parsedArtifact{}, contracts.File{}, err
	}
	mediaType, _, err := mime.ParseMediaType(manifestFile.ContentType)
	if err != nil || mediaType != "application/json" || manifestFile.Size < 1 || manifestFile.Size > maxArtifactManifestBytes {
		return parsedArtifact{}, contracts.File{}, contracts.ErrInvalidTarget
	}
	body, actualFile, err := files.ReadFile(ctx, fileID)
	if err != nil {
		return parsedArtifact{}, contracts.File{}, err
	}
	if body == nil {
		return parsedArtifact{}, contracts.File{}, contracts.ErrPublicUnavailable
	}
	defer body.Close()
	actualContentType, actualTypeErr := normalizedContentType(actualFile.ContentType)
	manifestContentType, manifestTypeErr := normalizedContentType(manifestFile.ContentType)
	if actualFile.ID != manifestFile.ID || actualFile.Revision != manifestFile.Revision || actualFile.Size != manifestFile.Size ||
		actualFile.SHA256 != manifestFile.SHA256 || actualTypeErr != nil || manifestTypeErr != nil || actualContentType != manifestContentType {
		return parsedArtifact{}, contracts.File{}, contracts.ErrPublicUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(body, maxArtifactManifestBytes+1))
	if err != nil {
		return parsedArtifact{}, contracts.File{}, err
	}
	if int64(len(data)) != manifestFile.Size || len(data) > maxArtifactManifestBytes {
		return parsedArtifact{}, contracts.File{}, contracts.ErrInvalidTarget
	}
	actualDigest := sha256.Sum256(data)
	if hex.EncodeToString(actualDigest[:]) != manifestFile.SHA256 {
		return parsedArtifact{}, contracts.File{}, contracts.ErrPublicUnavailable
	}
	var envelope artifactEnvelope
	if err := httpjson.DecodeBytes(data, &envelope); err != nil || envelope.Format != "static-page-artifact/v1" || envelope.Assets == nil || len(envelope.Assets) > maxArtifactAssets {
		return parsedArtifact{}, contracts.File{}, contracts.ErrInvalidTarget
	}
	var rootFields map[string]json.RawMessage
	if json.Unmarshal(data, &rootFields) != nil || len(rootFields) != 4 || rootFields["format"] == nil || rootFields["digest"] == nil ||
		rootFields["document"] == nil || rootFields["assets"] == nil {
		return parsedArtifact{}, contracts.File{}, contracts.ErrInvalidTarget
	}
	var documentFields map[string]json.RawMessage
	if json.Unmarshal(rootFields["document"], &documentFields) != nil || len(documentFields) != 4 || documentFields["file_id"] == nil ||
		documentFields["content_type"] == nil || documentFields["sha256"] == nil || documentFields["size"] == nil {
		return parsedArtifact{}, contracts.File{}, contracts.ErrInvalidTarget
	}
	var rawAssets map[string]map[string]json.RawMessage
	if json.Unmarshal(rootFields["assets"], &rawAssets) != nil || len(rawAssets) != len(envelope.Assets) {
		return parsedArtifact{}, contracts.File{}, contracts.ErrInvalidTarget
	}
	for _, fields := range rawAssets {
		if (len(fields) != 4 && len(fields) != 5) || fields["file_id"] == nil || fields["content_type"] == nil ||
			fields["sha256"] == nil || fields["size"] == nil {
			return parsedArtifact{}, contracts.File{}, contracts.ErrInvalidTarget
		}
		if len(fields) == 5 && fields["cache_class"] == nil {
			return parsedArtifact{}, contracts.File{}, contracts.ErrInvalidTarget
		}
	}
	expectedDigest, err := artifactPayloadDigest(data)
	if err != nil || envelope.Digest != expectedDigest {
		return parsedArtifact{}, contracts.File{}, contracts.ErrInvalidTarget
	}
	manifest := parsedArtifact{document: envelope.Document, assets: make(map[string]artifactAsset, len(envelope.Assets))}
	fileIDs := make(map[string]struct{}, len(envelope.Assets)+1)
	if err := validateArtifactFile(manifest.document, fileID, true); err != nil {
		return parsedArtifact{}, contracts.File{}, err
	}
	fileIDs[manifest.document.FileID] = struct{}{}
	for path, entry := range envelope.Assets {
		if err := validateArtifactAssetPath(path); err != nil {
			return parsedArtifact{}, contracts.File{}, err
		}
		if !allowedArtifactCacheClasses[entry.CacheClass] || validateArtifactFile(entry.artifactFile, fileID, false) != nil {
			return parsedArtifact{}, contracts.File{}, contracts.ErrInvalidTarget
		}
		if _, duplicate := fileIDs[entry.FileID]; duplicate {
			return parsedArtifact{}, contracts.File{}, contracts.ErrInvalidTarget
		}
		fileIDs[entry.FileID] = struct{}{}
		manifest.assets[path] = entry
	}
	return manifest, manifestFile, nil
}

func validateArtifactFile(entry artifactFile, manifestID string, document bool) error {
	if entry.FileID == manifestID || !canonicalUUID(entry.FileID) || entry.Size < 0 || !artifactSHA256Pattern.MatchString(entry.SHA256) {
		return contracts.ErrInvalidTarget
	}
	contentType, err := normalizedContentType(entry.ContentType)
	if err != nil || contentType != entry.ContentType || !validArtifactContentType(contentType) {
		return contracts.ErrInvalidTarget
	}
	if document {
		mediaType, parameters, parseErr := mime.ParseMediaType(contentType)
		if parseErr != nil || mediaType != "text/html" || len(parameters) > 1 ||
			len(parameters) == 1 && parameters["charset"] != "utf-8" {
			return contracts.ErrInvalidTarget
		}
	} else if mediaType, _, _ := mime.ParseMediaType(contentType); mediaType == "text/html" {
		return contracts.ErrInvalidTarget
	}
	entry.ContentType = contentType
	return nil
}

func validArtifactContentType(value string) bool {
	mediaType, parameters, err := mime.ParseMediaType(value)
	if err != nil {
		return false
	}
	switch mediaType {
	case "text/html", "text/css", "text/javascript", "application/javascript", "application/json",
		"image/jpeg", "image/png", "image/webp", "image/avif", "image/gif",
		"font/woff", "font/woff2", "application/font-woff":
	default:
		return false
	}
	if len(parameters) == 0 {
		return true
	}
	return len(parameters) == 1 && parameters["charset"] == "utf-8" &&
		(mediaType == "text/html" || mediaType == "text/css" || mediaType == "text/javascript" || mediaType == "application/javascript")
}

func artifactCacheControl(asset artifactAsset) string {
	switch asset.CacheClass {
	case "immutable":
		return "public, max-age=31536000, immutable"
	case "short":
		return "public, max-age=300"
	default:
		return "no-store"
	}
}

func validateArtifactAssetPath(path string) error {
	if path == "" || strings.HasPrefix(path, "/") || strings.HasSuffix(path, "/") || strings.ContainsRune(path, '\\') {
		return contracts.ErrInvalidTarget
	}
	canonical, err := contracts.NormalizePath("/" + path)
	if err != nil || canonical != "/"+path {
		return contracts.ErrInvalidTarget
	}
	return nil
}

func validateArtifactFiles(ctx context.Context, files CoreFiles, manifestID string, manifest parsedArtifact) error {
	entries := make([]artifactFile, 0, len(manifest.assets)+1)
	entries = append(entries, manifest.document)
	for _, asset := range manifest.assets {
		entries = append(entries, asset.artifactFile)
	}
	for _, entry := range entries {
		file, err := files.StatFile(ctx, entry.FileID)
		if err != nil {
			return mapTargetCoreError(err)
		}
		if entry.FileID == manifestID || !artifactMetadataMatches(entry, file) {
			return contracts.ErrInvalidTarget
		}
	}
	return nil
}

func artifactMetadataMatches(entry artifactFile, file contracts.File) bool {
	contentType, err := normalizedContentType(file.ContentType)
	return err == nil && entry.FileID == file.ID && entry.Size == file.Size &&
		entry.SHA256 == file.SHA256 && contentType == entry.ContentType
}

func artifactRelativePath(route contracts.Route, requestPath string) (string, bool) {
	path, err := contracts.NormalizePath(requestPath)
	if err != nil || !contracts.PathMatches(route.Path, path, contracts.MatchPrefix) || path == route.Path {
		return "", false
	}
	base := strings.TrimSuffix(route.Path, "/")
	if base == "" {
		base = "/"
	}
	var relative string
	if base == "/" {
		relative = strings.TrimPrefix(path, "/")
	} else {
		relative = strings.TrimPrefix(path, base+"/")
	}
	if validateArtifactAssetPath(relative) != nil {
		return "", false
	}
	return relative, true
}

func canonicalUUID(value string) bool {
	parsed, err := uuid.Parse(value)
	return err == nil && parsed != uuid.Nil && parsed.String() == value
}

func normalizedContentType(value string) (string, error) {
	if value == "" || len(value) > 256 || strings.ContainsAny(value, "\r\n") {
		return "", contracts.ErrInvalidTarget
	}
	mediaType, parameters, err := mime.ParseMediaType(value)
	if err != nil || strings.Contains(mediaType, "*") {
		return "", contracts.ErrInvalidTarget
	}
	canonical := mime.FormatMediaType(mediaType, parameters)
	if canonical == "" {
		return "", contracts.ErrInvalidTarget
	}
	return canonical, nil
}

func fileETag(checksum string) (string, error) {
	if !artifactSHA256Pattern.MatchString(checksum) {
		return "", contracts.ErrPublicUnavailable
	}
	return `"sha256-` + checksum + `"`, nil
}

func matchesIfNoneMatch(value, etag string) bool {
	for _, candidate := range strings.Split(value, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || strings.TrimPrefix(candidate, "W/") == etag {
			return true
		}
	}
	return false
}

func mapTargetCoreError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, contracts.ErrFileForbidden):
		return contracts.ErrPublicForbidden
	case errors.Is(err, contracts.ErrFileNotFound):
		return contracts.ErrPublicNotFound
	case errors.Is(err, contracts.ErrInvalidRoute):
		return contracts.ErrInvalidTarget
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	default:
		return contracts.ErrPublicUnavailable
	}
}

func mapDeliveryFileError(err error) error {
	switch {
	case errors.Is(err, contracts.ErrFileForbidden), errors.Is(err, contracts.ErrFileNotFound):
		return contracts.ErrPublicNotFound
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	default:
		return contracts.ErrPublicUnavailable
	}
}

func mapRouteError(err error) error {
	if errors.Is(err, contracts.ErrPublicNotFound) || errors.Is(err, contracts.ErrPublicNotFound) || errors.Is(err, contracts.ErrInvalidRoute) {
		return contracts.ErrPublicNotFound
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return fmt.Errorf("%w: route lookup: %v", contracts.ErrPublicUnavailable, err)
}

var _ contracts.DeliveryAPI = (*CoreDelivery)(nil)
var _ interface {
	ValidateRouteTarget(context.Context, contracts.Target, string) error
} = (*CoreTargetValidator)(nil)
