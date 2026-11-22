package application

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"mime"
	"net/mail"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	core "github.com/Zubrium/cdn/core/presentation"
	schemamodel "github.com/Zubrium/cdn/engines/forms/features/schemas/model"
	"github.com/Zubrium/cdn/engines/forms/features/sessions/model"
	"github.com/Zubrium/cdn/engines/forms/features/sessions/repository"
	submissionapp "github.com/Zubrium/cdn/engines/forms/features/submissions/application"
	submissionmodel "github.com/Zubrium/cdn/engines/forms/features/submissions/model"
)

var (
	ErrExpired  = errors.New("visitor form session expired")
	ErrMismatch = errors.New("visitor form session does not match the published route")
)

type Schemas interface {
	Get(context.Context, string) (schemamodel.Schema, int64, error)
}

type Submissions interface {
	Get(context.Context, string) (submissionmodel.Submission, int64, error)
	CreateAccepted(context.Context, submissionapp.CreateInput) (submissionmodel.Submission, int64, error)
}

type Files interface {
	StatFile(context.Context, core.Session, string) (core.File, error)
	DeleteFile(context.Context, core.Session, string, int64) error
}

type Service struct {
	repository  *repository.Repository
	schemas     Schemas
	submissions Submissions
	files       Files
	fileSession core.Session
	ttl         time.Duration
	pageSize    int
}

func New(repo *repository.Repository, schemas Schemas, submissions Submissions, files Files, fileSession core.Session, ttl time.Duration, pageSize int) (*Service, error) {
	if repo == nil || schemas == nil || submissions == nil || files == nil || ttl < time.Minute || ttl > 24*time.Hour || pageSize < 1 || pageSize > 1000 {
		return nil, core.ErrModuleDataUnavailable
	}
	return &Service{repository: repo, schemas: schemas, submissions: submissions, files: files, fileSession: fileSession, ttl: ttl, pageSize: pageSize}, nil
}

type Binding struct {
	FormID        string
	SchemaVersion int64
}

type SessionView struct {
	ID            string
	FormID        string
	SchemaVersion int64
	Fields        map[string]any
	ExpiresAt     time.Time
	Completed     bool
}

type Credential struct {
	Token   string
	Session SessionView
}

type BeginUploadInput struct {
	Binding           Binding
	Token             string
	FieldID           string
	IdempotencyKey    string
	ContentType       string
	DeclaredSize      int64
	RouteMaxBytes     int64
	RouteContentTypes []string
	AccessGroup       string
}

type UploadReservation struct {
	ID          string
	FieldID     string
	MaxBytes    int64
	Completed   bool
	FileID      string
	SHA256      string
	Size        int64
	ContentType string
	AccessGroup string
}

type CompletedUpload struct {
	AttachmentID string
	FieldID      string
}

type CompleteUploadInput struct {
	Binding       Binding
	Token         string
	ReservationID string
	FileID        string
	SHA256        string
	ContentType   string
	Size          int64
	AccessGroup   string
}

type AbortUploadInput struct {
	Binding       Binding
	Token         string
	ReservationID string
}

type SubmitInput struct {
	Binding        Binding
	Token          string
	IdempotencyKey string
	Values         map[string]any
}

type Receipt struct {
	ID        string
	CreatedAt time.Time
}

func (service *Service) Create(ctx context.Context, binding Binding) (Credential, error) {
	if service == nil || ctx == nil || !validBinding(binding) {
		return Credential{}, core.ErrModuleDataInvalid
	}
	schema, _, err := service.currentSchema(ctx, binding)
	if err != nil {
		return Credential{}, err
	}
	if _, err := schemamodel.ParseFieldDefinitions(schema.Fields); err != nil {
		return Credential{}, core.ErrModuleDataUnavailable
	}
	token, tokenHash, err := newToken()
	if err != nil {
		return Credential{}, core.ErrModuleDataUnavailable
	}
	id, err := newUUID()
	if err != nil {
		return Credential{}, core.ErrModuleDataUnavailable
	}
	now := time.Now().UTC()
	entry := model.Session{ID: id, FormID: binding.FormID, SchemaVersion: binding.SchemaVersion,
		State: model.StateActive, CreatedAt: now, ExpiresAt: now.Add(service.ttl), Uploads: []model.Upload{}}
	stored, err := service.repository.Create(ctx, tokenHash, entry)
	if err != nil {
		return Credential{}, mapStoreError(err)
	}
	return Credential{Token: token, Session: view(stored.Session, schema.Fields)}, nil
}

func (service *Service) Get(ctx context.Context, binding Binding, token string) (SessionView, error) {
	stored, schema, err := service.load(ctx, binding, token, false)
	if err != nil {
		return SessionView{}, err
	}
	if stored.Session.State == model.StateCancelled {
		return SessionView{}, ErrExpired
	}
	if stored.Session.State == model.StateSubmitted {
		return view(stored.Session, nil), nil
	}
	if _, err := schemamodel.ParseFieldDefinitions(schema.Fields); err != nil {
		return SessionView{}, core.ErrModuleDataUnavailable
	}
	return view(stored.Session, schema.Fields), nil
}

func (service *Service) BeginUpload(ctx context.Context, input BeginUploadInput) (UploadReservation, error) {
	if service == nil || ctx == nil || !validBinding(input.Binding) || !validToken(input.Token) || !validIdentifier(input.FieldID) ||
		!validIdempotencyKey(input.IdempotencyKey) || input.DeclaredSize < -1 || input.RouteMaxBytes < 1 || !validGroup(input.AccessGroup) {
		return UploadReservation{}, core.ErrModuleDataInvalid
	}
	mediaType, err := normalizedMediaType(input.ContentType)
	if err != nil {
		return UploadReservation{}, core.ErrModuleDataInvalid
	}
	if !containsMediaType(input.RouteContentTypes, mediaType) {
		return UploadReservation{}, core.ErrModuleDataInvalid
	}
	tokenHash := digest(input.Token)
	keyHash := digest(input.IdempotencyKey)
	for attempt := 0; attempt < 5; attempt++ {
		stored, schema, err := service.loadByHash(ctx, input.Binding, tokenHash, true)
		if err != nil {
			return UploadReservation{}, err
		}
		if stored.Session.State != model.StateActive {
			return UploadReservation{}, core.ErrModuleDataConflict
		}
		definitions, err := schemamodel.ParseFieldDefinitions(schema.Fields)
		if err != nil {
			return UploadReservation{}, core.ErrModuleDataUnavailable
		}
		field, exists := definitions[input.FieldID]
		if !exists || field.Type != "file" || !containsMediaType(field.AllowedContentTypes, mediaType) {
			return UploadReservation{}, core.ErrModuleDataInvalid
		}
		maxBytes := min(field.MaxFileBytes, input.RouteMaxBytes)
		if input.DeclaredSize > maxBytes {
			return UploadReservation{}, core.ErrModuleDataLimitExceeded
		}
		for _, upload := range stored.Session.Uploads {
			if upload.IdempotencyHash == keyHash {
				declaredSizeConflict := upload.DeclaredSize >= 0 && input.DeclaredSize >= 0 && upload.DeclaredSize != input.DeclaredSize
				if upload.Deleting || upload.FieldID != input.FieldID || upload.ContentType != mediaType || declaredSizeConflict || upload.MaxBytes != maxBytes || upload.AccessGroup != input.AccessGroup {
					return UploadReservation{}, core.ErrModuleDataConflict
				}
				return reservation(upload), nil
			}
		}
		count := 0
		for _, upload := range stored.Session.Uploads {
			if upload.FieldID == input.FieldID {
				count++
			}
		}
		if count >= field.MaxFiles || len(stored.Session.Uploads) >= model.MaxUploadsPerSession {
			return UploadReservation{}, core.ErrModuleDataLimitExceeded
		}
		id, err := newUUID()
		if err != nil {
			return UploadReservation{}, core.ErrModuleDataUnavailable
		}
		upload := model.Upload{ID: id, FieldID: input.FieldID, IdempotencyHash: keyHash, ContentType: mediaType,
			DeclaredSize: input.DeclaredSize, MaxBytes: maxBytes, AccessGroup: input.AccessGroup}
		stored.Session.Uploads = append(stored.Session.Uploads, upload)
		updated, err := service.repository.Update(ctx, stored)
		if errors.Is(err, core.ErrModuleDataConflict) {
			continue
		}
		if err != nil {
			return UploadReservation{}, mapStoreError(err)
		}
		return reservation(updated.Session.Uploads[len(updated.Session.Uploads)-1]), nil
	}
	return UploadReservation{}, core.ErrModuleDataConflict
}

func (service *Service) CompleteUpload(ctx context.Context, input CompleteUploadInput) (CompletedUpload, error) {
	if service == nil || ctx == nil || !validBinding(input.Binding) || !validToken(input.Token) || !validIdentifier(input.ReservationID) ||
		!validIdentifier(input.FileID) || !validSHA256(input.SHA256) || input.Size < 0 || !validGroup(input.AccessGroup) {
		return CompletedUpload{}, core.ErrModuleDataInvalid
	}
	tokenHash := digest(input.Token)
	for attempt := 0; attempt < 5; attempt++ {
		stored, schema, err := service.loadByHash(ctx, input.Binding, tokenHash, true)
		if err != nil {
			return CompletedUpload{}, err
		}
		index := findUpload(stored.Session.Uploads, input.ReservationID)
		if index < 0 {
			return CompletedUpload{}, core.ErrModuleDataNotFound
		}
		upload := stored.Session.Uploads[index]
		if upload.Deleting {
			return CompletedUpload{}, core.ErrModuleDataConflict
		}
		if upload.AccessGroup != input.AccessGroup || upload.ContentType != strings.ToLower(input.ContentType) || upload.Size != 0 && upload.Size != input.Size {
			return CompletedUpload{}, core.ErrModuleDataConflict
		}
		if upload.Completed {
			if upload.FileID != input.FileID || upload.Size != input.Size || upload.SHA256 != input.SHA256 {
				return CompletedUpload{}, core.ErrModuleDataConflict
			}
			return CompletedUpload{AttachmentID: upload.ID, FieldID: upload.FieldID}, nil
		}
		if stored.Session.State != model.StateActive {
			// A reservation that lost a race with submit/cancel/expiry cannot be
			// attached. Completed reservations were handled above and remain
			// idempotent after submit.
			return CompletedUpload{}, ErrExpired
		}
		if upload.DeclaredSize >= 0 && upload.DeclaredSize != input.Size || input.Size > upload.MaxBytes {
			return CompletedUpload{}, core.ErrModuleDataLimitExceeded
		}
		definitions, err := schemamodel.ParseFieldDefinitions(schema.Fields)
		if err != nil {
			return CompletedUpload{}, core.ErrModuleDataUnavailable
		}
		field, exists := definitions[upload.FieldID]
		if !exists || field.Type != "file" || input.Size > field.MaxFileBytes || !containsMediaType(field.AllowedContentTypes, upload.ContentType) {
			return CompletedUpload{}, core.ErrModuleDataConflict
		}
		file, err := service.files.StatFile(ctx, service.fileSession, input.FileID)
		if err != nil || file.ID != input.FileID || file.Size != input.Size || file.SHA256 != input.SHA256 || strings.ToLower(file.ContentType) != upload.ContentType ||
			file.Revision < 1 || !hasGroup(file.Groups, upload.AccessGroup) {
			if errors.Is(err, core.ErrFileNotFound) {
				return CompletedUpload{}, core.ErrModuleDataNotFound
			}
			if err != nil {
				return CompletedUpload{}, mapStoreError(err)
			}
			return CompletedUpload{}, core.ErrModuleDataInvalid
		}
		stored.Session.Uploads[index].FileID = input.FileID
		stored.Session.Uploads[index].SHA256 = input.SHA256
		stored.Session.Uploads[index].Size = input.Size
		stored.Session.Uploads[index].Completed = true
		_, err = service.repository.Update(ctx, stored)
		if errors.Is(err, core.ErrModuleDataConflict) {
			continue
		}
		if err != nil {
			return CompletedUpload{}, mapStoreError(err)
		}
		return CompletedUpload{AttachmentID: upload.ID, FieldID: upload.FieldID}, nil
	}
	return CompletedUpload{}, core.ErrModuleDataConflict
}

func (service *Service) AbortUpload(ctx context.Context, input AbortUploadInput) error {
	if service == nil || ctx == nil || !validBinding(input.Binding) || !validToken(input.Token) || !validIdentifier(input.ReservationID) {
		return core.ErrModuleDataInvalid
	}
	tokenHash := digest(input.Token)
	for attempt := 0; attempt < 8; attempt++ {
		stored, _, err := service.loadByHash(ctx, input.Binding, tokenHash, true)
		if err != nil {
			return err
		}
		if stored.Session.State != model.StateActive {
			return core.ErrModuleDataConflict
		}
		index := findUpload(stored.Session.Uploads, input.ReservationID)
		if index < 0 {
			return nil
		}
		upload := stored.Session.Uploads[index]
		if upload.Completed && !upload.Deleting {
			stored.Session.Uploads[index].Deleting = true
			if _, err := service.repository.Update(ctx, stored); errors.Is(err, core.ErrModuleDataConflict) {
				continue
			} else if err != nil {
				return mapStoreError(err)
			}
			continue
		}
		if upload.Completed {
			if err := service.deleteFile(ctx, upload.FileID); err != nil {
				return err
			}
		}
		stored.Session.Uploads = append(stored.Session.Uploads[:index], stored.Session.Uploads[index+1:]...)
		_, err = service.repository.Update(ctx, stored)
		if errors.Is(err, core.ErrModuleDataConflict) {
			continue
		}
		return mapStoreError(err)
	}
	return core.ErrModuleDataConflict
}

func (service *Service) Submit(ctx context.Context, input SubmitInput) (Receipt, error) {
	if service == nil || ctx == nil || !validBinding(input.Binding) || !validToken(input.Token) || !validIdempotencyKey(input.IdempotencyKey) || input.Values == nil {
		return Receipt{}, core.ErrModuleDataInvalid
	}
	tokenHash, keyHash := digest(input.Token), digest(input.IdempotencyKey)
	fingerprint, fingerprintErr := fingerprintValues(input.Values)
	if fingerprintErr != nil {
		return Receipt{}, core.ErrModuleDataInvalid
	}
	for attempt := 0; attempt < 8; attempt++ {
		stored, _, err := service.loadByHash(ctx, input.Binding, tokenHash, true)
		if err != nil {
			return Receipt{}, err
		}
		if stored.Session.State == model.StateSubmitted || stored.Session.State == model.StateSubmitting {
			if stored.Session.SubmissionKeyHash != keyHash || stored.Session.SubmissionFingerprint != fingerprint {
				return Receipt{}, core.ErrModuleDataConflict
			}
			if stored.Session.State == model.StateSubmitting {
				stored, err = service.finishSubmission(ctx, tokenHash, stored)
				if err != nil {
					return Receipt{}, err
				}
			}
			submission, _, err := service.submissions.Get(ctx, stored.Session.SubmissionID)
			if err != nil {
				return Receipt{}, mapStoreError(err)
			}
			return Receipt{ID: submission.ID, CreatedAt: submission.CreatedAt}, nil
		}
		if stored.Session.State != model.StateActive {
			return Receipt{}, ErrExpired
		}
		_, normalizedValues, usedUploads, fileIDs, err := service.fingerprintAndValidate(ctx, input, tokenHash)
		if err != nil {
			return Receipt{}, err
		}
		stored.Session.State = model.StateSubmitting
		stored.Session.SubmissionID = deterministicSubmissionID(stored.Session.ID, keyHash)
		stored.Session.SubmissionKeyHash = keyHash
		stored.Session.SubmissionFingerprint = fingerprint
		stored.Session.SubmissionValues = normalizedValues
		stored.Session.SubmissionAttachments = append([]string(nil), fileIDs...)
		stored.Session.SubmissionCreatedAt = time.Now().UTC()
		stored.Session.UsedUploadIDs = append([]string(nil), usedUploads...)
		reserved, err := service.repository.Update(ctx, stored)
		if errors.Is(err, core.ErrModuleDataConflict) {
			continue
		}
		if err != nil {
			return Receipt{}, mapStoreError(err)
		}
		reserved, err = service.finishSubmission(ctx, tokenHash, reserved)
		if err != nil {
			return Receipt{}, err
		}
		submission, _, err := service.submissions.Get(ctx, reserved.Session.SubmissionID)
		if err != nil {
			return Receipt{}, mapStoreError(err)
		}
		return Receipt{ID: submission.ID, CreatedAt: submission.CreatedAt}, nil
	}
	return Receipt{}, core.ErrModuleDataConflict
}

// finishSubmission idempotently materializes the answer reserved in the
// session, then advances the session to submitted. A retry after a process or
// database interruption resumes from the durable reservation.
func (service *Service) finishSubmission(ctx context.Context, tokenHash string, stored model.Stored) (model.Stored, error) {
	for attempt := 0; attempt < 8; attempt++ {
		if stored.Session.State == model.StateSubmitted {
			return stored, nil
		}
		if stored.Session.State != model.StateSubmitting {
			return model.Stored{}, core.ErrModuleDataConflict
		}
		schema, _, err := service.schemas.Get(ctx, stored.Session.FormID)
		if err != nil {
			return model.Stored{}, mapStoreError(err)
		}
		submission, _, err := service.submissions.CreateAccepted(ctx, submissionapp.CreateInput{
			ID: stored.Session.SubmissionID, FormID: stored.Session.FormID, FormKey: schema.Key, SchemaVersion: stored.Session.SchemaVersion,
			Values: stored.Session.SubmissionValues, Attachments: stored.Session.SubmissionAttachments,
			IdempotencyKeyHash: stored.Session.SubmissionKeyHash, IdempotencyFingerprint: stored.Session.SubmissionFingerprint,
			CreatedAt: stored.Session.SubmissionCreatedAt,
		})
		if err != nil {
			return model.Stored{}, mapStoreError(err)
		}
		if submission.ID != stored.Session.SubmissionID || submission.FormID != stored.Session.FormID || submission.SchemaVersion != stored.Session.SchemaVersion {
			return model.Stored{}, core.ErrModuleDataUnavailable
		}
		stored.Session.State = model.StateSubmitted
		updated, err := service.repository.Update(ctx, stored)
		if err == nil {
			return updated, nil
		}
		if !errors.Is(err, core.ErrModuleDataConflict) {
			return model.Stored{}, mapStoreError(err)
		}
		stored, err = service.repository.Get(ctx, tokenHash)
		if err != nil {
			return model.Stored{}, mapStoreError(err)
		}
	}
	return model.Stored{}, core.ErrModuleDataConflict
}

func (service *Service) Cancel(ctx context.Context, binding Binding, token string) error {
	if service == nil || ctx == nil || !validBinding(binding) || !validToken(token) {
		return core.ErrModuleDataInvalid
	}
	for attempt := 0; attempt < 5; attempt++ {
		stored, _, err := service.loadByHash(ctx, binding, digest(token), false)
		if err != nil {
			return err
		}
		if stored.Session.State == model.StateSubmitted || stored.Session.State == model.StateSubmitting {
			return core.ErrModuleDataConflict
		}
		if stored.Session.State == model.StateCancelled {
			return service.cleanupSession(ctx, stored)
		}
		stored.Session.State = model.StateCancelled
		updated, err := service.repository.Update(ctx, stored)
		if errors.Is(err, core.ErrModuleDataConflict) {
			continue
		}
		if err != nil {
			return mapStoreError(err)
		}
		return service.cleanupSession(ctx, updated)
	}
	return core.ErrModuleDataConflict
}

func (service *Service) CancelFormSessions(ctx context.Context, formID string, currentVersion int64) error {
	if service == nil || ctx == nil || !validIdentifier(formID) || currentVersion < 1 {
		return core.ErrModuleDataInvalid
	}
	after := ""
	for {
		items, next, err := service.repository.ListByForm(ctx, formID, after, service.pageSize)
		if err != nil {
			return mapStoreError(err)
		}
		for _, item := range items {
			if item.Session.State != model.StateActive || item.Session.SchemaVersion == currentVersion {
				continue
			}
			item.Session.State = model.StateCancelled
			updated, err := service.repository.Update(ctx, item)
			if errors.Is(err, core.ErrModuleDataConflict) {
				continue
			}
			if err != nil {
				return mapStoreError(err)
			}
			if err := service.cleanupSession(ctx, updated); err != nil {
				return err
			}
		}
		if next == "" {
			return nil
		}
		after = next
	}
}

// Cleanup removes expired and cancelled visitor data. A submitted session
// keeps only the files referenced by its immutable submission.
func (service *Service) Cleanup(ctx context.Context) error {
	if service == nil || ctx == nil {
		return core.ErrModuleDataUnavailable
	}
	after := ""
	for {
		items, next, err := service.repository.ListAll(ctx, after, service.pageSize)
		if err != nil {
			return mapStoreError(err)
		}
		for _, item := range items {
			if item.Session.State == model.StateCancelled {
				if err := service.cleanupSession(ctx, item); err != nil {
					return err
				}
				continue
			}
			if item.Session.State == model.StateSubmitting {
				if _, err := service.finishSubmission(ctx, tokenHashFromRecord(item), item); err != nil {
					return err
				}
				continue
			}
			if item.Session.State == model.StateActive {
				if !item.Session.ExpiresAt.After(time.Now().UTC()) {
					if err := service.expireActiveSession(ctx, item); err != nil {
						return err
					}
					continue
				}
				if err := service.cleanupDeletingUploads(ctx, tokenHashFromRecord(item), item); err != nil {
					return err
				}
				item, err = service.repository.Get(ctx, tokenHashFromRecord(item))
				if errors.Is(err, core.ErrModuleDataNotFound) {
					continue
				}
				if err != nil {
					return mapStoreError(err)
				}
				if item.Session.State != model.StateActive {
					continue
				}
				schema, _, err := service.schemas.Get(ctx, item.Session.FormID)
				if errors.Is(err, core.ErrModuleDataNotFound) || (err == nil && schema.Version != item.Session.SchemaVersion) {
					if err := service.cancelActiveSession(ctx, item); err != nil {
						return err
					}
				} else if err != nil {
					return mapStoreError(err)
				}
			}
		}
		if next == "" {
			return nil
		}
		after = next
	}
}

func (service *Service) cancelActiveSession(ctx context.Context, stored model.Stored) error {
	tokenHash := tokenHashFromRecord(stored)
	for attempt := 0; attempt < 8; attempt++ {
		if stored.Session.State != model.StateActive {
			switch stored.Session.State {
			case model.StateCancelled:
				return service.cleanupSession(ctx, stored)
			case model.StateSubmitting:
				_, err := service.finishSubmission(ctx, tokenHash, stored)
				return err
			case model.StateSubmitted:
				if !stored.Session.ExpiresAt.After(time.Now().UTC()) {
					return service.cleanupSession(ctx, stored)
				}
				return nil
			default:
				return core.ErrModuleDataUnavailable
			}
		}
		stored.Session.State = model.StateCancelled
		updated, err := service.repository.Update(ctx, stored)
		if err == nil {
			return service.cleanupSession(ctx, updated)
		}
		if !errors.Is(err, core.ErrModuleDataConflict) {
			return mapStoreError(err)
		}
		stored, err = service.repository.Get(ctx, tokenHash)
		if err != nil {
			if errors.Is(err, core.ErrModuleDataNotFound) {
				return nil
			}
			return mapStoreError(err)
		}
	}
	return core.ErrModuleDataConflict
}

func (service *Service) expireActiveSession(ctx context.Context, stored model.Stored) error {
	if stored.Session.ExpiresAt.After(time.Now().UTC()) {
		return nil
	}
	return service.cancelActiveSession(ctx, stored)
}

func (service *Service) cleanupDeletingUploads(ctx context.Context, tokenHash string, stored model.Stored) error {
	for _, upload := range stored.Session.Uploads {
		if !upload.Deleting {
			continue
		}
		if err := service.deleteFile(ctx, upload.FileID); err != nil {
			return err
		}
		for attempt := 0; attempt < 8; attempt++ {
			current, err := service.repository.Get(ctx, tokenHash)
			if errors.Is(err, core.ErrModuleDataNotFound) {
				return nil
			}
			if err != nil {
				return mapStoreError(err)
			}
			index := findUpload(current.Session.Uploads, upload.ID)
			if index < 0 {
				break
			}
			if current.Session.State != model.StateActive {
				break
			}
			current.Session.Uploads = append(current.Session.Uploads[:index], current.Session.Uploads[index+1:]...)
			_, err = service.repository.Update(ctx, current)
			if err == nil {
				break
			}
			if !errors.Is(err, core.ErrModuleDataConflict) {
				return mapStoreError(err)
			}
		}
	}
	return nil
}

func tokenHashFromRecord(stored model.Stored) string {
	const prefix = "visitor:"
	if !strings.HasPrefix(stored.RecordKey, prefix) {
		return ""
	}
	return strings.TrimPrefix(stored.RecordKey, prefix)
}

func (service *Service) fingerprintAndValidate(ctx context.Context, input SubmitInput, tokenHash string) (string, map[string]any, []string, []string, error) {
	stored, schema, err := service.loadByHash(ctx, input.Binding, tokenHash, true)
	if err != nil {
		return "", nil, nil, nil, err
	}
	if stored.Session.State != model.StateActive {
		return "", nil, nil, nil, core.ErrModuleDataConflict
	}
	definitions, err := schemamodel.ParseFieldDefinitions(schema.Fields)
	if err != nil {
		return "", nil, nil, nil, core.ErrModuleDataUnavailable
	}
	if len(input.Values) > len(definitions) {
		return "", nil, nil, nil, core.ErrModuleDataInvalid
	}
	for name := range input.Values {
		if _, exists := definitions[name]; !exists {
			return "", nil, nil, nil, core.ErrModuleDataInvalid
		}
	}
	normalized := make(map[string]any, len(input.Values))
	usedUploads := make([]string, 0)
	fileIDs := make([]string, 0)
	used := make(map[string]struct{})
	fieldNames := make([]string, 0, len(definitions))
	for name := range definitions {
		fieldNames = append(fieldNames, name)
	}
	sort.Strings(fieldNames)
	for _, name := range fieldNames {
		field := definitions[name]
		value, exists := input.Values[name]
		if !exists || value == nil {
			if field.Required {
				return "", nil, nil, nil, core.ErrModuleDataInvalid
			}
			continue
		}
		switch field.Type {
		case "text", "email":
			text, ok := value.(string)
			if !ok || !utf8.ValidString(text) || strings.ContainsRune(text, '\x00') || (field.MinLength != nil && len([]rune(text)) < *field.MinLength) || (field.MaxLength != nil && len([]rune(text)) > *field.MaxLength) {
				return "", nil, nil, nil, core.ErrModuleDataInvalid
			}
			if field.Type == "email" {
				address, err := mail.ParseAddress(text)
				if err != nil || address.Address != text {
					return "", nil, nil, nil, core.ErrModuleDataInvalid
				}
			}
			if field.Pattern != "" && !regexp.MustCompile(field.Pattern).MatchString(text) {
				return "", nil, nil, nil, core.ErrModuleDataInvalid
			}
			normalized[name] = text
		case "number":
			number, ok := numericValue(value)
			if !ok || (field.Minimum != nil && number < *field.Minimum) || (field.Maximum != nil && number > *field.Maximum) {
				return "", nil, nil, nil, core.ErrModuleDataInvalid
			}
			normalized[name] = number
		case "boolean":
			if _, ok := value.(bool); !ok {
				return "", nil, nil, nil, core.ErrModuleDataInvalid
			}
			normalized[name] = value
		case "select":
			selection, ok := value.(string)
			if !ok || !containsString(field.Options, selection) {
				return "", nil, nil, nil, core.ErrModuleDataInvalid
			}
			normalized[name] = selection
		case "file":
			ids, ok := attachmentIDs(value)
			if !ok || (field.Required && len(ids) == 0) || len(ids) > field.MaxFiles {
				return "", nil, nil, nil, core.ErrModuleDataInvalid
			}
			storedIDs := make([]string, 0, len(ids))
			for _, id := range ids {
				if _, duplicate := used[id]; duplicate {
					return "", nil, nil, nil, core.ErrModuleDataInvalid
				}
				used[id] = struct{}{}
				index := findUpload(stored.Session.Uploads, id)
				if index < 0 {
					return "", nil, nil, nil, core.ErrModuleDataInvalid
				}
				upload := stored.Session.Uploads[index]
				if !upload.Completed || upload.Deleting || upload.FieldID != name || upload.Size > field.MaxFileBytes || !containsMediaType(field.AllowedContentTypes, upload.ContentType) {
					return "", nil, nil, nil, core.ErrModuleDataInvalid
				}
				file, err := service.files.StatFile(ctx, service.fileSession, upload.FileID)
				if err != nil || file.ID != upload.FileID || file.Size != upload.Size || file.SHA256 != upload.SHA256 || !hasGroup(file.Groups, upload.AccessGroup) {
					if errors.Is(err, core.ErrFileNotFound) {
						return "", nil, nil, nil, core.ErrModuleDataNotFound
					}
					if err != nil {
						return "", nil, nil, nil, mapStoreError(err)
					}
					return "", nil, nil, nil, core.ErrModuleDataInvalid
				}
				storedIDs = append(storedIDs, upload.FileID)
				fileIDs = append(fileIDs, upload.FileID)
				usedUploads = append(usedUploads, upload.ID)
			}
			normalized[name] = storedIDs
		default:
			return "", nil, nil, nil, core.ErrModuleDataInvalid
		}
	}
	fingerprint, err := fingerprintValues(input.Values)
	if err != nil {
		return "", nil, nil, nil, core.ErrModuleDataInvalid
	}
	return fingerprint, normalized, usedUploads, fileIDs, nil
}

func fingerprintValues(values map[string]any) (string, error) {
	canonical, err := json.Marshal(values)
	if err != nil {
		return "", err
	}
	return digest(string(canonical)), nil
}

func (service *Service) load(ctx context.Context, binding Binding, token string, requireActive bool) (model.Stored, schemamodel.Schema, error) {
	if !validBinding(binding) || !validToken(token) {
		return model.Stored{}, schemamodel.Schema{}, core.ErrModuleDataInvalid
	}
	return service.loadByHash(ctx, binding, digest(token), requireActive)
}

func (service *Service) loadByHash(ctx context.Context, binding Binding, tokenHash string, requireActive bool) (model.Stored, schemamodel.Schema, error) {
	for attempt := 0; attempt < 8; attempt++ {
		stored, err := service.repository.Get(ctx, tokenHash)
		if errors.Is(err, core.ErrModuleDataNotFound) {
			return model.Stored{}, schemamodel.Schema{}, ErrExpired
		}
		if err != nil {
			return model.Stored{}, schemamodel.Schema{}, mapStoreError(err)
		}
		if stored.Session.FormID != binding.FormID || stored.Session.SchemaVersion != binding.SchemaVersion {
			return model.Stored{}, schemamodel.Schema{}, ErrMismatch
		}
		if stored.Session.State == model.StateSubmitting {
			stored, err = service.finishSubmission(ctx, tokenHash, stored)
			if err != nil {
				return model.Stored{}, schemamodel.Schema{}, err
			}
			continue
		}
		if stored.Session.State == model.StateCancelled {
			if err := service.cleanupSession(ctx, stored); err != nil {
				return model.Stored{}, schemamodel.Schema{}, err
			}
			return model.Stored{}, schemamodel.Schema{}, ErrExpired
		}
		if !stored.Session.ExpiresAt.After(time.Now().UTC()) {
			if stored.Session.State == model.StateActive {
				if err := service.expireActiveSession(ctx, stored); err != nil {
					return model.Stored{}, schemamodel.Schema{}, err
				}
			} else if stored.Session.State == model.StateSubmitted {
				if err := service.cleanupSession(ctx, stored); err != nil {
					return model.Stored{}, schemamodel.Schema{}, err
				}
			}
			return model.Stored{}, schemamodel.Schema{}, ErrExpired
		}
		if stored.Session.State == model.StateSubmitted {
			if requireActive {
				return stored, schemamodel.Schema{}, nil
			}
			return stored, schemamodel.Schema{}, nil
		}
		if stored.Session.State != model.StateActive {
			return model.Stored{}, schemamodel.Schema{}, core.ErrModuleDataUnavailable
		}
		schema, _, err := service.currentSchema(ctx, binding)
		if err == nil {
			return stored, schema, nil
		}
		if !errors.Is(err, core.ErrModuleDataNotFound) && !errors.Is(err, ErrMismatch) {
			return model.Stored{}, schemamodel.Schema{}, mapStoreError(err)
		}
		if cancelErr := service.cancelActiveSession(ctx, stored); cancelErr != nil {
			return model.Stored{}, schemamodel.Schema{}, cancelErr
		}
		return model.Stored{}, schemamodel.Schema{}, ErrExpired
	}
	return model.Stored{}, schemamodel.Schema{}, core.ErrModuleDataConflict
}

func (service *Service) currentSchema(ctx context.Context, binding Binding) (schemamodel.Schema, int64, error) {
	schema, revision, err := service.schemas.Get(ctx, binding.FormID)
	if err != nil {
		return schemamodel.Schema{}, 0, err
	}
	if schema.ID != binding.FormID || schema.Version != binding.SchemaVersion {
		return schemamodel.Schema{}, 0, ErrMismatch
	}
	return schema, revision, nil
}

func (service *Service) cleanupSession(ctx context.Context, stored model.Stored) error {
	if stored.Session.State == model.StateActive {
		if stored.Session.ExpiresAt.After(time.Now().UTC()) {
			return core.ErrModuleDataConflict
		}
		return service.cancelActiveSession(ctx, stored)
	}
	if stored.Session.State == model.StateSubmitting {
		stored, err := service.finishSubmission(ctx, tokenHashFromRecord(stored), stored)
		if err != nil {
			return err
		}
		if stored.Session.ExpiresAt.After(time.Now().UTC()) {
			return nil
		}
	}
	if stored.Session.State != model.StateCancelled && stored.Session.State != model.StateSubmitted {
		return core.ErrModuleDataConflict
	}
	used := make(map[string]struct{}, len(stored.Session.UsedUploadIDs))
	for _, id := range stored.Session.UsedUploadIDs {
		used[id] = struct{}{}
	}
	for _, upload := range stored.Session.Uploads {
		if !upload.Completed {
			continue
		}
		if stored.Session.State == model.StateSubmitted {
			if _, keep := used[upload.ID]; keep {
				continue
			}
		}
		if err := service.deleteFile(ctx, upload.FileID); err != nil {
			return err
		}
	}
	if err := service.repository.Delete(ctx, stored); err != nil && !errors.Is(err, core.ErrModuleDataNotFound) {
		return mapStoreError(err)
	}
	return nil
}

func (service *Service) deleteFile(ctx context.Context, id string) error {
	for attempt := 0; attempt < 3; attempt++ {
		file, err := service.files.StatFile(ctx, service.fileSession, id)
		if errors.Is(err, core.ErrFileNotFound) {
			return nil
		}
		if err != nil {
			return mapStoreError(err)
		}
		err = service.files.DeleteFile(ctx, service.fileSession, id, file.Revision)
		if err == nil || errors.Is(err, core.ErrFileNotFound) {
			return nil
		}
		if !errors.Is(err, core.ErrRevisionConflict) {
			return mapStoreError(err)
		}
	}
	return core.ErrModuleDataConflict
}

func validBinding(binding Binding) bool {
	return validIdentifier(binding.FormID) && binding.SchemaVersion > 0
}

func validIdentifier(value string) bool {
	if value == "" || len(value) > 128 || !utf8.ValidString(value) {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || strings.ContainsRune("_.-", char) {
			continue
		}
		return false
	}
	return true
}

func validToken(value string) bool {
	if len(value) != 43 {
		return false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	return err == nil && len(decoded) == 32
}

func newToken() (string, string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw[:])
	return token, digest(token), nil
}

func newUUID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	return formatUUID(raw), nil
}

func formatUUID(value [16]byte) string {
	encoded := make([]byte, 36)
	hex.Encode(encoded[0:8], value[0:4])
	hex.Encode(encoded[9:13], value[4:6])
	hex.Encode(encoded[14:18], value[6:8])
	hex.Encode(encoded[19:23], value[8:10])
	hex.Encode(encoded[24:36], value[10:16])
	encoded[8], encoded[13], encoded[18], encoded[23] = '-', '-', '-', '-'
	return string(encoded)
}

func deterministicSubmissionID(sessionID, idemHash string) string {
	sum := sha256.Sum256([]byte("forms-submission-v1\x00" + sessionID + "\x00" + idemHash))
	var raw [16]byte
	copy(raw[:], sum[:16])
	raw[6] = (raw[6] & 0x0f) | 0x50
	raw[8] = (raw[8] & 0x3f) | 0x80
	return formatUUID(raw)
}

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func validIdempotencyKey(value string) bool {
	if len(value) < 16 || len(value) > 128 || !utf8.ValidString(value) {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || strings.ContainsRune("-_.:", char) {
			continue
		}
		return false
	}
	return true
}

func validGroup(value string) bool {
	if value == "" || len(value) > 64 || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for index := 1; index < len(value); index++ {
		char := value[index]
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '_' && char != '-' {
			return false
		}
	}
	return true
}

func normalizedMediaType(value string) (string, error) {
	if value == "" || strings.TrimSpace(value) != value || strings.ContainsAny(value, "\r\n") {
		return "", core.ErrModuleDataInvalid
	}
	mediaType, params, err := mime.ParseMediaType(value)
	if err != nil || len(params) != 0 || strings.Contains(mediaType, "*") || schemamodel.IsActiveMediaType(mediaType) {
		return "", core.ErrModuleDataInvalid
	}
	return strings.ToLower(mediaType), nil
}

func containsMediaType(values []string, expected string) bool {
	for _, raw := range values {
		mediaType, _, err := mime.ParseMediaType(raw)
		if err == nil && strings.ToLower(mediaType) == expected {
			return true
		}
	}
	return false
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func numericValue(value any) (float64, bool) {
	var number float64
	switch typed := value.(type) {
	case float64:
		number = typed
	case float32:
		number = float64(typed)
	case int:
		number = float64(typed)
	case int64:
		number = float64(typed)
	case json.Number:
		parsed, err := typed.Float64()
		if err != nil {
			return 0, false
		}
		number = parsed
	case string:
		parsed, err := strconv.ParseFloat(typed, 64)
		if err != nil {
			return 0, false
		}
		number = parsed
	default:
		return 0, false
	}
	return number, !math.IsNaN(number) && !math.IsInf(number, 0)
}

func attachmentIDs(value any) ([]string, bool) {
	var values []string
	switch typed := value.(type) {
	case []any:
		values = make([]string, len(typed))
		for index, item := range typed {
			text, ok := item.(string)
			if !ok {
				return nil, false
			}
			values[index] = text
		}
	case []string:
		values = append([]string(nil), typed...)
	default:
		return nil, false
	}
	seen := make(map[string]struct{}, len(values))
	for _, id := range values {
		if !validIdentifier(id) {
			return nil, false
		}
		if _, exists := seen[id]; exists {
			return nil, false
		}
		seen[id] = struct{}{}
	}
	return values, true
}

func hasGroup(groups []string, expected string) bool { return containsString(groups, expected) }

func findUpload(uploads []model.Upload, id string) int {
	for index := range uploads {
		if uploads[index].ID == id {
			return index
		}
	}
	return -1
}

func reservation(upload model.Upload) UploadReservation {
	return UploadReservation{ID: upload.ID, FieldID: upload.FieldID, MaxBytes: upload.MaxBytes, Completed: upload.Completed,
		FileID: upload.FileID, SHA256: upload.SHA256, Size: upload.Size, ContentType: upload.ContentType, AccessGroup: upload.AccessGroup}
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && hex.EncodeToString(decoded) == value
}

func view(session model.Session, rawFields map[string]any) SessionView {
	fields := rawFields
	if fields == nil {
		fields = map[string]any{}
	}
	return SessionView{ID: session.ID, FormID: session.FormID, SchemaVersion: session.SchemaVersion,
		Fields: fields, ExpiresAt: session.ExpiresAt.UTC(), Completed: session.State == model.StateSubmitted}
}

func mapStoreError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, core.ErrModuleDataNotFound):
		return core.ErrModuleDataNotFound
	case errors.Is(err, core.ErrModuleDataConflict):
		return core.ErrModuleDataConflict
	case errors.Is(err, core.ErrModuleDataInvalid):
		return core.ErrModuleDataInvalid
	case errors.Is(err, core.ErrModuleDataLimitExceeded):
		return core.ErrModuleDataLimitExceeded
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	default:
		return core.ErrModuleDataUnavailable
	}
}
