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
	"strings"

	core "github.com/Zubrium/cdn/core/presentation"
	routesapp "github.com/Zubrium/cdn/engines/router/features/routes/application"
	"github.com/Zubrium/cdn/engines/router/features/routes/model"
	engine "github.com/Zubrium/cdn/engines/router/presentation"
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
)

type RouteResolver interface {
	ResolveRoute(context.Context, string, string) (model.Route, error)
}

type CoreIdentity interface {
	Authenticate(context.Context, string) (core.Session, core.Principal, error)
}

type CoreFiles interface {
	StatFile(context.Context, core.Session, string) (core.File, error)
	ReadFile(context.Context, core.Session, string) (io.ReadCloser, core.File, error)
}

// CoreTargetValidator confirms that Router's configured Core identity can
// read every file referenced by a target before the route becomes active.
type CoreTargetValidator struct {
	identity   CoreIdentity
	files      CoreFiles
	credential string
	operations engine.PublicOperationCatalog
}

func NewCoreTargetValidator(identity CoreIdentity, files CoreFiles, credential string, operations engine.PublicOperationCatalog) (*CoreTargetValidator, error) {
	if isNilAPI(identity) || isNilAPI(files) || credential == "" || strings.ContainsRune(credential, '\x00') {
		return nil, engine.ErrInvalid
	}
	return &CoreTargetValidator{identity: identity, files: files, credential: credential, operations: operations}, nil
}

func (validator *CoreTargetValidator) ValidateRouteTarget(ctx context.Context, target model.Target, match string) error {
	if validator == nil || ctx == nil || target.Validate() != nil {
		return engine.ErrInvalidTarget
	}
	if target.Kind == model.TargetStaticSiteArtifact && target.FallbackEnabled {
		return engine.ErrInvalidTarget
	}
	if target.Kind == model.TargetRegisteredOperation {
		if validator.operations == nil {
			return engine.ErrPublicOperationNotRegistered
		}
		return mapOperationTargetValidationError(validator.operations.ValidatePublicOperationTarget(ctx, target))
	}
	session, err := validator.authenticate(ctx)
	if err != nil {
		return mapCoreAuthError(err)
	}
	if target.Kind == model.TargetCoreFile {
		_, err := validator.files.StatFile(ctx, session, target.FileID)
		return mapTargetCoreError(err)
	}
	manifest, _, err := readArtifactManifest(ctx, validator.files, session, target.FileID)
	if err != nil {
		return mapTargetCoreError(err)
	}
	if len(manifest.assets) > 0 && match != model.MatchPrefix {
		return engine.ErrInvalidTarget
	}
	return validateArtifactFiles(ctx, validator.files, session, target.FileID, manifest)
}

func mapOperationTargetValidationError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	case errors.Is(err, engine.ErrForbidden):
		return routesapp.ErrForbidden
	case errors.Is(err, engine.ErrUnauthorized), errors.Is(err, engine.ErrUnavailable):
		return routesapp.ErrUnavailable
	case errors.Is(err, engine.ErrNotFound):
		return routesapp.ErrNotFound
	default:
		return routesapp.ErrInvalid
	}
}

func (validator *CoreTargetValidator) authenticate(ctx context.Context) (core.Session, error) {
	if validator == nil || validator.identity == nil {
		return core.Session{}, core.ErrUnavailable
	}
	session, _, err := validator.identity.Authenticate(ctx, validator.credential)
	if err != nil {
		return core.Session{}, err
	}
	return session, nil
}

type CoreDelivery struct {
	branchID   string
	credential string
	identity   CoreIdentity
	files      CoreFiles
	routes     RouteResolver
	operations engine.PublicOperationCatalog
}

func NewCoreDelivery(branchID, credential string, identity CoreIdentity, files CoreFiles, routes RouteResolver, operations engine.PublicOperationCatalog) (*CoreDelivery, error) {
	if branchID == "" || len(branchID) > 128 || strings.TrimSpace(branchID) != branchID ||
		credential == "" || strings.ContainsRune(credential, '\x00') || isNilAPI(identity) || isNilAPI(files) || isNilAPI(routes) {
		return nil, engine.ErrInvalid
	}
	for _, char := range branchID {
		if char < 0x21 || char == 0x7f || char == '/' || char == '\\' {
			return nil, engine.ErrInvalid
		}
	}
	return &CoreDelivery{branchID: branchID, credential: credential, identity: identity, files: files, routes: routes, operations: operations}, nil
}

func (delivery *CoreDelivery) Deliver(ctx context.Context, request engine.DeliveryRequest) (engine.DeliveryResponse, error) {
	if delivery == nil || ctx == nil || request.BranchID != delivery.branchID {
		return engine.DeliveryResponse{}, engine.ErrNotFound
	}
	if request.Method != http.MethodGet && request.Method != http.MethodHead && request.Method != http.MethodPost &&
		request.Method != http.MethodPut && request.Method != http.MethodPatch && request.Method != http.MethodDelete {
		return engine.DeliveryResponse{}, engine.ErrInvalid
	}
	route, err := delivery.routes.ResolveRoute(ctx, request.Host, request.EscapedPath)
	if err != nil {
		return engine.DeliveryResponse{}, mapRouteError(err)
	}
	if route.BranchID != delivery.branchID || route.State != model.StateActive || route.ValidateForStorage() != nil {
		return engine.DeliveryResponse{}, engine.ErrNotFound
	}
	host, hostErr := model.NormalizeRequestHost(request.Host)
	path, pathErr := model.NormalizePath(request.EscapedPath)
	if hostErr != nil || pathErr != nil || route.Host != host || !model.PathMatches(route.Path, path, route.Match) {
		return engine.DeliveryResponse{}, engine.ErrNotFound
	}
	switch route.Target.Kind {
	case model.TargetCoreFile:
		if route.Match != model.MatchExact || request.Method != http.MethodGet && request.Method != http.MethodHead || request.ContentLength != 0 {
			return engine.DeliveryResponse{}, engine.ErrNotFound
		}
		return delivery.deliverCoreFile(ctx, route.Target.FileID, request)
	case model.TargetStaticSiteArtifact:
		if request.Method != http.MethodGet && request.Method != http.MethodHead || request.ContentLength != 0 {
			return engine.DeliveryResponse{}, engine.ErrNotFound
		}
		return delivery.deliverArtifact(ctx, route, request)
	case model.TargetRegisteredOperation:
		if delivery.operations == nil {
			return engine.DeliveryResponse{}, engine.ErrUnavailable
		}
		return DispatchPublicOperation(ctx, delivery.operations, route.Target, engine.PublicOperationRequest{
			Method: request.Method, Host: route.Host, EscapedPath: path, Query: request.Query,
			ContentType: request.ContentType, ContentLength: request.ContentLength,
			IdempotencyKey: request.IdempotencyKey, IfNoneMatch: request.IfNoneMatch,
			Body: request.Body, FormSessionTokens: cloneFormSessionTokens(request.FormSessionTokens),
		})
	default:
		return engine.DeliveryResponse{}, engine.ErrNotFound
	}
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

func (delivery *CoreDelivery) deliverCoreFile(ctx context.Context, fileID string, request engine.DeliveryRequest) (engine.DeliveryResponse, error) {
	session, err := delivery.authenticate(ctx)
	if err != nil {
		return engine.DeliveryResponse{}, mapDeliveryAuthError(err)
	}
	var file core.File
	var body io.ReadCloser
	if request.Method == http.MethodHead || request.IfNoneMatch != "" {
		file, err = delivery.files.StatFile(ctx, session, fileID)
		if err != nil {
			return engine.DeliveryResponse{}, mapDeliveryFileError(err)
		}
		etag, etagErr := fileETag(file.SHA256)
		if etagErr != nil {
			return engine.DeliveryResponse{}, engine.ErrUnavailable
		}
		if request.IfNoneMatch != "" && matchesIfNoneMatch(request.IfNoneMatch, etag) {
			return engine.DeliveryResponse{StatusCode: http.StatusNotModified, ContentLength: -1, ETag: etag, CacheControl: "no-store"}, nil
		}
		if request.Method == http.MethodHead {
			contentType, typeErr := normalizedContentType(file.ContentType)
			if typeErr != nil {
				return engine.DeliveryResponse{}, engine.ErrUnavailable
			}
			return engine.DeliveryResponse{StatusCode: http.StatusOK, ContentType: contentType, ContentLength: file.Size,
				ETag: etag, CacheControl: "no-store", ContentDisposition: "attachment"}, nil
		}
	}
	if body == nil {
		body, file, err = delivery.files.ReadFile(ctx, session, fileID)
		if err != nil {
			return engine.DeliveryResponse{}, mapDeliveryFileError(err)
		}
	}
	if body == nil {
		return engine.DeliveryResponse{}, engine.ErrUnavailable
	}
	etag, etagErr := fileETag(file.SHA256)
	if etagErr != nil {
		_ = body.Close()
		return engine.DeliveryResponse{}, engine.ErrUnavailable
	}
	contentType, typeErr := normalizedContentType(file.ContentType)
	if typeErr != nil {
		_ = body.Close()
		return engine.DeliveryResponse{}, engine.ErrUnavailable
	}
	return engine.DeliveryResponse{StatusCode: http.StatusOK, ContentType: contentType, ContentLength: file.Size,
		ETag: etag, CacheControl: "no-store", ContentDisposition: "attachment", Body: body}, nil
}

func (delivery *CoreDelivery) deliverArtifact(ctx context.Context, route model.Route, request engine.DeliveryRequest) (engine.DeliveryResponse, error) {
	if route.Target.FallbackEnabled {
		return engine.DeliveryResponse{}, engine.ErrNotFound
	}
	session, err := delivery.authenticate(ctx)
	if err != nil {
		return engine.DeliveryResponse{}, mapDeliveryAuthError(err)
	}
	manifest, _, err := readArtifactManifest(ctx, delivery.files, session, route.Target.FileID)
	if err != nil {
		return engine.DeliveryResponse{}, mapDeliveryFileError(err)
	}
	path, pathErr := model.NormalizePath(request.EscapedPath)
	if pathErr != nil {
		return engine.DeliveryResponse{}, engine.ErrNotFound
	}
	var entry artifactFile
	cacheControl := "no-store"
	if path == route.Path {
		entry = manifest.document
	} else {
		localPath, ok := artifactRelativePath(route, path)
		if !ok {
			return engine.DeliveryResponse{}, engine.ErrNotFound
		}
		asset, found := manifest.assets[localPath]
		if !found {
			return engine.DeliveryResponse{}, engine.ErrNotFound
		}
		entry = asset.artifactFile
		cacheControl = artifactCacheControl(asset)
	}
	if request.Method == http.MethodHead || request.IfNoneMatch != "" {
		file, statErr := delivery.files.StatFile(ctx, session, entry.FileID)
		if statErr != nil {
			return engine.DeliveryResponse{}, mapDeliveryFileError(statErr)
		}
		if !artifactMetadataMatches(entry, file) {
			return engine.DeliveryResponse{}, engine.ErrUnavailable
		}
		etag, _ := fileETag(entry.SHA256)
		if request.IfNoneMatch != "" && matchesIfNoneMatch(request.IfNoneMatch, etag) {
			return engine.DeliveryResponse{StatusCode: http.StatusNotModified, ContentLength: -1, ETag: etag, CacheControl: cacheControl}, nil
		}
		contentType, _ := normalizedContentType(entry.ContentType)
		if request.Method == http.MethodHead {
			return engine.DeliveryResponse{StatusCode: http.StatusOK, ContentType: contentType, ContentLength: entry.Size,
				ETag: etag, CacheControl: cacheControl}, nil
		}
	}
	body, file, err := delivery.files.ReadFile(ctx, session, entry.FileID)
	if err != nil {
		return engine.DeliveryResponse{}, mapDeliveryFileError(err)
	}
	if body == nil {
		return engine.DeliveryResponse{}, engine.ErrUnavailable
	}
	if !artifactMetadataMatches(entry, file) {
		_ = body.Close()
		return engine.DeliveryResponse{}, engine.ErrUnavailable
	}
	etag, _ := fileETag(entry.SHA256)
	contentType, _ := normalizedContentType(entry.ContentType)
	return engine.DeliveryResponse{StatusCode: http.StatusOK, ContentType: contentType, ContentLength: entry.Size,
		ETag: etag, CacheControl: cacheControl, Body: body}, nil
}

func (delivery *CoreDelivery) authenticate(ctx context.Context) (core.Session, error) {
	session, _, err := delivery.identity.Authenticate(ctx, delivery.credential)
	return session, err
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

func readArtifactManifest(ctx context.Context, files CoreFiles, session core.Session, fileID string) (parsedArtifact, core.File, error) {
	manifestFile, err := files.StatFile(ctx, session, fileID)
	if err != nil {
		return parsedArtifact{}, core.File{}, err
	}
	mediaType, _, err := mime.ParseMediaType(manifestFile.ContentType)
	if err != nil || mediaType != "application/json" || manifestFile.Size < 1 || manifestFile.Size > maxArtifactManifestBytes {
		return parsedArtifact{}, core.File{}, engine.ErrInvalidTarget
	}
	body, actualFile, err := files.ReadFile(ctx, session, fileID)
	if err != nil {
		return parsedArtifact{}, core.File{}, err
	}
	if body == nil {
		return parsedArtifact{}, core.File{}, core.ErrUnavailable
	}
	defer body.Close()
	actualContentType, actualTypeErr := normalizedContentType(actualFile.ContentType)
	manifestContentType, manifestTypeErr := normalizedContentType(manifestFile.ContentType)
	if actualFile.ID != manifestFile.ID || actualFile.Revision != manifestFile.Revision || actualFile.Size != manifestFile.Size ||
		actualFile.SHA256 != manifestFile.SHA256 || actualTypeErr != nil || manifestTypeErr != nil || actualContentType != manifestContentType {
		return parsedArtifact{}, core.File{}, engine.ErrUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(body, maxArtifactManifestBytes+1))
	if err != nil {
		return parsedArtifact{}, core.File{}, err
	}
	if int64(len(data)) != manifestFile.Size || len(data) > maxArtifactManifestBytes {
		return parsedArtifact{}, core.File{}, engine.ErrInvalidTarget
	}
	actualDigest := sha256.Sum256(data)
	if hex.EncodeToString(actualDigest[:]) != manifestFile.SHA256 {
		return parsedArtifact{}, core.File{}, core.ErrUnavailable
	}
	var envelope artifactEnvelope
	if err := httpjson.DecodeBytes(data, &envelope); err != nil || envelope.Format != "static-page-artifact/v1" || envelope.Assets == nil || len(envelope.Assets) > maxArtifactAssets {
		return parsedArtifact{}, core.File{}, engine.ErrInvalidTarget
	}
	var rootFields map[string]json.RawMessage
	if json.Unmarshal(data, &rootFields) != nil || len(rootFields) != 4 || rootFields["format"] == nil || rootFields["digest"] == nil ||
		rootFields["document"] == nil || rootFields["assets"] == nil {
		return parsedArtifact{}, core.File{}, engine.ErrInvalidTarget
	}
	var documentFields map[string]json.RawMessage
	if json.Unmarshal(rootFields["document"], &documentFields) != nil || len(documentFields) != 4 || documentFields["file_id"] == nil ||
		documentFields["content_type"] == nil || documentFields["sha256"] == nil || documentFields["size"] == nil {
		return parsedArtifact{}, core.File{}, engine.ErrInvalidTarget
	}
	var rawAssets map[string]map[string]json.RawMessage
	if json.Unmarshal(rootFields["assets"], &rawAssets) != nil || len(rawAssets) != len(envelope.Assets) {
		return parsedArtifact{}, core.File{}, engine.ErrInvalidTarget
	}
	for _, fields := range rawAssets {
		if (len(fields) != 4 && len(fields) != 5) || fields["file_id"] == nil || fields["content_type"] == nil ||
			fields["sha256"] == nil || fields["size"] == nil {
			return parsedArtifact{}, core.File{}, engine.ErrInvalidTarget
		}
		if len(fields) == 5 && fields["cache_class"] == nil {
			return parsedArtifact{}, core.File{}, engine.ErrInvalidTarget
		}
	}
	expectedDigest, err := artifactPayloadDigest(data)
	if err != nil || envelope.Digest != expectedDigest {
		return parsedArtifact{}, core.File{}, engine.ErrInvalidTarget
	}
	manifest := parsedArtifact{document: envelope.Document, assets: make(map[string]artifactAsset, len(envelope.Assets))}
	fileIDs := make(map[string]struct{}, len(envelope.Assets)+1)
	if err := validateArtifactFile(manifest.document, fileID, true); err != nil {
		return parsedArtifact{}, core.File{}, err
	}
	fileIDs[manifest.document.FileID] = struct{}{}
	for path, entry := range envelope.Assets {
		if err := validateArtifactAssetPath(path); err != nil {
			return parsedArtifact{}, core.File{}, err
		}
		if !allowedArtifactCacheClasses[entry.CacheClass] || validateArtifactFile(entry.artifactFile, fileID, false) != nil {
			return parsedArtifact{}, core.File{}, engine.ErrInvalidTarget
		}
		if _, duplicate := fileIDs[entry.FileID]; duplicate {
			return parsedArtifact{}, core.File{}, engine.ErrInvalidTarget
		}
		fileIDs[entry.FileID] = struct{}{}
		manifest.assets[path] = entry
	}
	return manifest, manifestFile, nil
}

func validateArtifactFile(entry artifactFile, manifestID string, document bool) error {
	if entry.FileID == manifestID || !canonicalUUID(entry.FileID) || entry.Size < 0 || !artifactSHA256Pattern.MatchString(entry.SHA256) {
		return engine.ErrInvalidTarget
	}
	contentType, err := normalizedContentType(entry.ContentType)
	if err != nil || contentType != entry.ContentType || !validArtifactContentType(contentType) {
		return engine.ErrInvalidTarget
	}
	if document {
		mediaType, parameters, parseErr := mime.ParseMediaType(contentType)
		if parseErr != nil || mediaType != "text/html" || len(parameters) > 1 ||
			len(parameters) == 1 && parameters["charset"] != "utf-8" {
			return engine.ErrInvalidTarget
		}
	} else if mediaType, _, _ := mime.ParseMediaType(contentType); mediaType == "text/html" {
		return engine.ErrInvalidTarget
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
		return engine.ErrInvalidTarget
	}
	canonical, err := model.NormalizePath("/" + path)
	if err != nil || canonical != "/"+path {
		return engine.ErrInvalidTarget
	}
	return nil
}

func validateArtifactFiles(ctx context.Context, files CoreFiles, session core.Session, manifestID string, manifest parsedArtifact) error {
	entries := make([]artifactFile, 0, len(manifest.assets)+1)
	entries = append(entries, manifest.document)
	for _, asset := range manifest.assets {
		entries = append(entries, asset.artifactFile)
	}
	for _, entry := range entries {
		file, err := files.StatFile(ctx, session, entry.FileID)
		if err != nil {
			return mapTargetCoreError(err)
		}
		if entry.FileID == manifestID || !artifactMetadataMatches(entry, file) {
			return engine.ErrInvalidTarget
		}
	}
	return nil
}

func artifactMetadataMatches(entry artifactFile, file core.File) bool {
	contentType, err := normalizedContentType(file.ContentType)
	return err == nil && entry.FileID == file.ID && entry.Size == file.Size &&
		entry.SHA256 == file.SHA256 && contentType == entry.ContentType
}

func artifactRelativePath(route model.Route, requestPath string) (string, bool) {
	path, err := model.NormalizePath(requestPath)
	if err != nil || !model.PathMatches(route.Path, path, model.MatchPrefix) || path == route.Path {
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
		return "", engine.ErrInvalidTarget
	}
	mediaType, parameters, err := mime.ParseMediaType(value)
	if err != nil || strings.Contains(mediaType, "*") {
		return "", engine.ErrInvalidTarget
	}
	canonical := mime.FormatMediaType(mediaType, parameters)
	if canonical == "" {
		return "", engine.ErrInvalidTarget
	}
	return canonical, nil
}

func fileETag(checksum string) (string, error) {
	if !artifactSHA256Pattern.MatchString(checksum) {
		return "", engine.ErrUnavailable
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

func mapCoreAuthError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return core.ErrUnavailable
}

func mapDeliveryAuthError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return engine.ErrUnavailable
}

func mapTargetCoreError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, core.ErrForbidden):
		return core.ErrForbidden
	case errors.Is(err, core.ErrFileNotFound):
		return core.ErrFileNotFound
	case errors.Is(err, model.ErrInvalidRoute):
		return engine.ErrInvalidTarget
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	case errors.Is(err, core.ErrUnavailable), errors.Is(err, core.ErrUnauthorized):
		return core.ErrUnavailable
	default:
		return core.ErrUnavailable
	}
}

func mapDeliveryFileError(err error) error {
	switch {
	case errors.Is(err, core.ErrForbidden), errors.Is(err, core.ErrFileNotFound):
		return engine.ErrNotFound
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	default:
		return engine.ErrUnavailable
	}
}

func mapRouteError(err error) error {
	if errors.Is(err, routesapp.ErrNotFound) || errors.Is(err, engine.ErrNotFound) || errors.Is(err, model.ErrInvalidRoute) {
		return engine.ErrNotFound
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return fmt.Errorf("%w: route lookup: %v", engine.ErrUnavailable, err)
}

var _ engine.DeliveryAPI = (*CoreDelivery)(nil)
var _ interface {
	ValidateRouteTarget(context.Context, model.Target, string) error
} = (*CoreTargetValidator)(nil)
