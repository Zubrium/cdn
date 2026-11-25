package contracts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// Admin engine sentinels are neutral: Admin HTTP adapters and the composition
// adapters compare the same values on both sides of the process-local bus.
var (
	ErrAdminContentUnauthorized = errors.New("unauthorized")
	ErrAdminContentForbidden    = errors.New("forbidden")
	ErrAdminContentNotFound     = errors.New("not found")
	ErrAdminContentConflict     = errors.New("revision conflict")
	ErrAdminContentInvalid      = errors.New("invalid request")
	ErrAdminContentTooLarge     = errors.New("request too large")
	ErrAdminContentUnavailable  = errors.New("Content dependency unavailable")
)

type adminContextKey struct{}

// WithAdminSubject binds a composition-authenticated subject to an Admin call
// context. The subject is created only by an adapter; HTTP payloads cannot set
// it. The original credential is never stored in the browser-visible response.
func WithAdminSubject(ctx context.Context, subject AuthorizationSubject) context.Context {
	if ctx == nil || subject.Validate() != nil {
		return ctx
	}
	return context.WithValue(ctx, adminContextKey{}, subject)
}

func AdminSubjectFromContext(ctx context.Context) (AuthorizationSubject, bool) {
	if ctx == nil {
		return AuthorizationSubject{}, false
	}
	subject, ok := ctx.Value(adminContextKey{}).(AuthorizationSubject)
	return subject, ok && subject.Validate() == nil
}

type AdminAuthenticateRequest struct {
	Credential string `json:"credential"`
}

type AdminAuthenticateSessionRequest struct {
	Token string `json:"token"`
}

type AdminAuthenticateResponse struct {
	Subject AuthorizationSubject `json:"subject"`
}

type AdminEmptyResponse struct{}

func adminOperation[Request, Response any](id string, validate func(Request) error) Operation[Request, Response] {
	return Operation[Request, Response]{Key: Key{ID: id, Version: 1}, Validate: validate}
}

func decodeAdminJSON(raw json.RawMessage, target any) error {
	if len(raw) == 0 {
		return ErrAdminContentInvalid
	}
	return json.Unmarshal(raw, target)
}

func AdminContentAuthenticateOperation() Operation[AdminAuthenticateRequest, AdminAuthenticateResponse] {
	return adminOperation[AdminAuthenticateRequest, AdminAuthenticateResponse]("admin.content.authenticate", func(request AdminAuthenticateRequest) error {
		if request.Credential == "" {
			return ErrAdminContentInvalid
		}
		return nil
	})
}

func AdminContentAuthenticateSessionOperation() Operation[AdminAuthenticateSessionRequest, AdminAuthenticateResponse] {
	return adminOperation[AdminAuthenticateSessionRequest, AdminAuthenticateResponse]("admin.content.authenticate-session", func(request AdminAuthenticateSessionRequest) error {
		if request.Token == "" {
			return ErrAdminContentInvalid
		}
		return nil
	})
}

type AdminContentModelProjection struct {
	Version       int             `json:"version"`
	ModelRevision int64           `json:"model_revision"`
	Digest        string          `json:"digest"`
	DataModel     json.RawMessage `json:"data_model"`
}

func (schema AdminContentModelProjection) ComputeDigest() (string, error) {
	payload := struct {
		Version       int             `json:"version"`
		ModelRevision int64           `json:"model_revision"`
		DataModel     json.RawMessage `json:"data_model"`
	}{schema.Version, schema.ModelRevision, schema.DataModel}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func (schema AdminContentModelProjection) VerifyDigest() bool {
	if schema.Version != 1 || schema.ModelRevision < 1 || schema.DataModel == nil || len(schema.Digest) != sha256.Size*2 {
		return false
	}
	digest, err := schema.ComputeDigest()
	return err == nil && strings.EqualFold(digest, schema.Digest)
}

type AdminContentPagePlacementProjection struct {
	Version        int                     `json:"version"`
	PageSnapshotID string                  `json:"page_snapshot_id"`
	PageID         string                  `json:"page_id"`
	Revision       int64                   `json:"revision"`
	Digest         string                  `json:"digest"`
	State          string                  `json:"state"`
	Placements     []AdminContentPlacement `json:"placements"`
}

func (projection AdminContentPagePlacementProjection) ComputeDigest() (string, error) {
	payload := struct {
		Version        int                     `json:"version"`
		PageSnapshotID string                  `json:"page_snapshot_id"`
		PageID         string                  `json:"page_id"`
		Revision       int64                   `json:"revision"`
		State          string                  `json:"state"`
		Placements     []AdminContentPlacement `json:"placements"`
	}{projection.Version, projection.PageSnapshotID, projection.PageID, projection.Revision, projection.State, projection.Placements}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func (projection AdminContentPagePlacementProjection) VerifyDigest() bool {
	if projection.Version != 1 || projection.PageSnapshotID == "" || projection.PageID == "" || projection.Revision < 1 || projection.Placements == nil || len(projection.Digest) != sha256.Size*2 {
		return false
	}
	digest, err := projection.ComputeDigest()
	return err == nil && strings.EqualFold(digest, projection.Digest)
}

type AdminContentProjections struct {
	ContentModel AdminContentModelProjection         `json:"content_model"`
	Page         AdminContentPagePlacementProjection `json:"page_placement_projection"`
	Locale       string                              `json:"locale"`
}

func (projections AdminContentProjections) VerifyScope(pageSnapshotID string) bool {
	return projections.ContentModel.VerifyDigest() && projections.Page.VerifyDigest() &&
		projections.Page.PageSnapshotID == pageSnapshotID
}

type AdminContentPlacement struct {
	ID          string                       `json:"id"`
	PageID      string                       `json:"page_id"`
	PageName    string                       `json:"page_name"`
	FeatureKey  string                       `json:"feature_key"`
	FeatureName string                       `json:"feature_name"`
	Version     string                       `json:"version"`
	Inputs      []AdminContentPlacementInput `json:"inputs"`
}

type AdminContentPlacementInput struct {
	InputKey string                            `json:"input_key"`
	Kind     string                            `json:"kind"`
	EntityID string                            `json:"entity_id"`
	Fields   []AdminContentPlacementField      `json:"fields"`
	Lookup   *AdminContentPlacementInputLookup `json:"lookup,omitempty"`
}

type AdminContentPlacementField struct {
	InputPath   string `json:"input_path"`
	FieldID     string `json:"field_id"`
	MediaAccept string `json:"media_accept,omitempty"`
}

type AdminContentPlacementInputLookup struct {
	Argument string `json:"argument"`
	FieldID  string `json:"field_id"`
}

type AdminContentLocaleConfig struct {
	Revision      int64    `json:"revision"`
	Locales       []string `json:"locales"`
	DefaultLocale string   `json:"default_locale"`
}

type AdminContentReplaceLocaleConfigRequest struct {
	ExpectedRevision int64    `json:"expected_revision"`
	Locales          []string `json:"locales"`
	DefaultLocale    string   `json:"default_locale"`
	IdempotencyKey   string   `json:"-"`
}

type AdminContentWorkspace struct {
	SnapshotID            string                      `json:"snapshot_id"`
	PageSnapshotID        string                      `json:"page_snapshot_id"`
	Locale                string                      `json:"locale"`
	LocaleCatalogRevision int64                       `json:"locale_catalog_revision"`
	ModelRevision         int64                       `json:"model_revision"`
	ModelDigest           string                      `json:"model_digest"`
	PageRevision          int64                       `json:"page_revision"`
	PageDigest            string                      `json:"page_digest"`
	DataModel             json.RawMessage             `json:"data_model"`
	Revision              int64                       `json:"revision"`
	SelectionRevision     int64                       `json:"selection_revision"`
	PublishedRevision     int64                       `json:"published_revision"`
	Records               []PublishedContentRecord    `json:"records"`
	Selections            []PublishedContentSelection `json:"selections"`
	Placements            []AdminContentPlacement     `json:"placements"`
	ClientRecordIDs       map[string]string           `json:"client_record_ids,omitempty"`
	ClientTranslationIDs  map[string]string           `json:"client_translation_ids,omitempty"`
}

type AdminContentRecordInput struct {
	ID            string         `json:"id,omitempty"`
	ClientKey     string         `json:"client_key,omitempty"`
	TranslationID string         `json:"translation_id,omitempty"`
	EntityID      string         `json:"entity_id"`
	SortOrder     int            `json:"sort_order"`
	Values        map[string]any `json:"values"`
}

type AdminContentSaveWorkspaceDraftRequest struct {
	SnapshotID                string                      `json:"-"`
	IdempotencyKey            string                      `json:"-"`
	ExpectedRevision          int64                       `json:"expected_revision"`
	ExpectedSelectionRevision int64                       `json:"expected_selection_revision"`
	Records                   []AdminContentRecordInput   `json:"records"`
	Selections                []PublishedContentSelection `json:"selections"`
}

type AdminContentPublishWorkspaceRequest struct {
	SnapshotID                string `json:"-"`
	IdempotencyKey            string `json:"-"`
	ExpectedRevision          int64  `json:"expected_revision"`
	ExpectedSelectionRevision int64  `json:"expected_selection_revision"`
}

type AdminContentHistoryItem struct {
	ID              string    `json:"id"`
	DraftRevision   int64     `json:"draft_revision"`
	ContentRevision int64     `json:"content_revision"`
	CreatedAt       time.Time `json:"created_at"`
	RecordCount     int       `json:"record_count"`
}

type AdminContentHistoryPage struct {
	Items      []AdminContentHistoryItem `json:"items"`
	NextCursor string                    `json:"next_cursor,omitempty"`
}

type AdminContentRestoreHistoryRequest struct {
	SnapshotID                string `json:"-"`
	HistoryID                 string `json:"-"`
	IdempotencyKey            string `json:"-"`
	ExpectedRevision          int64  `json:"expected_revision"`
	ExpectedSelectionRevision int64  `json:"expected_selection_revision"`
}

type AdminContentAPI interface {
	Authenticate(context.Context, string) (context.Context, error)
	AuthenticateSession(context.Context, string) (context.Context, error)
	GetLocaleConfig(context.Context) (AdminContentLocaleConfig, error)
	ReplaceLocaleConfig(context.Context, AdminContentReplaceLocaleConfigRequest) (AdminContentLocaleConfig, error)
	GetWorkspace(context.Context, AdminContentProjections) (AdminContentWorkspace, error)
	SaveWorkspaceDraft(context.Context, AdminContentProjections, AdminContentSaveWorkspaceDraftRequest) (AdminContentWorkspace, error)
	PublishWorkspace(context.Context, AdminContentProjections, AdminContentPublishWorkspaceRequest) (AdminContentWorkspace, error)
	ListHistory(context.Context, AdminContentProjections, string, int) (AdminContentHistoryPage, error)
	RestoreHistory(context.Context, AdminContentProjections, AdminContentRestoreHistoryRequest) (AdminContentWorkspace, error)
}

type AdminContentSubjectRequest struct {
	Subject AuthorizationSubject `json:"subject"`
}

type AdminContentGetLocaleConfigResponse struct {
	Config AdminContentLocaleConfig `json:"config"`
}

type AdminContentReplaceLocaleConfigRequestMessage struct {
	Subject        AuthorizationSubject                   `json:"subject"`
	Request        AdminContentReplaceLocaleConfigRequest `json:"request"`
	IdempotencyKey string                                 `json:"idempotency_key"`
}

type AdminContentWorkspaceResponse struct {
	Workspace AdminContentWorkspace `json:"workspace"`
}

type AdminContentLocaleConfigResponse struct {
	Config AdminContentLocaleConfig `json:"config"`
}

type AdminContentWorkspaceRequest struct {
	Subject     AuthorizationSubject    `json:"subject"`
	Projections AdminContentProjections `json:"projections"`
}

type AdminContentSaveWorkspaceDraftRequestMessage struct {
	Subject        AuthorizationSubject                  `json:"subject"`
	Projections    AdminContentProjections               `json:"projections"`
	Request        AdminContentSaveWorkspaceDraftRequest `json:"request"`
	SnapshotID     string                                `json:"snapshot_id"`
	IdempotencyKey string                                `json:"idempotency_key"`
}

type AdminContentPublishWorkspaceRequestMessage struct {
	Subject        AuthorizationSubject                `json:"subject"`
	Projections    AdminContentProjections             `json:"projections"`
	Request        AdminContentPublishWorkspaceRequest `json:"request"`
	SnapshotID     string                              `json:"snapshot_id"`
	IdempotencyKey string                              `json:"idempotency_key"`
}

type AdminContentListHistoryRequest struct {
	Subject     AuthorizationSubject    `json:"subject"`
	Projections AdminContentProjections `json:"projections"`
	Cursor      string                  `json:"cursor"`
	Limit       int                     `json:"limit"`
}

type AdminContentHistoryResponse struct {
	Page AdminContentHistoryPage `json:"page"`
}

type AdminContentRestoreHistoryRequestMessage struct {
	Subject        AuthorizationSubject              `json:"subject"`
	Projections    AdminContentProjections           `json:"projections"`
	Request        AdminContentRestoreHistoryRequest `json:"request"`
	SnapshotID     string                            `json:"snapshot_id"`
	HistoryID      string                            `json:"history_id"`
	IdempotencyKey string                            `json:"idempotency_key"`
}

func AdminContentGetLocaleConfigOperation() Operation[AdminContentSubjectRequest, AdminContentLocaleConfigResponse] {
	return adminOperation[AdminContentSubjectRequest, AdminContentLocaleConfigResponse]("admin.content.locale.get", func(request AdminContentSubjectRequest) error {
		if validAdminSubject(request.Subject) != nil {
			return ErrAdminContentInvalid
		}
		return nil
	})
}

func AdminContentReplaceLocaleConfigOperation() Operation[AdminContentReplaceLocaleConfigRequestMessage, AdminContentLocaleConfigResponse] {
	return adminOperation[AdminContentReplaceLocaleConfigRequestMessage, AdminContentLocaleConfigResponse]("admin.content.locale.replace", func(request AdminContentReplaceLocaleConfigRequestMessage) error {
		if validAdminSubject(request.Subject) != nil {
			return ErrAdminContentInvalid
		}
		return nil
	})
}

func AdminContentGetWorkspaceOperation() Operation[AdminContentWorkspaceRequest, AdminContentWorkspaceResponse] {
	return adminOperation[AdminContentWorkspaceRequest, AdminContentWorkspaceResponse]("admin.content.workspace.get", func(request AdminContentWorkspaceRequest) error {
		if validAdminSubject(request.Subject) != nil {
			return ErrAdminContentInvalid
		}
		return nil
	})
}

func AdminContentSaveWorkspaceDraftOperation() Operation[AdminContentSaveWorkspaceDraftRequestMessage, AdminContentWorkspaceResponse] {
	return adminOperation[AdminContentSaveWorkspaceDraftRequestMessage, AdminContentWorkspaceResponse]("admin.content.workspace.save", func(request AdminContentSaveWorkspaceDraftRequestMessage) error {
		if validAdminSubject(request.Subject) != nil {
			return ErrAdminContentInvalid
		}
		return nil
	})
}

func AdminContentPublishWorkspaceOperation() Operation[AdminContentPublishWorkspaceRequestMessage, AdminContentWorkspaceResponse] {
	return adminOperation[AdminContentPublishWorkspaceRequestMessage, AdminContentWorkspaceResponse]("admin.content.workspace.publish", func(request AdminContentPublishWorkspaceRequestMessage) error {
		if validAdminSubject(request.Subject) != nil {
			return ErrAdminContentInvalid
		}
		return nil
	})
}

func AdminContentListHistoryOperation() Operation[AdminContentListHistoryRequest, AdminContentHistoryResponse] {
	return adminOperation[AdminContentListHistoryRequest, AdminContentHistoryResponse]("admin.content.history.list", func(request AdminContentListHistoryRequest) error {
		if validAdminSubject(request.Subject) != nil || request.Limit < 1 {
			return ErrAdminContentInvalid
		}
		return nil
	})
}

func AdminContentRestoreHistoryOperation() Operation[AdminContentRestoreHistoryRequestMessage, AdminContentWorkspaceResponse] {
	return adminOperation[AdminContentRestoreHistoryRequestMessage, AdminContentWorkspaceResponse]("admin.content.history.restore", func(request AdminContentRestoreHistoryRequestMessage) error {
		if validAdminSubject(request.Subject) != nil {
			return ErrAdminContentInvalid
		}
		return nil
	})
}

func AdminContentOperationKeys() []Key {
	return []Key{
		AdminContentAuthenticateOperation().Key,
		AdminContentAuthenticateSessionOperation().Key,
		AdminContentGetLocaleConfigOperation().Key,
		AdminContentReplaceLocaleConfigOperation().Key,
		AdminContentGetWorkspaceOperation().Key,
		AdminContentSaveWorkspaceDraftOperation().Key,
		AdminContentPublishWorkspaceOperation().Key,
		AdminContentListHistoryOperation().Key,
		AdminContentRestoreHistoryOperation().Key,
	}
}
