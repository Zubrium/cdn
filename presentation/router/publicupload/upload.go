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
	"net/http"
	"net/url"
	"reflect"
	"strings"

	"github.com/Zubrium/cdn/pkg/contracts"
	httpjson "github.com/Zubrium/cdn/pkg/json"
	"github.com/Zubrium/cdn/presentation/router/publicforms"
	"github.com/google/uuid"
)

const OperationID = "public.form-upload"

// Loader and Forms are process-local DataBus clients. Router never receives an
// engine facade, credential, module ID or storage destination from the request.
type Loader = contracts.PublicLoaderClient
type Forms = contracts.PublicFormsVisitor

type Config struct {
	Credential           string
	GroupAuthorization   GroupAuthorization
	MaxFileBytes         int64
	PartSize             int64
	MaxParts             int
	MaxConcurrentUploads int
	AllowedContentTypes  []string
	Forms                Forms
}

// GroupAuthorization checks current Core grants for Router's configured
// service identity. Loader still performs the authoritative write checks for
// each file and FileSet operation.
type GroupAuthorization interface {
	Authenticate(context.Context, string) (contracts.AuthorizationSubject, contracts.Principal, error)
	RequireGroupLevel(context.Context, contracts.AuthorizationSubject, []string, contracts.AccessLevel) error
}

type Operation struct {
	loader               Loader
	credential           string
	authorization        GroupAuthorization
	maxFileBytes         int64
	partSize             int64
	maxParts             int
	maxConcurrentUploads int
	allowedContentTypes  []string
	forms                Forms
	slots                chan struct{}
}

type routeConfig struct {
	Enabled             bool                       `json:"enabled"`
	AccessGroup         string                     `json:"access_group"`
	FileSetID           string                     `json:"file_set_id,omitempty"`
	MaxBytes            int64                      `json:"max_bytes"`
	AllowedContentTypes []string                   `json:"allowed_content_types"`
	Form                *publicforms.BindingConfig `json:"form,omitempty"`
}

type result struct {
	FileID    string `json:"file_id"`
	FileSetID string `json:"file_set_id,omitempty"`
}

type formResult struct {
	AttachmentID string `json:"attachment_id"`
	FieldID      string `json:"field_id"`
}

func New(api Loader, cfg Config) (*Operation, error) {
	if nilDependency(api) || nilDependency(cfg.GroupAuthorization) || cfg.Credential == "" || strings.ContainsRune(cfg.Credential, '\x00') || cfg.MaxFileBytes <= 0 ||
		cfg.PartSize <= 0 || cfg.MaxParts <= 0 || cfg.MaxParts > 1000 || cfg.MaxConcurrentUploads <= 0 || cfg.MaxConcurrentUploads > 1024 {
		return nil, errors.New("invalid public upload operation configuration")
	}
	partsNeeded := cfg.MaxFileBytes / cfg.PartSize
	if cfg.MaxFileBytes%cfg.PartSize != 0 {
		partsNeeded++
	}
	if partsNeeded > int64(cfg.MaxParts) {
		return nil, errors.New("public upload byte limit exceeds Loader part capacity")
	}
	allowed, err := normalizeAllowedContentTypes(cfg.AllowedContentTypes)
	if err != nil || len(allowed) == 0 {
		return nil, errors.New("public upload operation requires explicit allowed content types")
	}
	return &Operation{
		loader: api, credential: cfg.Credential, authorization: cfg.GroupAuthorization,
		maxFileBytes: cfg.MaxFileBytes, partSize: cfg.PartSize, maxParts: cfg.MaxParts,
		maxConcurrentUploads: cfg.MaxConcurrentUploads,
		allowedContentTypes:  allowed, forms: cfg.Forms, slots: make(chan struct{}, cfg.MaxConcurrentUploads),
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

func (operation *Operation) Descriptor() contracts.PublicOperationDescriptor {
	if operation == nil {
		return contracts.PublicOperationDescriptor{}
	}
	return contracts.PublicOperationDescriptor{
		ID: OperationID, ContractVersion: 1, Methods: []string{"POST"},
		MaxRequestBytes:     operation.maxFileBytes,
		AllowedContentTypes: append([]string(nil), operation.allowedContentTypes...),
	}
}

func (operation *Operation) ValidateRouteConfig(raw json.RawMessage) error {
	if operation == nil {
		return contracts.ErrPublicInvalidOperation
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
		return contracts.ErrPublicUnavailable
	}
	config, err := decodeRouteConfig(raw)
	if err != nil || operation.validateRouteConfig(config) != nil {
		return contracts.ErrPublicInvalidTarget
	}
	if err = operation.authorizeRoute(ctx, config); err != nil {
		return err
	}
	if config.Form != nil {
		if operation.forms == nil || config.FileSetID == "" {
			return contracts.ErrPublicInvalidTarget
		}
		allowed, _ := normalizeAllowedContentTypes(config.AllowedContentTypes)
		if err := operation.forms.ValidatePublicUploadRoute(ctx, contracts.PublicUploadRouteBinding{
			PublicFormBinding: config.Form.Binding(), AccessGroup: config.AccessGroup,
			MaxBytes: config.MaxBytes, AllowedContentTypes: allowed,
		}); err != nil {
			return publicforms.MapFormsError(err)
		}
	}
	return nil
}

func (operation *Operation) authorizeRoute(ctx context.Context, config routeConfig) error {
	session, principal, err := operation.authorization.Authenticate(ctx, operation.credential)
	if err != nil {
		return mapCoreAuthorizationError(err)
	}
	if !principal.Active {
		return contracts.ErrPublicUnauthorized
	}
	if err := operation.authorization.RequireGroupLevel(ctx, session, []string{config.AccessGroup}, contracts.AccessLevelWrite); err != nil {
		return mapCoreAuthorizationError(err)
	}
	if config.FileSetID == "" {
		return nil
	}
	set, err := operation.loader.GetFileSet(ctx, contracts.PublicLoaderGetFileSetRequest{FileSetID: config.FileSetID})
	if err != nil || set.ID != config.FileSetID || !validGroup(set.ManagementGroup) {
		if errors.Is(err, contracts.ErrPublicNotFound) || (err == nil && (set.ID != config.FileSetID || !validGroup(set.ManagementGroup))) {
			return contracts.ErrPublicNotFound
		}
		return mapPublicError(err)
	}
	if err := operation.authorization.RequireGroupLevel(ctx, session, []string{set.ManagementGroup}, contracts.AccessLevelWrite); err != nil {
		return mapCoreAuthorizationError(err)
	}
	return nil
}

func decodeRouteConfig(raw json.RawMessage) (routeConfig, error) {
	if len(raw) == 0 || len(raw) > 64<<10 {
		return routeConfig{}, contracts.ErrPublicInvalidTarget
	}
	var config routeConfig
	if err := httpjson.DecodeBytes(raw, &config); err != nil {
		return routeConfig{}, contracts.ErrPublicInvalidTarget
	}
	if config.FileSetID != "" {
		id, err := uuid.Parse(config.FileSetID)
		if err != nil || id == uuid.Nil {
			return routeConfig{}, contracts.ErrPublicInvalidTarget
		}
		config.FileSetID = id.String()
	}
	if config.Form != nil {
		if config.Form.Validate() != nil || config.FileSetID == "" {
			return routeConfig{}, contracts.ErrPublicInvalidTarget
		}
	}
	return config, nil
}

func (operation *Operation) validateRouteConfig(config routeConfig) error {
	if !config.Enabled || !validGroup(config.AccessGroup) || config.MaxBytes <= 0 || config.MaxBytes > operation.maxFileBytes {
		return contracts.ErrPublicInvalidTarget
	}
	if config.FileSetID != "" {
		id, err := uuid.Parse(config.FileSetID)
		if err != nil || id == uuid.Nil {
			return contracts.ErrPublicInvalidTarget
		}
	}
	if config.Form != nil && (config.Form.Validate() != nil || config.FileSetID == "" || operation.forms == nil) {
		return contracts.ErrPublicInvalidTarget
	}
	allowed, err := normalizeAllowedContentTypes(config.AllowedContentTypes)
	if err != nil {
		return contracts.ErrPublicInvalidTarget
	}
	for _, mediaType := range allowed {
		if !contains(operation.allowedContentTypes, mediaType) {
			return contracts.ErrPublicInvalidTarget
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

func (operation *Operation) Execute(ctx context.Context, raw json.RawMessage, request contracts.PublicOperationRequest) (contracts.DeliveryResponse, error) {
	if operation == nil || ctx == nil {
		return contracts.DeliveryResponse{}, contracts.ErrPublicUnavailable
	}
	config, err := decodeRouteConfig(raw)
	if err != nil || operation.validateRouteConfig(config) != nil {
		return contracts.DeliveryResponse{}, contracts.ErrPublicInvalidTarget
	}
	config.AllowedContentTypes, _ = normalizeAllowedContentTypes(config.AllowedContentTypes)
	mediaType, _, err := mime.ParseMediaType(request.ContentType)
	stream, hasStream := contracts.PublicStreamReference(request.Body)
	if request.Method != "POST" || err != nil || !contains(config.AllowedContentTypes, strings.ToLower(mediaType)) ||
		request.ContentLength < -1 || request.ContentLength > config.MaxBytes || !contracts.ValidPublicIdempotencyKey(request.IdempotencyKey) ||
		request.Host == "" || len(request.Host) > 512 || strings.ContainsAny(request.Host, "\r\n") || len(request.EscapedPath) > 8192 ||
		!hasStream || request.Streams == nil {
		if request.ContentLength > config.MaxBytes {
			return contracts.DeliveryResponse{}, contracts.ErrPublicTooLarge
		}
		return contracts.DeliveryResponse{}, contracts.ErrPublicInvalid
	}
	fieldID := ""
	var formBinding *publicforms.BindingConfig
	var formToken string
	var reservation contracts.PublicVisitorUploadReservation
	if config.Form == nil {
		if request.Query != "" {
			return contracts.DeliveryResponse{}, contracts.ErrPublicInvalid
		}
	} else {
		fieldID, err = parseFieldQuery(request.Query)
		if err != nil {
			return contracts.DeliveryResponse{}, err
		}
		formBinding = config.Form
		formToken = request.FormSessionTokens[formBinding.FormID]
		if formToken == "" {
			return publicforms.ErrorResponse(contracts.ErrPublicNotFound, formBinding.FormID, false)
		}
	}
	if err := operation.authorizeRoute(ctx, config); err != nil {
		if errors.Is(err, contracts.ErrPublicForbidden) || errors.Is(err, contracts.ErrPublicUnauthorized) {
			return contracts.DeliveryResponse{}, contracts.ErrPublicUnavailable
		}
		return contracts.DeliveryResponse{}, err
	}
	select {
	case operation.slots <- struct{}{}:
		defer func() { <-operation.slots }()
	case <-ctx.Done():
		return contracts.DeliveryResponse{}, ctx.Err()
	}
	if formBinding != nil {
		reservation, err = operation.forms.BeginVisitorUpload(ctx, contracts.PublicBeginVisitorUploadRequest{
			PublicFormBinding: formBinding.Binding(), Token: formToken, FieldID: fieldID,
			IdempotencyKey: scopedFormIdempotencyKey(request, config, fieldID), ContentType: strings.ToLower(mediaType),
			DeclaredSize: request.ContentLength, RouteMaxBytes: config.MaxBytes,
			RouteContentTypes: append([]string(nil), config.AllowedContentTypes...), AccessGroup: config.AccessGroup,
		})
		if err != nil {
			return publicforms.ErrorResponse(err, formBinding.FormID, errors.Is(err, contracts.ErrPublicExpired) || errors.Is(err, contracts.ErrPublicNotFound))
		}
		if !validUUID(reservation.ID) || reservation.FieldID != fieldID || reservation.MaxBytes <= 0 || reservation.MaxBytes > config.MaxBytes {
			return contracts.DeliveryResponse{}, contracts.ErrPublicUnavailable
		}
	}
	maxBytes := config.MaxBytes
	if formBinding != nil {
		maxBytes = reservation.MaxBytes
	}
	key := scopedIdempotencyKey(request, config)
	name := "public-upload-" + key[:32]
	attributes := map[string]string{"source": "router-public-upload"}
	if formBinding != nil {
		name = "form-upload-" + reservation.ID[:8]
		attributes = map[string]string{"source": "router-form-upload", "form_id": formBinding.FormID, "field_id": fieldID}
	}
	session, err := operation.loader.BeginUpload(ctx, contracts.PublicLoaderBeginUploadRequest{
		Stream: stream, Name: name, IdempotencyKey: key, ContentType: strings.ToLower(mediaType),
		DeclaredSize: request.ContentLength, MaxBytes: maxBytes, PartSize: operation.partSize,
		Groups: []string{config.AccessGroup}, Attributes: attributes,
	}, request.Streams)
	if err != nil {
		if formBinding != nil {
			_ = operation.abortFormReservation(ctx, *formBinding, formToken, reservation.ID)
		}
		return contracts.DeliveryResponse{}, mapPublicError(err)
	}
	if !validUUID(session.ID) || session.PartSize != operation.partSize {
		return contracts.DeliveryResponse{}, contracts.ErrPublicUnavailable
	}
	if session.State != "uploading" && session.State != "committing" && session.State != "committed" {
		return contracts.DeliveryResponse{}, contracts.ErrPublicUnavailable
	}
	file, err := operation.loader.Commit(ctx, contracts.PublicLoaderCommitRequest{SessionID: session.ID, Parts: session.StagedParts})
	if err != nil || !validUUID(file.ID) {
		_ = operation.loader.Abort(ctx, contracts.PublicLoaderAbortRequest{SessionID: session.ID, Parts: session.StagedParts})
		if formBinding != nil {
			_ = operation.abortFormReservation(ctx, *formBinding, formToken, reservation.ID)
		}
		return contracts.DeliveryResponse{}, mapPublicError(err)
	}
	if err := ctx.Err(); err != nil {
		return contracts.DeliveryResponse{}, err
	}

	if formBinding != nil {
		return operation.executeFormUpload(ctx, config, *formBinding, formToken, fieldID, reservation, file)
	}
	if err := operation.addToFileSet(ctx, config.FileSetID, file.ID); err != nil {
		return contracts.DeliveryResponse{}, err
	}
	body, err := json.Marshal(result{FileID: file.ID, FileSetID: config.FileSetID})
	if err != nil {
		return contracts.DeliveryResponse{}, contracts.ErrPublicUnavailable
	}
	return contracts.DeliveryResponse{
		StatusCode: 201, ContentType: "application/json; charset=utf-8", ContentLength: int64(len(body)),
		CacheControl: "no-store", Body: io.NopCloser(bytes.NewReader(body)),
	}, nil
}

func parseFieldQuery(raw string) (string, error) {
	if raw == "" || len(raw) > 256 {
		return "", contracts.ErrPublicInvalid
	}
	values, err := url.ParseQuery(raw)
	if err != nil || len(values) != 1 || len(values["field"]) != 1 {
		return "", contracts.ErrPublicInvalid
	}
	field := values.Get("field")
	if len(field) == 0 || len(field) > 64 || field[0] < 'a' || field[0] > 'z' {
		return "", contracts.ErrPublicInvalid
	}
	for index := 1; index < len(field); index++ {
		char := field[index]
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '_' {
			return "", contracts.ErrPublicInvalid
		}
	}
	return field, nil
}

func (operation *Operation) abortFormReservation(ctx context.Context, binding publicforms.BindingConfig, token, reservationID string) error {
	if operation.forms == nil {
		return contracts.ErrPublicUnavailable
	}
	return operation.forms.AbortVisitorUpload(ctx, contracts.PublicAbortVisitorUploadRequest{
		PublicFormBinding: binding.Binding(), Token: token, ReservationID: reservationID,
	})
}

func (operation *Operation) executeFormUpload(ctx context.Context, config routeConfig, binding publicforms.BindingConfig, token, fieldID string,
	reservation contracts.PublicVisitorUploadReservation, file contracts.PublicLoaderFile) (contracts.DeliveryResponse, error) {
	if reservation.Completed {
		if !validUUID(reservation.FileID) || !validUUID(reservation.AttachmentID) || reservation.Size != file.Size || reservation.SHA256 != file.SHA256 ||
			reservation.ContentType != file.ContentType || reservation.AccessGroup != config.AccessGroup {
			return publicforms.ErrorResponse(contracts.ErrPublicConflict, binding.FormID, false)
		}
		if err := operation.addToFileSet(ctx, config.FileSetID, reservation.FileID); err != nil {
			return contracts.DeliveryResponse{}, err
		}
		return formUploadResponse(http.StatusOK, contracts.PublicVisitorAttachment{ID: reservation.AttachmentID, FieldID: fieldID})
	}
	attachment, err := operation.forms.CompleteVisitorUpload(ctx, contracts.PublicCompleteVisitorUploadRequest{
		PublicFormBinding: binding.Binding(), Token: token, ReservationID: reservation.ID,
		FileID: file.ID, SHA256: file.SHA256, ContentType: file.ContentType, Size: file.Size, AccessGroup: config.AccessGroup,
	})
	if err != nil {
		if errors.Is(err, contracts.ErrPublicExpired) || errors.Is(err, contracts.ErrPublicNotFound) {
			// A committed Loader file is not a usable attachment until Forms has
			// recorded it. Expiry and missing reservation are definitive; a CAS
			// conflict is ambiguous because another concurrent request may have
			// completed the same reservation, so preserve the file for retry.
			if cleanupErr := operation.deleteCommittedFile(ctx, file.ID); cleanupErr != nil {
				return contracts.DeliveryResponse{}, contracts.ErrPublicUnavailable
			}
			_ = operation.abortFormReservation(ctx, binding, token, reservation.ID)
		}
		return publicforms.ErrorResponse(err, binding.FormID, errors.Is(err, contracts.ErrPublicExpired) || errors.Is(err, contracts.ErrPublicNotFound))
	}
	if attachment.ID == "" || attachment.FieldID != fieldID {
		return contracts.DeliveryResponse{}, contracts.ErrPublicUnavailable
	}
	if err := operation.addToFileSet(ctx, config.FileSetID, file.ID); err != nil {
		return contracts.DeliveryResponse{}, err
	}
	return formUploadResponse(http.StatusCreated, attachment)
}

func (operation *Operation) deleteCommittedFile(ctx context.Context, fileID string) error {
	for attempt := 0; attempt < 3; attempt++ {
		file, err := operation.loader.StatFile(ctx, contracts.PublicLoaderStatFileRequest{FileID: fileID})
		if errors.Is(err, contracts.ErrPublicNotFound) {
			return nil
		}
		if err != nil || file.ID != fileID || file.Revision < 1 {
			return contracts.ErrPublicUnavailable
		}
		err = operation.loader.DeleteFile(ctx, contracts.PublicLoaderDeleteFileRequest{FileID: fileID, Revision: file.Revision})
		if err == nil || errors.Is(err, contracts.ErrPublicNotFound) {
			return nil
		}
		if !errors.Is(err, contracts.ErrPublicConflict) {
			return contracts.ErrPublicUnavailable
		}
	}
	return contracts.ErrPublicUnavailable
}

func formUploadResponse(status int, attachment contracts.PublicVisitorAttachment) (contracts.DeliveryResponse, error) {
	body, err := json.Marshal(formResult{AttachmentID: attachment.ID, FieldID: attachment.FieldID})
	if err != nil {
		return contracts.DeliveryResponse{}, contracts.ErrPublicUnavailable
	}
	return contracts.DeliveryResponse{StatusCode: status, ContentType: "application/json; charset=utf-8", ContentLength: int64(len(body)),
		CacheControl: "no-store", Body: io.NopCloser(bytes.NewReader(body))}, nil
}

func scopedFormIdempotencyKey(request contracts.PublicOperationRequest, config routeConfig, fieldID string) string {
	value := strings.Join([]string{scopedIdempotencyKey(request, config), config.Form.FormID,
		fmt.Sprint(config.Form.SchemaVersion), fieldID}, "\x00")
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func (operation *Operation) addToFileSet(ctx context.Context, setID, fileID string) error {
	if setID == "" {
		return nil
	}
	for attempt := 0; attempt < 3; attempt++ {
		set, err := operation.loader.GetFileSet(ctx, contracts.PublicLoaderGetFileSetRequest{FileSetID: setID})
		if err != nil || set.ID != setID || set.Revision <= 0 {
			return contracts.ErrPublicUnavailable
		}
		_, err = operation.loader.AddFileToSet(ctx, contracts.PublicLoaderAddFileSetRequest{FileSetID: setID, FileID: fileID, Revision: set.Revision})
		if err == nil {
			return nil
		}
		if !errors.Is(err, contracts.ErrPublicConflict) || ctx.Err() != nil {
			return mapPublicError(err)
		}
	}
	return contracts.ErrPublicUnavailable
}

func scopedIdempotencyKey(request contracts.PublicOperationRequest, config routeConfig) string {
	value := strings.Join([]string{OperationID, request.Method, strings.ToLower(request.Host), request.EscapedPath,
		config.AccessGroup, config.FileSetID, request.IdempotencyKey}, "\x00")
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func validUUID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil
}

func mapPublicError(err error) error {
	return publicforms.MapFormsError(err)
}

func mapCoreAuthorizationError(err error) error {
	switch {
	case errors.Is(err, contracts.ErrAccessForbidden):
		return contracts.ErrPublicForbidden
	case errors.Is(err, contracts.ErrAccessUnauthorized):
		return contracts.ErrPublicUnauthorized
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	default:
		return contracts.ErrPublicUnavailable
	}
}

var _ contracts.PublicOperation = (*Operation)(nil)
