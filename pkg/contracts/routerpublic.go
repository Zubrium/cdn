package contracts

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/google/uuid"
)

// Public Router operations use neutral payload contracts. Neither the module
// that owns an engine nor its credential can be selected by a request payload;
// composition attaches that identity outside the JSON envelope.
var (
	ErrPublicInvalidOperation       = errors.New("invalid public operation")
	ErrPublicOperationNotRegistered = errors.New("public operation is not registered")
	ErrPublicInvalid                = errors.New("invalid public request")
	ErrPublicInvalidTarget          = errors.New("invalid public operation target")
	ErrPublicUnauthorized           = errors.New("unauthorized")
	ErrPublicForbidden              = errors.New("forbidden")
	ErrPublicNotFound               = errors.New("not found")
	ErrPublicExpired                = errors.New("public form session expired")
	ErrPublicConflict               = errors.New("revision conflict")
	ErrPublicTooLarge               = errors.New("public request exceeds its size limit")
	ErrPublicUnavailable            = errors.New("public operation dependency unavailable")
)

const (
	PublicFormValidateOperationID       = "forms.public-validate-form"
	PublicFormValidateUploadOperationID = "forms.public-validate-upload-route"
	PublicFormCreateSessionOperationID  = "forms.public-create-session"
	PublicFormGetSessionOperationID     = "forms.public-get-session"
	PublicFormCancelSessionOperationID  = "forms.public-cancel-session"
	PublicFormBeginUploadOperationID    = "forms.public-begin-upload"
	PublicFormCompleteUploadOperationID = "forms.public-complete-upload"
	PublicFormAbortUploadOperationID    = "forms.public-abort-upload"
	PublicFormSubmitOperationID         = "forms.public-submit-response"

	PublicLoaderBeginUploadOperationID = "loader.public-begin-upload"
	PublicLoaderPutPartOperationID     = "loader.public-put-upload-part"
	PublicLoaderCommitOperationID      = "loader.public-commit-upload"
	PublicLoaderAbortOperationID       = "loader.public-abort-upload"
	PublicLoaderStatFileOperationID    = "loader.public-stat-file"
	PublicLoaderDeleteFileOperationID  = "loader.public-delete-file"
	PublicLoaderGetFileSetOperationID  = "loader.public-get-fileset"
	PublicLoaderAddFileSetOperationID  = "loader.public-add-fileset"

	// PublicOperationContractVersion is shared by every public forms/loader
	// operation. A breaking payload change requires a new key version.
	PublicOperationContractVersion uint32 = 1
)

// PublicOperation, PublicOperationDescriptor and PublicOperationRequest are
// the transport-neutral Router catalog contracts. The Router facade re-exports
// them as aliases so both sides compile against one definition.
type PublicOperation interface {
	Descriptor() PublicOperationDescriptor
	ValidateRouteConfig(json.RawMessage) error
	Execute(context.Context, json.RawMessage, PublicOperationRequest) (DeliveryResponse, error)
}

type PublicOperationDescriptor struct {
	ID                  string   `json:"id"`
	ContractVersion     int      `json:"contract_version"`
	Methods             []string `json:"methods"`
	MaxRequestBytes     int64    `json:"max_request_bytes"`
	AllowedContentTypes []string `json:"allowed_content_types"`
}

type PublicOperationRequest struct {
	Method            string
	Host              string
	EscapedPath       string
	Query             string
	ContentType       string
	ContentLength     int64
	IdempotencyKey    string
	IfNoneMatch       string
	Body              io.Reader
	FormSessionTokens map[string]string
	Streams           PublicUploadStreams
}

type DeliveryResponse struct {
	StatusCode         int
	ContentType        string
	ContentLength      int64
	ETag               string
	LastModified       string
	CacheControl       string
	Vary               []string
	ContentDisposition string
	AcceptRanges       string
	Body               io.ReadCloser
	FormSessionCookie  *FormSessionCookieChange
}

type FormSessionCookieChange struct {
	FormID    string
	Token     string
	ExpiresAt time.Time
	Clear     bool
}

// PublicFormBinding is copied from a server-side Router route configuration.
type PublicFormBinding struct {
	FormID        string `json:"form_id"`
	SchemaVersion int64  `json:"schema_version"`
}

func (binding PublicFormBinding) Validate() error {
	if binding.FormID == "" || len(binding.FormID) > 128 || binding.SchemaVersion < 1 {
		return ErrPublicInvalid
	}
	for _, char := range binding.FormID {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '_' || char == '.' || char == '-' {
			continue
		}
		return ErrPublicInvalid
	}
	return nil
}

type PublicUploadRouteBinding struct {
	PublicFormBinding
	AccessGroup         string   `json:"access_group"`
	MaxBytes            int64    `json:"max_bytes"`
	AllowedContentTypes []string `json:"allowed_content_types"`
}

func (binding PublicUploadRouteBinding) Validate() error {
	if err := binding.PublicFormBinding.Validate(); err != nil {
		return err
	}
	if !ValidAccessGroupKey(binding.AccessGroup) || binding.MaxBytes <= 0 || len(binding.AllowedContentTypes) == 0 || len(binding.AllowedContentTypes) > 32 {
		return ErrPublicInvalid
	}
	for _, contentType := range binding.AllowedContentTypes {
		if contentType == "" || len(contentType) > 128 {
			return ErrPublicInvalid
		}
	}
	return nil
}

type PublicVisitorSession struct {
	ID            string         `json:"id"`
	FormID        string         `json:"form_id"`
	SchemaVersion int64          `json:"schema_version"`
	Fields        map[string]any `json:"fields"`
	ExpiresAt     time.Time      `json:"expires_at"`
	Completed     bool           `json:"completed"`
}

type PublicVisitorSessionCredential struct {
	Token   string               `json:"token"`
	Session PublicVisitorSession `json:"session"`
}

type PublicVisitorUploadReservation struct {
	ID           string `json:"id"`
	FieldID      string `json:"field_id"`
	MaxBytes     int64  `json:"max_bytes"`
	Completed    bool   `json:"completed"`
	FileID       string `json:"file_id"`
	SHA256       string `json:"sha256"`
	AttachmentID string `json:"attachment_id"`
	ContentType  string `json:"content_type"`
	Size         int64  `json:"size"`
	AccessGroup  string `json:"access_group"`
}

type PublicVisitorAttachment struct {
	ID      string `json:"id"`
	FieldID string `json:"field_id"`
}

type PublicVisitorSubmissionReceipt struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
}

type PublicGetVisitorSessionRequest struct {
	PublicFormBinding
	Token string `json:"token"`
}

func (request PublicGetVisitorSessionRequest) Validate() error {
	return validatePublicBindingAndToken(request.PublicFormBinding, request.Token)
}

type PublicCancelVisitorSessionRequest struct {
	PublicFormBinding
	Token string `json:"token"`
}

func (request PublicCancelVisitorSessionRequest) Validate() error {
	return validatePublicBindingAndToken(request.PublicFormBinding, request.Token)
}

type PublicBeginVisitorUploadRequest struct {
	PublicFormBinding
	Token             string   `json:"token"`
	FieldID           string   `json:"field_id"`
	IdempotencyKey    string   `json:"idempotency_key"`
	ContentType       string   `json:"content_type"`
	DeclaredSize      int64    `json:"declared_size"`
	RouteMaxBytes     int64    `json:"route_max_bytes"`
	RouteContentTypes []string `json:"route_content_types"`
	AccessGroup       string   `json:"access_group"`
}

func (request PublicBeginVisitorUploadRequest) Validate() error {
	if err := validatePublicBindingAndToken(request.PublicFormBinding, request.Token); err != nil {
		return err
	}
	if !validPublicFieldID(request.FieldID) || !ValidPublicIdempotencyKey(request.IdempotencyKey) ||
		request.ContentType == "" || len(request.ContentType) > 128 || request.DeclaredSize < 0 ||
		request.RouteMaxBytes <= 0 || request.DeclaredSize > request.RouteMaxBytes ||
		!ValidAccessGroupKey(request.AccessGroup) || len(request.RouteContentTypes) == 0 || len(request.RouteContentTypes) > 32 {
		return ErrPublicInvalid
	}
	return nil
}

type PublicCompleteVisitorUploadRequest struct {
	PublicFormBinding
	Token         string `json:"token"`
	ReservationID string `json:"reservation_id"`
	FileID        string `json:"file_id"`
	SHA256        string `json:"sha256"`
	ContentType   string `json:"content_type"`
	Size          int64  `json:"size"`
	AccessGroup   string `json:"access_group"`
}

func (request PublicCompleteVisitorUploadRequest) Validate() error {
	if err := validatePublicBindingAndToken(request.PublicFormBinding, request.Token); err != nil {
		return err
	}
	if !validPublicUUID(request.ReservationID) || !validPublicUUID(request.FileID) || !ValidPublicSHA256(request.SHA256) ||
		request.ContentType == "" || len(request.ContentType) > 128 || request.Size <= 0 || !ValidAccessGroupKey(request.AccessGroup) {
		return ErrPublicInvalid
	}
	return nil
}

type PublicAbortVisitorUploadRequest struct {
	PublicFormBinding
	Token         string `json:"token"`
	ReservationID string `json:"reservation_id"`
}

func (request PublicAbortVisitorUploadRequest) Validate() error {
	if err := validatePublicBindingAndToken(request.PublicFormBinding, request.Token); err != nil {
		return err
	}
	if !validPublicUUID(request.ReservationID) {
		return ErrPublicInvalid
	}
	return nil
}

type PublicSubmitVisitorResponseRequest struct {
	PublicFormBinding
	Token          string         `json:"token"`
	IdempotencyKey string         `json:"idempotency_key"`
	Values         map[string]any `json:"values"`
}

func (request PublicSubmitVisitorResponseRequest) Validate() error {
	if err := validatePublicBindingAndToken(request.PublicFormBinding, request.Token); err != nil {
		return err
	}
	if !ValidPublicIdempotencyKey(request.IdempotencyKey) || request.Values == nil {
		return ErrPublicInvalid
	}
	return nil
}

type PublicLoaderStagedPart struct {
	Number   int    `json:"number"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256"`
	ObjectID string `json:"object_id"`
}

func (part PublicLoaderStagedPart) Validate() error {
	if part.Number < 1 || part.Size <= 0 || !ValidPublicSHA256(part.SHA256) || !validPublicUUID(part.ObjectID) {
		return ErrPublicInvalid
	}
	return nil
}

type PublicLoaderBeginUploadRequest struct {
	Stream          StreamRef         `json:"stream_ref"`
	Name            string            `json:"name"`
	IdempotencyKey  string            `json:"idempotency_key"`
	ContentType     string            `json:"content_type"`
	DeclaredSize    int64             `json:"declared_size"`
	MaxBytes        int64             `json:"max_bytes"`
	PartSize        int64             `json:"part_size"`
	Groups          []string          `json:"groups"`
	Attributes      map[string]string `json:"attributes"`
	FileSetID       string            `json:"file_set_id"`
	ManagementGroup string            `json:"management_group"`
}

func (request PublicLoaderBeginUploadRequest) Validate() error {
	if request.Stream == "" || request.Name == "" || len(request.Name) > 128 ||
		!ValidPublicIdempotencyKey(request.IdempotencyKey) || request.ContentType == "" ||
		len(request.ContentType) > 128 || request.DeclaredSize < -1 || request.MaxBytes <= 0 ||
		request.PartSize <= 0 || len(request.Groups) == 0 || len(request.Groups) > 8 {
		return ErrPublicInvalid
	}
	for _, group := range request.Groups {
		if !ValidAccessGroupKey(group) {
			return ErrPublicInvalid
		}
	}
	if request.FileSetID != "" {
		if !validPublicUUID(request.FileSetID) || !ValidAccessGroupKey(request.ManagementGroup) {
			return ErrPublicInvalid
		}
	}
	return nil
}

type PublicLoaderUploadSession struct {
	ID            string                   `json:"id"`
	State         string                   `json:"state"`
	PartSize      int64                    `json:"part_size"`
	ExpectedParts int                      `json:"expected_parts"`
	StagedParts   []PublicLoaderStagedPart `json:"staged_parts"`
}

func (session PublicLoaderUploadSession) Validate() error {
	if !validPublicUUID(session.ID) || session.State == "" || session.PartSize <= 0 || session.ExpectedParts < 0 ||
		len(session.StagedParts) != session.ExpectedParts {
		return ErrPublicInvalid
	}
	for _, part := range session.StagedParts {
		if err := part.Validate(); err != nil {
			return err
		}
	}
	return nil
}

type PublicLoaderPutPartRequest struct {
	SessionID string                 `json:"session_id"`
	Part      PublicLoaderStagedPart `json:"part"`
}

func (request PublicLoaderPutPartRequest) Validate() error {
	if !validPublicUUID(request.SessionID) {
		return ErrPublicInvalid
	}
	return request.Part.Validate()
}

type PublicLoaderCommitRequest struct {
	SessionID string                   `json:"session_id"`
	Parts     []PublicLoaderStagedPart `json:"parts,omitempty"`
}

func (request PublicLoaderCommitRequest) Validate() error {
	if !validPublicUUID(request.SessionID) {
		return ErrPublicInvalid
	}
	for _, part := range request.Parts {
		if err := part.Validate(); err != nil {
			return err
		}
	}
	return nil
}

type PublicLoaderAbortRequest struct {
	SessionID string                   `json:"session_id"`
	Parts     []PublicLoaderStagedPart `json:"parts"`
}

func (request PublicLoaderAbortRequest) Validate() error {
	if !validPublicUUID(request.SessionID) {
		return ErrPublicInvalid
	}
	for _, part := range request.Parts {
		if err := part.Validate(); err != nil {
			return err
		}
	}
	return nil
}

type PublicLoaderStatFileRequest struct {
	FileID string `json:"file_id"`
}

func (request PublicLoaderStatFileRequest) Validate() error {
	if !validPublicUUID(request.FileID) {
		return ErrPublicInvalid
	}
	return nil
}

type PublicLoaderDeleteFileRequest struct {
	FileID   string `json:"file_id"`
	Revision int64  `json:"revision"`
}

func (request PublicLoaderDeleteFileRequest) Validate() error {
	if !validPublicUUID(request.FileID) || request.Revision <= 0 {
		return ErrPublicInvalid
	}
	return nil
}

type PublicLoaderGetFileSetRequest struct {
	FileSetID string `json:"file_set_id"`
}

func (request PublicLoaderGetFileSetRequest) Validate() error {
	if !validPublicUUID(request.FileSetID) {
		return ErrPublicInvalid
	}
	return nil
}

type PublicLoaderAddFileSetRequest struct {
	FileSetID string `json:"file_set_id"`
	FileID    string `json:"file_id"`
	Revision  int64  `json:"revision"`
}

func (request PublicLoaderAddFileSetRequest) Validate() error {
	if !validPublicUUID(request.FileSetID) || !validPublicUUID(request.FileID) || request.Revision <= 0 {
		return ErrPublicInvalid
	}
	return nil
}

type PublicLoaderFile struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	ContentType string            `json:"content_type"`
	Size        int64             `json:"size"`
	SHA256      string            `json:"sha256"`
	Revision    int64             `json:"revision"`
	Groups      []string          `json:"groups"`
	Attributes  map[string]string `json:"attributes"`
}

func (file PublicLoaderFile) Validate() error {
	if !validPublicUUID(file.ID) || file.Revision <= 0 || !ValidPublicSHA256(file.SHA256) {
		return ErrPublicInvalid
	}
	return nil
}

type PublicLoaderFileSet struct {
	ID              string    `json:"id"`
	Namespace       string    `json:"namespace"`
	Name            string    `json:"name"`
	ManagementGroup string    `json:"management_group"`
	Revision        int64     `json:"revision"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

func (set PublicLoaderFileSet) Validate() error {
	if !validPublicUUID(set.ID) || set.Revision <= 0 || !ValidAccessGroupKey(set.ManagementGroup) {
		return ErrPublicInvalid
	}
	return nil
}

type PublicEmptyResponse struct{}

func publicOperation(id string) Key {
	return Key{ID: id, Version: PublicOperationContractVersion}
}

func PublicFormValidateOperation() Operation[PublicFormBinding, PublicEmptyResponse] {
	return Operation[PublicFormBinding, PublicEmptyResponse]{Key: publicOperation(PublicFormValidateOperationID), Validate: PublicFormBinding.Validate}
}

func PublicFormValidateUploadOperation() Operation[PublicUploadRouteBinding, PublicEmptyResponse] {
	return Operation[PublicUploadRouteBinding, PublicEmptyResponse]{Key: publicOperation(PublicFormValidateUploadOperationID), Validate: PublicUploadRouteBinding.Validate}
}

func PublicFormCreateSessionOperation() Operation[PublicFormBinding, PublicVisitorSessionCredential] {
	return Operation[PublicFormBinding, PublicVisitorSessionCredential]{Key: publicOperation(PublicFormCreateSessionOperationID), Validate: PublicFormBinding.Validate}
}

func PublicFormGetSessionOperation() Operation[PublicGetVisitorSessionRequest, PublicVisitorSession] {
	return Operation[PublicGetVisitorSessionRequest, PublicVisitorSession]{Key: publicOperation(PublicFormGetSessionOperationID), Validate: PublicGetVisitorSessionRequest.Validate}
}

func PublicFormCancelSessionOperation() Operation[PublicCancelVisitorSessionRequest, PublicEmptyResponse] {
	return Operation[PublicCancelVisitorSessionRequest, PublicEmptyResponse]{Key: publicOperation(PublicFormCancelSessionOperationID), Validate: PublicCancelVisitorSessionRequest.Validate}
}

func PublicFormBeginUploadOperation() Operation[PublicBeginVisitorUploadRequest, PublicVisitorUploadReservation] {
	return Operation[PublicBeginVisitorUploadRequest, PublicVisitorUploadReservation]{Key: publicOperation(PublicFormBeginUploadOperationID), Validate: PublicBeginVisitorUploadRequest.Validate}
}

func PublicFormCompleteUploadOperation() Operation[PublicCompleteVisitorUploadRequest, PublicVisitorAttachment] {
	return Operation[PublicCompleteVisitorUploadRequest, PublicVisitorAttachment]{Key: publicOperation(PublicFormCompleteUploadOperationID), Validate: PublicCompleteVisitorUploadRequest.Validate}
}

func PublicFormAbortUploadOperation() Operation[PublicAbortVisitorUploadRequest, PublicEmptyResponse] {
	return Operation[PublicAbortVisitorUploadRequest, PublicEmptyResponse]{Key: publicOperation(PublicFormAbortUploadOperationID), Validate: PublicAbortVisitorUploadRequest.Validate}
}

func PublicFormSubmitOperation() Operation[PublicSubmitVisitorResponseRequest, PublicVisitorSubmissionReceipt] {
	return Operation[PublicSubmitVisitorResponseRequest, PublicVisitorSubmissionReceipt]{Key: publicOperation(PublicFormSubmitOperationID), Validate: PublicSubmitVisitorResponseRequest.Validate}
}

func PublicLoaderBeginUploadOperation() Operation[PublicLoaderBeginUploadRequest, PublicLoaderUploadSession] {
	return Operation[PublicLoaderBeginUploadRequest, PublicLoaderUploadSession]{Key: publicOperation(PublicLoaderBeginUploadOperationID), Validate: PublicLoaderBeginUploadRequest.Validate}
}

func PublicLoaderPutPartOperation() Operation[PublicLoaderPutPartRequest, PublicLoaderUploadSession] {
	return Operation[PublicLoaderPutPartRequest, PublicLoaderUploadSession]{Key: publicOperation(PublicLoaderPutPartOperationID), Validate: PublicLoaderPutPartRequest.Validate}
}

func PublicLoaderCommitOperation() Operation[PublicLoaderCommitRequest, PublicLoaderFile] {
	return Operation[PublicLoaderCommitRequest, PublicLoaderFile]{Key: publicOperation(PublicLoaderCommitOperationID), Validate: PublicLoaderCommitRequest.Validate}
}

func PublicLoaderAbortOperation() Operation[PublicLoaderAbortRequest, PublicEmptyResponse] {
	return Operation[PublicLoaderAbortRequest, PublicEmptyResponse]{Key: publicOperation(PublicLoaderAbortOperationID), Validate: PublicLoaderAbortRequest.Validate}
}

func PublicLoaderStatFileOperation() Operation[PublicLoaderStatFileRequest, PublicLoaderFile] {
	return Operation[PublicLoaderStatFileRequest, PublicLoaderFile]{Key: publicOperation(PublicLoaderStatFileOperationID), Validate: PublicLoaderStatFileRequest.Validate}
}

func PublicLoaderDeleteFileOperation() Operation[PublicLoaderDeleteFileRequest, PublicEmptyResponse] {
	return Operation[PublicLoaderDeleteFileRequest, PublicEmptyResponse]{Key: publicOperation(PublicLoaderDeleteFileOperationID), Validate: PublicLoaderDeleteFileRequest.Validate}
}

func PublicLoaderGetFileSetOperation() Operation[PublicLoaderGetFileSetRequest, PublicLoaderFileSet] {
	return Operation[PublicLoaderGetFileSetRequest, PublicLoaderFileSet]{Key: publicOperation(PublicLoaderGetFileSetOperationID), Validate: PublicLoaderGetFileSetRequest.Validate}
}

func PublicLoaderAddFileSetOperation() Operation[PublicLoaderAddFileSetRequest, PublicLoaderFileSet] {
	return Operation[PublicLoaderAddFileSetRequest, PublicLoaderFileSet]{Key: publicOperation(PublicLoaderAddFileSetOperationID), Validate: PublicLoaderAddFileSetRequest.Validate}
}

func validatePublicBindingAndToken(binding PublicFormBinding, token string) error {
	if err := binding.Validate(); err != nil {
		return err
	}
	if token == "" || len(token) > 256 {
		return ErrPublicInvalid
	}
	return nil
}

func validPublicFieldID(value string) bool {
	if value == "" || len(value) > 64 || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for index := 1; index < len(value); index++ {
		char := value[index]
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '_' {
			return false
		}
	}
	return true
}

func validPublicUUID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil && id.String() == value
}

// ValidPublicIdempotencyKey and ValidPublicSHA256 are shared by the Router
// adapters and the engine-side providers.
func ValidPublicIdempotencyKey(value string) bool {
	if len(value) < 16 || len(value) > 128 {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '-' || char == '_' || char == '.' || char == ':' {
			continue
		}
		return false
	}
	return true
}

func ValidPublicSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, char := range value {
		if (char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') {
			continue
		}
		return false
	}
	return true
}
