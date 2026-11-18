package router

import (
	"bytes"
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
	"github.com/google/uuid"
)

const (
	maxArtifactManifestBytes = 1 << 20
	maxArtifactFiles         = 4096
	maxArtifactEntrypoints   = 1024
)

var artifactSHA256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

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

func (validator *CoreTargetValidator) ValidateRouteTarget(ctx context.Context, target model.Target) error {
	if validator == nil || ctx == nil || target.Validate() != nil {
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
	if target.FallbackEnabled && manifest.fallback == nil {
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
			IdempotencyKey: request.IdempotencyKey, Body: request.Body,
		})
	default:
		return engine.DeliveryResponse{}, engine.ErrNotFound
	}
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
	session, err := delivery.authenticate(ctx)
	if err != nil {
		return engine.DeliveryResponse{}, mapDeliveryAuthError(err)
	}
	manifest, _, err := readArtifactManifest(ctx, delivery.files, session, route.Target.FileID)
	if err != nil {
		return engine.DeliveryResponse{}, mapDeliveryFileError(err)
	}
	if route.Target.FallbackEnabled && manifest.fallback == nil {
		return engine.DeliveryResponse{}, engine.ErrUnavailable
	}
	localPath, ok := artifactRelativePath(route, request.EscapedPath)
	if !ok {
		return engine.DeliveryResponse{}, engine.ErrNotFound
	}
	filePath, found := manifest.entrypoints[localPath]
	if !found {
		_, found = manifest.files[localPath]
		if found {
			filePath = localPath
		}
	}
	if !found && route.Target.FallbackEnabled && manifest.fallback != nil {
		filePath, found = *manifest.fallback, true
	}
	if !found {
		return engine.DeliveryResponse{}, engine.ErrNotFound
	}
	entry := manifest.files[filePath]
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
			return engine.DeliveryResponse{StatusCode: http.StatusNotModified, ContentLength: -1, ETag: etag, CacheControl: "no-store"}, nil
		}
		contentType, _ := normalizedContentType(entry.ContentType)
		if request.Method == http.MethodHead {
			return engine.DeliveryResponse{StatusCode: http.StatusOK, ContentType: contentType, ContentLength: entry.Size,
				ETag: etag, CacheControl: "no-store"}, nil
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
		ETag: etag, CacheControl: "no-store", Body: body}, nil
}

func (delivery *CoreDelivery) authenticate(ctx context.Context) (core.Session, error) {
	session, _, err := delivery.identity.Authenticate(ctx, delivery.credential)
	return session, err
}

type artifactFile struct {
	FileID      string `json:"file_id"`
	ContentType string `json:"content_type"`
	SHA256      string `json:"sha256"`
	Size        int64  `json:"size"`
	CacheClass  string `json:"cache_class"`
}

type artifactPayload struct {
	Format      string                  `json:"format"`
	Entrypoints map[string]string       `json:"entrypoints"`
	Files       map[string]artifactFile `json:"files"`
	Fallback    *string                 `json:"fallback"`
}

type artifactEnvelope struct {
	Format      string                  `json:"format"`
	Digest      string                  `json:"digest"`
	Entrypoints map[string]string       `json:"entrypoints"`
	Files       map[string]artifactFile `json:"files"`
	Fallback    *string                 `json:"fallback"`
}

type parsedArtifact struct {
	entrypoints map[string]string
	files       map[string]artifactFile
	fallback    *string
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
	if actualFile.ID != manifestFile.ID || actualFile.Revision != manifestFile.Revision || actualFile.Size != manifestFile.Size || actualFile.SHA256 != manifestFile.SHA256 {
		return parsedArtifact{}, core.File{}, engine.ErrUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(body, maxArtifactManifestBytes+1))
	if err != nil {
		return parsedArtifact{}, core.File{}, err
	}
	if int64(len(data)) != manifestFile.Size || len(data) > maxArtifactManifestBytes || !validUniqueJSON(data) {
		return parsedArtifact{}, core.File{}, engine.ErrInvalidTarget
	}
	actualDigest := sha256.Sum256(data)
	if hex.EncodeToString(actualDigest[:]) != manifestFile.SHA256 {
		return parsedArtifact{}, core.File{}, core.ErrUnavailable
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var envelope artifactEnvelope
	if err := decoder.Decode(&envelope); err != nil {
		return parsedArtifact{}, core.File{}, engine.ErrInvalidTarget
	}
	if envelope.Format != "static-site-artifact/v1" || envelope.Entrypoints == nil || envelope.Files == nil ||
		len(envelope.Files) == 0 || len(envelope.Files) > maxArtifactFiles || len(envelope.Entrypoints) == 0 || len(envelope.Entrypoints) > maxArtifactEntrypoints {
		return parsedArtifact{}, core.File{}, engine.ErrInvalidTarget
	}
	rootFields := make(map[string]json.RawMessage)
	if json.Unmarshal(data, &rootFields) != nil || rootFields["format"] == nil || rootFields["digest"] == nil ||
		rootFields["entrypoints"] == nil || rootFields["files"] == nil || rootFields["fallback"] == nil {
		return parsedArtifact{}, core.File{}, engine.ErrInvalidTarget
	}
	var rawFiles map[string]map[string]json.RawMessage
	if json.Unmarshal(rootFields["files"], &rawFiles) != nil || len(rawFiles) != len(envelope.Files) {
		return parsedArtifact{}, core.File{}, engine.ErrInvalidTarget
	}
	for _, fields := range rawFiles {
		if len(fields) != 5 || fields["file_id"] == nil || fields["content_type"] == nil || fields["sha256"] == nil || fields["size"] == nil || fields["cache_class"] == nil {
			return parsedArtifact{}, core.File{}, engine.ErrInvalidTarget
		}
	}
	payload := artifactPayload{Format: envelope.Format, Entrypoints: envelope.Entrypoints, Files: envelope.Files, Fallback: envelope.Fallback}
	canonical, err := json.Marshal(payload)
	if err != nil || !strings.HasPrefix(envelope.Digest, "sha256:") || !artifactSHA256Pattern.MatchString(strings.TrimPrefix(envelope.Digest, "sha256:")) {
		return parsedArtifact{}, core.File{}, engine.ErrInvalidTarget
	}
	digest := sha256.Sum256(canonical)
	if hex.EncodeToString(digest[:]) != strings.TrimPrefix(envelope.Digest, "sha256:") {
		return parsedArtifact{}, core.File{}, engine.ErrInvalidTarget
	}
	manifest := parsedArtifact{entrypoints: make(map[string]string, len(envelope.Entrypoints)), files: make(map[string]artifactFile, len(envelope.Files)), fallback: envelope.Fallback}
	fileIDs := make(map[string]struct{}, len(envelope.Files))
	for path, entry := range envelope.Files {
		canonicalPath, pathErr := model.NormalizePath(path)
		if pathErr != nil || canonicalPath != path || entry.FileID == fileID || !canonicalUUID(entry.FileID) ||
			entry.Size < 0 || !artifactSHA256Pattern.MatchString(entry.SHA256) ||
			(entry.CacheClass != "document" && entry.CacheClass != "immutable") {
			return parsedArtifact{}, core.File{}, engine.ErrInvalidTarget
		}
		if _, duplicate := fileIDs[entry.FileID]; duplicate {
			return parsedArtifact{}, core.File{}, engine.ErrInvalidTarget
		}
		fileIDs[entry.FileID] = struct{}{}
		entry.ContentType, err = normalizedContentType(entry.ContentType)
		if err != nil {
			return parsedArtifact{}, core.File{}, engine.ErrInvalidTarget
		}
		manifest.files[path] = entry
	}
	for path, destination := range envelope.Entrypoints {
		canonicalPath, pathErr := model.NormalizePath(path)
		canonicalDestination, destinationErr := model.NormalizePath(destination)
		entry, exists := manifest.files[destination]
		mediaType, _, typeErr := mime.ParseMediaType(entry.ContentType)
		if pathErr != nil || destinationErr != nil || canonicalPath != path || canonicalDestination != destination || !exists || typeErr != nil || mediaType != "text/html" {
			return parsedArtifact{}, core.File{}, engine.ErrInvalidTarget
		}
		manifest.entrypoints[path] = destination
	}
	if envelope.Fallback != nil {
		canonicalFallback, pathErr := model.NormalizePath(*envelope.Fallback)
		entry, exists := manifest.files[*envelope.Fallback]
		mediaType, _, typeErr := mime.ParseMediaType(entry.ContentType)
		if pathErr != nil || canonicalFallback != *envelope.Fallback || !exists || typeErr != nil || mediaType != "text/html" {
			return parsedArtifact{}, core.File{}, engine.ErrInvalidTarget
		}
	}
	return manifest, manifestFile, nil
}

func validateArtifactFiles(ctx context.Context, files CoreFiles, session core.Session, manifestID string, manifest parsedArtifact) error {
	for _, entry := range manifest.files {
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
		strings.EqualFold(entry.SHA256, file.SHA256) && contentType == entry.ContentType
}

func artifactRelativePath(route model.Route, requestPath string) (string, bool) {
	path, err := model.NormalizePath(requestPath)
	if err != nil || !model.PathMatches(route.Path, path, route.Match) {
		return "", false
	}
	if route.Path == "/" {
		return path, true
	}
	local := strings.TrimPrefix(path, route.Path)
	if local == "" || local == "/" {
		return "/", true
	}
	if local[0] != '/' {
		local = "/" + local
	}
	canonical, err := model.NormalizePath(local)
	return canonical, err == nil
}

func validUniqueJSON(raw []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if consumeJSONValue(decoder, 0) != nil {
		return false
	}
	var extra any
	return errors.Is(decoder.Decode(&extra), io.EOF)
}

func consumeJSONValue(decoder *json.Decoder, depth int) error {
	if depth > 32 {
		return engine.ErrInvalidTarget
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, composite := token.(json.Delim)
	if !composite {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return engine.ErrInvalidTarget
			}
			if _, exists := seen[key]; exists {
				return engine.ErrInvalidTarget
			}
			seen[key] = struct{}{}
			if err := consumeJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
		closeToken, err := decoder.Token()
		if err != nil || closeToken != json.Delim('}') {
			return engine.ErrInvalidTarget
		}
	case '[':
		for decoder.More() {
			if err := consumeJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
		closeToken, err := decoder.Token()
		if err != nil || closeToken != json.Delim(']') {
			return engine.ErrInvalidTarget
		}
	default:
		return engine.ErrInvalidTarget
	}
	return nil
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
	ValidateRouteTarget(context.Context, model.Target) error
} = (*CoreTargetValidator)(nil)
