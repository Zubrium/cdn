// Package publicupload implements Router's explicitly configured anonymous
// upload operation on top of Loader. It never reads request cookies or
// accepts storage/group destinations from a visitor.
package publicupload

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
	"os"
	"path/filepath"
	"reflect"
	"strings"

	core "github.com/Zubrium/cdn/core/presentation"
	loader "github.com/Zubrium/cdn/engines/loader/features/files/pkg"
	engine "github.com/Zubrium/cdn/engines/router/presentation"
	"github.com/google/uuid"
)

const OperationID = "public.form-upload"

type Loader interface {
	Authenticate(context.Context, string) (context.Context, error)
	BeginUpload(context.Context, loader.BeginUploadInput) (loader.UploadSession, error)
	PutUploadPart(context.Context, string, int, int64, string, io.Reader) (loader.UploadSession, error)
	CommitUpload(context.Context, string) (loader.File, error)
	GetFileSet(context.Context, string) (loader.FileSet, error)
	AddFileToSet(context.Context, string, string, int64) (loader.FileSet, error)
}

type Config struct {
	Credential           string
	GroupAuthorization   GroupAuthorization
	StagingDirectory     string
	MaxFileBytes         int64
	PartSize             int64
	MaxParts             int
	MaxConcurrentUploads int
	MaxStagedBytes       int64
	AllowedContentTypes  []string
}

// GroupAuthorization checks current Core grants for Router's configured
// service identity. Loader still performs the authoritative write checks for
// each file and FileSet operation.
type GroupAuthorization interface {
	Authenticate(context.Context, string) (core.Session, core.Principal, error)
	RequireGroupLevel(context.Context, core.Session, []string, core.AccessLevel) error
}

type Operation struct {
	loader               Loader
	credential           string
	authorization        GroupAuthorization
	stagingDirectory     string
	maxFileBytes         int64
	partSize             int64
	maxParts             int
	maxConcurrentUploads int
	allowedContentTypes  []string
	slots                chan struct{}
}

type routeConfig struct {
	Enabled             bool     `json:"enabled"`
	AccessGroup         string   `json:"access_group"`
	FileSetID           string   `json:"file_set_id,omitempty"`
	MaxBytes            int64    `json:"max_bytes"`
	AllowedContentTypes []string `json:"allowed_content_types"`
}

type result struct {
	FileID    string `json:"file_id"`
	FileSetID string `json:"file_set_id,omitempty"`
}

func New(api Loader, cfg Config) (*Operation, error) {
	if nilDependency(api) || nilDependency(cfg.GroupAuthorization) || cfg.Credential == "" || strings.ContainsRune(cfg.Credential, '\x00') || cfg.MaxFileBytes <= 0 ||
		cfg.PartSize <= 0 || cfg.MaxParts <= 0 || cfg.MaxParts > 1000 || cfg.MaxConcurrentUploads <= 0 || cfg.MaxConcurrentUploads > 1024 || cfg.MaxStagedBytes <= 0 {
		return nil, errors.New("invalid public upload operation configuration")
	}
	if cfg.MaxFileBytes > cfg.MaxStagedBytes/int64(cfg.MaxConcurrentUploads) {
		return nil, errors.New("public upload concurrency exceeds the staging byte budget")
	}
	partsNeeded := cfg.MaxFileBytes / cfg.PartSize
	if cfg.MaxFileBytes%cfg.PartSize != 0 {
		partsNeeded++
	}
	if partsNeeded > int64(cfg.MaxParts) {
		return nil, errors.New("public upload byte limit exceeds Loader part capacity")
	}
	if err := validateStagingDirectory(cfg.StagingDirectory); err != nil {
		return nil, err
	}
	allowed, err := normalizeAllowedContentTypes(cfg.AllowedContentTypes)
	if err != nil || len(allowed) == 0 {
		return nil, errors.New("public upload operation requires explicit allowed content types")
	}
	return &Operation{
		loader: api, credential: cfg.Credential, authorization: cfg.GroupAuthorization, stagingDirectory: filepath.Clean(cfg.StagingDirectory),
		maxFileBytes: cfg.MaxFileBytes, partSize: cfg.PartSize, maxParts: cfg.MaxParts,
		maxConcurrentUploads: cfg.MaxConcurrentUploads,
		allowedContentTypes:  allowed, slots: make(chan struct{}, cfg.MaxConcurrentUploads),
	}, nil
}

func nilDependency(value any) bool {
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

func validateStagingDirectory(path string) error {
	if path == "" || strings.TrimSpace(path) != path || !filepath.IsAbs(path) {
		return errors.New("public upload staging directory must be configured as an absolute path")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect public upload staging directory: %w", err)
	}
	mode := info.Mode().Perm()
	isSymlink := info.Mode()&os.ModeSymlink != 0
	if !info.IsDir() || isSymlink || mode&0o700 != 0o700 || mode&0o077 != 0 {
		return fmt.Errorf("public upload staging directory must be an owner-only writable non-symlink directory (mode %04o, directory %t, symlink %t)", mode, info.IsDir(), isSymlink)
	}
	return nil
}

func normalizeAllowedContentTypes(values []string) ([]string, error) {
	if len(values) == 0 || len(values) > 32 {
		return nil, errors.New("invalid public upload content-type allowlist")
	}
	allowed := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		mediaType, parameters, err := mime.ParseMediaType(value)
		if err != nil || strings.Contains(mediaType, "*") || len(parameters) != 0 || strings.TrimSpace(value) != value {
			return nil, errors.New("invalid public upload content-type allowlist")
		}
		mediaType = strings.ToLower(mediaType)
		if isActiveContentType(mediaType) {
			return nil, errors.New("active content types cannot be uploaded through a public route")
		}
		if _, exists := seen[mediaType]; exists {
			return nil, errors.New("duplicate public upload content type")
		}
		seen[mediaType] = struct{}{}
		allowed = append(allowed, mediaType)
	}
	return allowed, nil
}

func isActiveContentType(mediaType string) bool {
	switch mediaType {
	case "application/ecmascript", "application/javascript", "application/x-httpd-php", "application/xhtml+xml",
		"application/xml", "image/svg+xml", "text/css", "text/html", "text/javascript", "text/xml":
		return true
	default:
		return false
	}
}

func (operation *Operation) Descriptor() engine.PublicOperationDescriptor {
	if operation == nil {
		return engine.PublicOperationDescriptor{}
	}
	return engine.PublicOperationDescriptor{
		ID: OperationID, ContractVersion: 1, Methods: []string{"POST"},
		MaxRequestBytes:     operation.maxFileBytes,
		AllowedContentTypes: append([]string(nil), operation.allowedContentTypes...),
	}
}

func (operation *Operation) ValidateRouteConfig(raw json.RawMessage) error {
	if operation == nil {
		return engine.ErrInvalidPublicOperation
	}
	config, err := decodeRouteConfig(raw)
	if err != nil {
		return err
	}
	return operation.validateRouteConfig(config)
}

// ValidateRouteConfigWithContext additionally confirms that Router's
// configured service identity can write the target group and, when present,
// the FileSet management group before the route is stored.
func (operation *Operation) ValidateRouteConfigWithContext(ctx context.Context, raw json.RawMessage) error {
	if operation == nil || ctx == nil {
		return engine.ErrUnavailable
	}
	config, err := decodeRouteConfig(raw)
	if err != nil || operation.validateRouteConfig(config) != nil {
		return engine.ErrInvalidTarget
	}
	_, err = operation.authorizeRoute(ctx, config)
	return err
}

func (operation *Operation) authorizeRoute(ctx context.Context, config routeConfig) (context.Context, error) {
	session, principal, err := operation.authorization.Authenticate(ctx, operation.credential)
	if err != nil {
		return nil, mapCoreAuthorizationError(err)
	}
	if !principal.Active {
		return nil, engine.ErrUnauthorized
	}
	if err := operation.authorization.RequireGroupLevel(ctx, session, []string{config.AccessGroup}, core.AccessLevelWrite); err != nil {
		return nil, mapCoreAuthorizationError(err)
	}
	loaderCtx, err := operation.loader.Authenticate(ctx, operation.credential)
	if err != nil || loaderCtx == nil {
		return nil, mapLoaderAuthorizationError(err)
	}
	if config.FileSetID == "" {
		return loaderCtx, nil
	}
	set, err := operation.loader.GetFileSet(loaderCtx, config.FileSetID)
	if err != nil || set.ID != config.FileSetID || !validGroup(set.ManagementGroup) {
		if errors.Is(err, loader.ErrNotFound) || (err == nil && (set.ID != config.FileSetID || !validGroup(set.ManagementGroup))) {
			return nil, engine.ErrNotFound
		}
		return nil, mapLoaderAuthorizationError(err)
	}
	if err := operation.authorization.RequireGroupLevel(ctx, session, []string{set.ManagementGroup}, core.AccessLevelWrite); err != nil {
		return nil, mapCoreAuthorizationError(err)
	}
	return loaderCtx, nil
}

func decodeRouteConfig(raw json.RawMessage) (routeConfig, error) {
	if len(raw) == 0 || len(raw) > 64<<10 || !validUniqueJSON(raw) {
		return routeConfig{}, engine.ErrInvalidTarget
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var config routeConfig
	if err := decoder.Decode(&config); err != nil {
		return routeConfig{}, engine.ErrInvalidTarget
	}
	var extra any
	if !errors.Is(decoder.Decode(&extra), io.EOF) {
		return routeConfig{}, engine.ErrInvalidTarget
	}
	if config.FileSetID != "" {
		id, err := uuid.Parse(config.FileSetID)
		if err != nil || id == uuid.Nil {
			return routeConfig{}, engine.ErrInvalidTarget
		}
		config.FileSetID = id.String()
	}
	return config, nil
}

func validUniqueJSON(raw []byte) bool {
	if !json.Valid(raw) {
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := consumeJSONValue(decoder, 0); err != nil {
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

func (operation *Operation) validateRouteConfig(config routeConfig) error {
	if !config.Enabled || !validGroup(config.AccessGroup) || config.MaxBytes <= 0 || config.MaxBytes > operation.maxFileBytes {
		return engine.ErrInvalidTarget
	}
	if config.FileSetID != "" {
		id, err := uuid.Parse(config.FileSetID)
		if err != nil || id == uuid.Nil {
			return engine.ErrInvalidTarget
		}
	}
	allowed, err := normalizeAllowedContentTypes(config.AllowedContentTypes)
	if err != nil {
		return engine.ErrInvalidTarget
	}
	for _, mediaType := range allowed {
		if !contains(operation.allowedContentTypes, mediaType) {
			return engine.ErrInvalidTarget
		}
	}
	return nil
}

func validGroup(value string) bool {
	if len(value) == 0 || len(value) > 64 || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for i := 1; i < len(value); i++ {
		char := value[i]
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '_' && char != '-' {
			return false
		}
	}
	return true
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func (operation *Operation) Execute(ctx context.Context, raw json.RawMessage, request engine.PublicOperationRequest) (engine.DeliveryResponse, error) {
	if operation == nil || ctx == nil {
		return engine.DeliveryResponse{}, engine.ErrUnavailable
	}
	config, err := decodeRouteConfig(raw)
	if err != nil || operation.validateRouteConfig(config) != nil {
		return engine.DeliveryResponse{}, engine.ErrInvalidTarget
	}
	config.AllowedContentTypes, _ = normalizeAllowedContentTypes(config.AllowedContentTypes)
	mediaType, _, err := mime.ParseMediaType(request.ContentType)
	if request.Method != "POST" || err != nil || !contains(config.AllowedContentTypes, strings.ToLower(mediaType)) ||
		request.ContentLength < -1 || request.ContentLength > config.MaxBytes || !loader.ValidIdempotencyKey(request.IdempotencyKey) ||
		request.Host == "" || len(request.Host) > 512 || strings.ContainsAny(request.Host, "\r\n") || len(request.EscapedPath) > 8192 {
		if request.ContentLength > config.MaxBytes {
			return engine.DeliveryResponse{}, engine.ErrTooLarge
		}
		return engine.DeliveryResponse{}, engine.ErrInvalid
	}
	if request.Body == nil {
		return engine.DeliveryResponse{}, engine.ErrInvalid
	}
	loaderCtx, err := operation.authorizeRoute(ctx, config)
	if err != nil {
		if errors.Is(err, engine.ErrForbidden) || errors.Is(err, engine.ErrUnauthorized) {
			return engine.DeliveryResponse{}, engine.ErrUnavailable
		}
		return engine.DeliveryResponse{}, err
	}
	select {
	case operation.slots <- struct{}{}:
		defer func() { <-operation.slots }()
	case <-ctx.Done():
		return engine.DeliveryResponse{}, ctx.Err()
	}
	staged, size, checksum, err := operation.stage(ctx, request.Body, config.MaxBytes)
	if err != nil {
		return engine.DeliveryResponse{}, err
	}
	defer func() {
		_ = staged.Close()
		_ = os.Remove(staged.Name())
	}()
	if request.ContentLength >= 0 && size != request.ContentLength {
		return engine.DeliveryResponse{}, engine.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return engine.DeliveryResponse{}, err
	}

	key := scopedIdempotencyKey(request, config)
	name := "public-upload-" + key[:32]
	session, err := operation.loader.BeginUpload(loaderCtx, loader.BeginUploadInput{
		IdempotencyKey: key, Name: name, ContentType: strings.ToLower(mediaType), Size: size, SHA256: checksum,
		Groups: []string{config.AccessGroup}, Attributes: map[string]string{"source": "router-public-upload"},
	})
	if err != nil || !validUUID(session.ID) || session.PartSize != operation.partSize {
		return engine.DeliveryResponse{}, mapLoaderError(err)
	}
	if session.State == "uploading" {
		if err := operation.uploadParts(loaderCtx, staged, session, size); err != nil {
			return engine.DeliveryResponse{}, err
		}
	} else if session.State != "committing" && session.State != "committed" {
		return engine.DeliveryResponse{}, engine.ErrUnavailable
	}
	file, err := operation.loader.CommitUpload(loaderCtx, session.ID)
	if err != nil || !validUUID(file.ID) {
		return engine.DeliveryResponse{}, mapLoaderError(err)
	}
	if config.FileSetID != "" {
		if err := operation.addToFileSet(loaderCtx, config.FileSetID, file.ID); err != nil {
			return engine.DeliveryResponse{}, err
		}
	}

	body, err := json.Marshal(result{FileID: file.ID, FileSetID: config.FileSetID})
	if err != nil {
		return engine.DeliveryResponse{}, engine.ErrUnavailable
	}
	return engine.DeliveryResponse{
		StatusCode: 201, ContentType: "application/json; charset=utf-8", ContentLength: int64(len(body)),
		CacheControl: "no-store", Body: io.NopCloser(bytes.NewReader(body)),
	}, nil
}

func (operation *Operation) stage(ctx context.Context, body io.Reader, maxBytes int64) (*os.File, int64, string, error) {
	file, err := os.CreateTemp(operation.stagingDirectory, "public-upload-*")
	if err != nil {
		return nil, 0, "", engine.ErrUnavailable
	}
	removeOnError := true
	defer func() {
		if removeOnError {
			_ = file.Close()
			_ = os.Remove(file.Name())
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		return nil, 0, "", engine.ErrUnavailable
	}
	sha := sha256.New()
	count, err := copyBounded(ctx, io.MultiWriter(file, sha), body, maxBytes)
	if err != nil {
		return nil, 0, "", err
	}
	if err := file.Sync(); err != nil {
		return nil, 0, "", engine.ErrUnavailable
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, 0, "", engine.ErrUnavailable
	}
	removeOnError = false
	return file, count, hex.EncodeToString(sha.Sum(nil)), nil
}

func copyBounded(ctx context.Context, destination io.Writer, source io.Reader, maxBytes int64) (int64, error) {
	buffer := make([]byte, 32*1024)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		read, readErr := source.Read(buffer)
		if read > 0 {
			if total > maxBytes-int64(read) {
				return total, engine.ErrTooLarge
			}
			written, writeErr := destination.Write(buffer[:read])
			if writeErr != nil {
				return total, engine.ErrUnavailable
			}
			if written != read {
				return total, engine.ErrUnavailable
			}
			total += int64(read)
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return total, nil
			}
			if errors.Is(readErr, engine.ErrTooLarge) {
				return total, engine.ErrTooLarge
			}
			return total, engine.ErrUnavailable
		}
		if read == 0 {
			return total, io.ErrNoProgress
		}
	}
}

func (operation *Operation) uploadParts(ctx context.Context, file *os.File, session loader.UploadSession, total int64) error {
	if total == 0 {
		return nil
	}
	partCount := 1 + (total-1)/session.PartSize
	if partCount > int64(operation.maxParts) {
		return engine.ErrTooLarge
	}
	for part := int64(0); part < partCount; part++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		offset := part * session.PartSize
		size := min(session.PartSize, total-offset)
		hasher := sha256.New()
		read, err := io.CopyN(hasher, io.NewSectionReader(file, offset, size), size)
		if err != nil || read != size {
			return engine.ErrUnavailable
		}
		_, err = operation.loader.PutUploadPart(ctx, session.ID, int(part+1), size, hex.EncodeToString(hasher.Sum(nil)), io.NewSectionReader(file, offset, size))
		if err != nil {
			return mapLoaderError(err)
		}
	}
	return nil
}

func (operation *Operation) addToFileSet(ctx context.Context, setID, fileID string) error {
	for attempt := 0; attempt < 3; attempt++ {
		set, err := operation.loader.GetFileSet(ctx, setID)
		if err != nil || set.ID != setID || set.Revision <= 0 {
			return engine.ErrUnavailable
		}
		_, err = operation.loader.AddFileToSet(ctx, setID, fileID, set.Revision)
		if err == nil {
			return nil
		}
		if !errors.Is(err, loader.ErrConflict) || ctx.Err() != nil {
			return engine.ErrUnavailable
		}
	}
	return engine.ErrUnavailable
}

func scopedIdempotencyKey(request engine.PublicOperationRequest, config routeConfig) string {
	value := strings.Join([]string{OperationID, request.Method, strings.ToLower(request.Host), request.EscapedPath,
		config.AccessGroup, config.FileSetID, request.IdempotencyKey}, "\x00")
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func validUUID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil
}

func mapLoaderError(err error) error {
	if errors.Is(err, loader.ErrUnauthorized) || errors.Is(err, loader.ErrForbidden) {
		return engine.ErrUnavailable
	}
	if errors.Is(err, loader.ErrConflict) {
		return engine.ErrConflict
	}
	if errors.Is(err, loader.ErrTooLarge) {
		return engine.ErrTooLarge
	}
	if errors.Is(err, loader.ErrInvalid) {
		return engine.ErrInvalid
	}
	return engine.ErrUnavailable
}

func mapCoreAuthorizationError(err error) error {
	switch {
	case errors.Is(err, core.ErrForbidden):
		return engine.ErrForbidden
	case errors.Is(err, core.ErrUnauthorized):
		return engine.ErrUnauthorized
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	default:
		return engine.ErrUnavailable
	}
}

func mapLoaderAuthorizationError(err error) error {
	switch {
	case errors.Is(err, loader.ErrUnauthorized):
		return engine.ErrUnauthorized
	case errors.Is(err, loader.ErrForbidden):
		return engine.ErrForbidden
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	default:
		return engine.ErrUnavailable
	}
}

var _ engine.PublicOperation = (*Operation)(nil)
