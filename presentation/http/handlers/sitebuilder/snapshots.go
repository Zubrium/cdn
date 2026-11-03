package sitebuilder

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/google/uuid"

	formsFeature "websiteengine/features/forms"
	"websiteengine/features/sitebuilder"
	"websiteengine/pkg/environment"
	"websiteengine/pkg/principal"
	"websiteengine/pkg/response"
)

type SnapshotsHandler struct {
	service *sitebuilder.SnapshotService
	forms   *formsFeature.Service
	log     *slog.Logger
	maxJSON int64
}

func NewSnapshotsHandler(service *sitebuilder.SnapshotService, forms *formsFeature.Service, log *slog.Logger, maxJSON int64) *SnapshotsHandler {
	return &SnapshotsHandler{service: service, forms: forms, log: log, maxJSON: maxJSON}
}

func (h *SnapshotsHandler) Draft(w http.ResponseWriter, r *http.Request) {
	state, err := h.service.GetDraft(r.Context())
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	response.JSON(w, http.StatusOK, state)
}

type saveDraftRequest struct {
	Revision int64           `json:"revision"`
	Config   json.RawMessage `json:"config"`
}

func (h *SnapshotsHandler) SaveDraft(w http.ResponseWriter, r *http.Request) {
	actor, _ := principal.FromContext(r.Context())
	var req saveDraftRequest
	if err := response.DecodeJSON(w, r, &req, h.maxJSON); err != nil {
		response.WriteDecodeError(w, r, err)
		return
	}
	if len(req.Config) == 0 {
		response.Error(w, r, http.StatusBadRequest, "invalid_config", "Config is required")
		return
	}
	state, err := h.service.SaveDraft(r.Context(), actor.TokenID, req.Revision, req.Config)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	response.JSON(w, http.StatusOK, state)
}

func (h *SnapshotsHandler) Metadata(w http.ResponseWriter, r *http.Request) {
	state, err := h.service.GetMetadata(r.Context())
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	response.JSON(w, http.StatusOK, state)
}

type saveMetadataRequest struct {
	Revision int64           `json:"revision"`
	Metadata json.RawMessage `json:"metadata"`
}

func (h *SnapshotsHandler) SaveMetadata(w http.ResponseWriter, r *http.Request) {
	actor, _ := principal.FromContext(r.Context())
	var req saveMetadataRequest
	if err := response.DecodeJSON(w, r, &req, h.maxJSON); err != nil {
		response.WriteDecodeError(w, r, err)
		return
	}
	if len(req.Metadata) == 0 {
		response.Error(w, r, http.StatusBadRequest, "invalid_metadata", "Metadata is required")
		return
	}
	state, err := h.service.SaveMetadata(r.Context(), actor.TokenID, req.Revision, req.Metadata)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	response.JSON(w, http.StatusOK, state)
}

func (h *SnapshotsHandler) PublishMetadata(w http.ResponseWriter, r *http.Request) {
	actor, _ := principal.FromContext(r.Context())
	state, err := h.service.PublishMetadata(r.Context(), actor.TokenID)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	response.JSON(w, http.StatusOK, state)
}

func (h *SnapshotsHandler) PromoteMetadata(w http.ResponseWriter, r *http.Request) {
	actor, _ := principal.FromContext(r.Context())
	state, err := h.service.PromoteMetadata(r.Context(), actor.TokenID)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	response.JSON(w, http.StatusOK, state)
}

func (h *SnapshotsHandler) DesignSystem(w http.ResponseWriter, r *http.Request) {
	state, err := h.service.GetDesignSystem(r.Context())
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	response.JSON(w, http.StatusOK, map[string]any{"state": state, "definitions": sitebuilder.DesignTokenDefinitions()})
}

type saveDesignSystemRequest struct {
	Revision     int64           `json:"revision"`
	DesignSystem json.RawMessage `json:"designSystem"`
}

func (h *SnapshotsHandler) SaveDesignSystem(w http.ResponseWriter, r *http.Request) {
	actor, _ := principal.FromContext(r.Context())
	var req saveDesignSystemRequest
	if err := response.DecodeJSON(w, r, &req, h.maxJSON); err != nil {
		response.WriteDecodeError(w, r, err)
		return
	}
	if len(req.DesignSystem) == 0 {
		response.Error(w, r, http.StatusBadRequest, "invalid_design_system", "Design system is required")
		return
	}
	state, err := h.service.SaveDesignSystem(r.Context(), actor.TokenID, req.Revision, req.DesignSystem)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	response.JSON(w, http.StatusOK, map[string]any{"state": state, "definitions": sitebuilder.DesignTokenDefinitions()})
}

func (h *SnapshotsHandler) PreviewDesignSystem(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DesignSystem json.RawMessage `json:"designSystem"`
	}
	if err := response.DecodeJSON(w, r, &req, h.maxJSON); err != nil {
		response.WriteDecodeError(w, r, err)
		return
	}
	token, err := h.service.CreateDesignPreview(r.Context(), req.DesignSystem)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	response.JSON(w, http.StatusCreated, map[string]string{"token": token})
}

func (h *SnapshotsHandler) SnapshotThemeCSS(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.writeThemeError(w, r, err)
		return
	}
	value, err := h.service.ThemeForSnapshot(r.Context(), id)
	if err != nil {
		h.writeThemeError(w, r, err)
		return
	}
	h.writeThemeCSS(w, r, value, true)
}

func (h *SnapshotsHandler) PreviewThemeCSS(w http.ResponseWriter, r *http.Request) {
	value, ok := h.service.DesignPreview(r.PathValue("token"))
	if !ok {
		h.writeThemeError(w, r, sitebuilder.ErrNotFound)
		return
	}
	h.writeThemeCSS(w, r, value, false)
}

func (h *SnapshotsHandler) writeThemeCSS(w http.ResponseWriter, r *http.Request, value sitebuilder.DesignSystem, immutable bool) {
	css, err := value.CSS("")
	if err != nil {
		h.writeThemeError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if immutable {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-store")
	}
	_, _ = w.Write([]byte(css))
}

func (h *SnapshotsHandler) writeThemeError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, sitebuilder.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	h.log.Error("theme stylesheet request failed", "request_id", response.RequestID(r.Context()), "error", err)
	http.Error(w, "theme stylesheet unavailable", http.StatusInternalServerError)
}

type publishRequest struct {
	Name string `json:"name"`
}

func (h *SnapshotsHandler) Create(w http.ResponseWriter, r *http.Request) {
	actor, _ := principal.FromContext(r.Context())
	var req publishRequest
	if err := response.DecodeJSON(w, r, &req, h.maxJSON); err != nil {
		response.WriteDecodeError(w, r, err)
		return
	}
	record, err := h.service.Create(r.Context(), actor.TokenID, strings.TrimSpace(req.Name))
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	response.JSON(w, http.StatusCreated, record)
}

func (h *SnapshotsHandler) List(w http.ResponseWriter, r *http.Request) {
	limit, err := response.ParseLimit(r.URL.Query().Get("limit"), 50, 200)
	if err != nil {
		response.Error(w, r, http.StatusBadRequest, "invalid_limit", err.Error())
		return
	}
	var beforeSequence *int64
	if raw := r.URL.Query().Get("beforeSequence"); raw != "" {
		sequence, parseErr := strconv.ParseInt(raw, 10, 64)
		if parseErr != nil || sequence < 1 {
			response.Error(w, r, http.StatusBadRequest, "invalid_cursor", "beforeSequence должен быть положительным номером снапшота")
			return
		}
		beforeSequence = &sequence
	}
	items, err := h.service.List(r.Context(), limit+1, beforeSequence)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	var nextCursor *int64
	if len(items) > limit {
		cursor := items[limit-1].Sequence
		nextCursor = &cursor
		items = items[:limit]
	}
	active := map[string]string{"dev": "", "prod": ""}
	activeSnapshots := map[string]*sitebuilder.SnapshotRecord{"dev": nil, "prod": nil}
	for _, env := range []environment.Environment{environment.Dev, environment.Prod} {
		record, _, activeErr := h.service.Active(r.Context(), env)
		if errors.Is(activeErr, sitebuilder.ErrSnapshotUnavailable) {
			continue
		}
		if activeErr != nil {
			h.writeError(w, r, activeErr)
			return
		}
		active[env.String()] = record.ID.String()
		activeSnapshots[env.String()] = &record
	}
	events, err := h.service.Events(r.Context(), 200)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	response.JSON(w, http.StatusOK, map[string]any{"items": items, "active": active, "activeSnapshots": activeSnapshots, "events": events, "nextCursor": nextCursor})
}

func (h *SnapshotsHandler) Events(w http.ResponseWriter, r *http.Request) {
	env, err := environment.Parse(r.URL.Query().Get("environment"))
	if err != nil {
		response.Error(w, r, http.StatusBadRequest, "invalid_environment", err.Error())
		return
	}
	limit, err := response.ParseLimit(r.URL.Query().Get("limit"), 50, 200)
	if err != nil {
		response.Error(w, r, http.StatusBadRequest, "invalid_limit", err.Error())
		return
	}
	var before *uuid.UUID
	if raw := r.URL.Query().Get("before"); raw != "" {
		id, parseErr := uuid.Parse(raw)
		if parseErr != nil {
			response.Error(w, r, http.StatusBadRequest, "invalid_cursor", "Некорректный указатель истории")
			return
		}
		before = &id
	}
	items, err := h.service.EventsPage(r.Context(), env, limit+1, before)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	var next *uuid.UUID
	if len(items) > limit {
		cursor := items[limit-1].ID
		next = &cursor
		items = items[:limit]
	}
	response.JSON(w, http.StatusOK, map[string]any{"items": items, "nextCursor": next})
}

func (h *SnapshotsHandler) Promote(w http.ResponseWriter, r *http.Request) {
	actor, _ := principal.FromContext(r.Context())
	id, ok := snapshotID(w, r)
	if !ok {
		return
	}
	record, err := h.service.Promote(r.Context(), actor.TokenID, id)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	response.JSON(w, http.StatusOK, record)
}

type rollbackRequest struct {
	Environment string `json:"environment"`
}

func (h *SnapshotsHandler) Deploy(w http.ResponseWriter, r *http.Request) {
	actor, _ := principal.FromContext(r.Context())
	id, ok := snapshotID(w, r)
	if !ok {
		return
	}
	var req rollbackRequest
	if err := response.DecodeJSON(w, r, &req, h.maxJSON); err != nil {
		response.WriteDecodeError(w, r, err)
		return
	}
	env, err := environment.Parse(req.Environment)
	if err != nil {
		response.Error(w, r, http.StatusBadRequest, "invalid_environment", err.Error())
		return
	}
	record, err := h.service.Deploy(r.Context(), actor.TokenID, id, env)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	response.JSON(w, http.StatusOK, record)
}

func (h *SnapshotsHandler) Rollback(w http.ResponseWriter, r *http.Request) {
	actor, _ := principal.FromContext(r.Context())
	id, ok := snapshotID(w, r)
	if !ok {
		return
	}
	var req rollbackRequest
	if err := response.DecodeJSON(w, r, &req, h.maxJSON); err != nil {
		response.WriteDecodeError(w, r, err)
		return
	}
	env, err := environment.Parse(req.Environment)
	if err != nil {
		response.Error(w, r, http.StatusBadRequest, "invalid_environment", err.Error())
		return
	}
	record, err := h.service.Rollback(r.Context(), actor.TokenID, id, env)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	response.JSON(w, http.StatusOK, record)
}

func (h *SnapshotsHandler) Runtime(w http.ResponseWriter, r *http.Request) {
	requestPath := r.URL.Query().Get("path")
	if requestPath == "" {
		requestPath = "/"
	}
	query := url.Values{}
	if search := r.URL.Query().Get("search"); search != "" {
		var err error
		query, err = url.ParseQuery(search)
		if err != nil {
			response.Error(w, r, http.StatusBadRequest, "invalid_search", "Search parameters are invalid")
			return
		}
	}
	composition, err := h.service.Runtime(r.Context(), environment.FromContext(r.Context()), requestPath, query)
	if err != nil {
		h.writePublicError(w, r, err)
		return
	}
	response.JSON(w, http.StatusOK, composition)
}

func (h *SnapshotsHandler) HTMLShell(ctx context.Context, env environment.Environment, requestPath string) ([]byte, bool, error) {
	return h.service.HTMLShell(ctx, env, requestPath)
}

func (h *SnapshotsHandler) ContentCatalog(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.ContentCatalog(r.Context())
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	response.JSON(w, http.StatusOK, map[string]any{"items": items})
}

// ContentSnapshots exposes immutable snapshot metadata to all authenticated
// content editors. Environment deployment controls remain site-manager only.
func (h *SnapshotsHandler) ContentSnapshots(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.ContentSnapshots(r.Context())
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	response.JSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *SnapshotsHandler) SnapshotContentCatalog(w http.ResponseWriter, r *http.Request) {
	snapshotID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		response.Error(w, r, http.StatusBadRequest, "invalid_id", "Некорректный идентификатор снапшота")
		return
	}
	items, err := h.service.ContentCatalogForSnapshot(r.Context(), snapshotID)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	response.JSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *SnapshotsHandler) ContentWorkspace(w http.ResponseWriter, r *http.Request) {
	snapshotID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		response.Error(w, r, http.StatusBadRequest, "invalid_id", "Некорректный идентификатор снапшота")
		return
	}
	workspace, err := h.service.ContentWorkspace(r.Context(), snapshotID, r.PathValue("featureKey"))
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	response.JSON(w, http.StatusOK, workspace)
}

type saveContentWorkspaceRequest struct {
	Revision int64                                   `json:"revision"`
	Items    []sitebuilder.SnapshotContentDraftInput `json:"items"`
}

func (h *SnapshotsHandler) SaveContentWorkspace(w http.ResponseWriter, r *http.Request) {
	snapshotID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		response.Error(w, r, http.StatusBadRequest, "invalid_id", "Некорректный идентификатор снапшота")
		return
	}
	var req saveContentWorkspaceRequest
	if err := response.DecodeJSON(w, r, &req, h.maxJSON); err != nil {
		response.WriteDecodeError(w, r, err)
		return
	}
	actor, _ := principal.FromContext(r.Context())
	workspace, err := h.service.SaveContentWorkspace(r.Context(), actor.TokenID, snapshotID, r.PathValue("featureKey"), req.Revision, req.Items)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	response.JSON(w, http.StatusOK, workspace)
}

func (h *SnapshotsHandler) ForkSnapshotFromContent(w http.ResponseWriter, r *http.Request) {
	snapshotID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		response.Error(w, r, http.StatusBadRequest, "invalid_id", "Некорректный идентификатор снапшота")
		return
	}
	var req publishRequest
	if err := response.DecodeJSON(w, r, &req, h.maxJSON); err != nil {
		response.WriteDecodeError(w, r, err)
		return
	}
	actor, _ := principal.FromContext(r.Context())
	record, err := h.service.ForkSnapshotFromContentDraft(r.Context(), actor.TokenID, snapshotID, strings.TrimSpace(req.Name))
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	response.JSON(w, http.StatusCreated, record)
}

func (h *SnapshotsHandler) Collection(w http.ResponseWriter, r *http.Request) {
	offset, err := parseNonnegative(r.URL.Query().Get("offset"))
	if err != nil {
		response.Error(w, r, http.StatusBadRequest, "invalid_offset", "offset must be a non-negative integer")
		return
	}
	limit, err := parseNonnegative(r.URL.Query().Get("limit"))
	if err != nil {
		response.Error(w, r, http.StatusBadRequest, "invalid_limit", "limit must be a non-negative integer")
		return
	}
	var snapshotID *uuid.UUID
	if raw := r.URL.Query().Get("snapshot"); raw != "" {
		parsed, parseErr := uuid.Parse(raw)
		if parseErr != nil {
			response.Error(w, r, http.StatusBadRequest, "invalid_snapshot", "snapshot must be a valid snapshot id")
			return
		}
		snapshotID = &parsed
	}
	page, err := h.service.Collection(r.Context(), environment.FromContext(r.Context()), snapshotID, r.PathValue("key"), r.URL.Query().Get("field"), r.URL.Query().Get("value"), offset, limit)
	if err != nil {
		h.writePublicError(w, r, err)
		return
	}
	response.JSON(w, http.StatusOK, page)
}

type formRequest struct {
	Data json.RawMessage `json:"data"`
}

func (h *SnapshotsHandler) SubmitForm(w http.ResponseWriter, r *http.Request) {
	var req formRequest
	if err := response.DecodeJSON(w, r, &req, h.maxJSON); err != nil {
		response.WriteDecodeError(w, r, err)
		return
	}
	key := r.PathValue("key")
	env := environment.FromContext(r.Context())
	schemaID, schema, err := h.service.FormSchema(r.Context(), env, key)
	if err != nil {
		h.writePublicError(w, r, err)
		return
	}
	submission, err := h.forms.SubmitWithSchema(r.Context(), env, key, schemaID, schema, req.Data)
	if err != nil {
		h.writePublicError(w, r, err)
		return
	}
	response.JSON(w, http.StatusCreated, map[string]string{"id": submission.ID.String(), "status": submission.Status})
}

func (h *SnapshotsHandler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	status, code := http.StatusInternalServerError, "site_builder_error"
	switch {
	case errors.Is(err, sitebuilder.ErrInvalid):
		status, code = http.StatusBadRequest, "invalid_site_config"
	case errors.Is(err, sitebuilder.ErrConflict):
		status, code = http.StatusConflict, "site_builder_conflict"
	case errors.Is(err, sitebuilder.ErrNotFound):
		status, code = http.StatusNotFound, "not_found"
	}
	if status == http.StatusInternalServerError && h.log != nil {
		h.log.Error("site builder request failed", "request_id", response.RequestID(r.Context()), "error", err)
	}
	response.Error(w, r, status, code, err.Error())
}

func (h *SnapshotsHandler) writePublicError(w http.ResponseWriter, r *http.Request, err error) {
	status, code := http.StatusInternalServerError, "runtime_error"
	switch {
	case errors.Is(err, sitebuilder.ErrNotFound):
		status, code = http.StatusNotFound, "not_found"
	case errors.Is(err, sitebuilder.ErrSnapshotUnavailable):
		status, code = http.StatusServiceUnavailable, "snapshot_unavailable"
	case errors.Is(err, sitebuilder.ErrConflict):
		status, code = http.StatusConflict, "ambiguous_content"
	case errors.Is(err, sitebuilder.ErrInvalid):
		status, code = http.StatusBadRequest, "invalid_query"
	case errors.Is(err, formsFeature.ErrValidation):
		status, code = http.StatusBadRequest, "invalid_form"
	case errors.Is(err, formsFeature.ErrNotFound):
		status, code = http.StatusNotFound, "form_not_found"
	}
	if status == http.StatusInternalServerError && h.log != nil {
		h.log.Error("public runtime request failed", "request_id", response.RequestID(r.Context()), "error", err)
	}
	response.Error(w, r, status, code, err.Error())
}

func snapshotID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		response.Error(w, r, http.StatusBadRequest, "invalid_id", "Некорректный идентификатор снапшота")
		return uuid.Nil, false
	}
	return id, true
}

func parseNonnegative(raw string) (int, error) {
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0, errors.New("invalid non-negative integer")
	}
	return n, nil
}
