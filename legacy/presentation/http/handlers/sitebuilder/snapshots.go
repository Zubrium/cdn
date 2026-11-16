package sitebuilder

import (
	"encoding/json"
	"errors"
	"fmt"
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
	service     *sitebuilder.SnapshotService
	forms       *formsFeature.Service
	log         *slog.Logger
	maxJSON     int64
	pageDefault int
	pageMax     int
}

func NewSnapshotsHandler(service *sitebuilder.SnapshotService, forms *formsFeature.Service, log *slog.Logger, maxJSON int64) *SnapshotsHandler {
	return &SnapshotsHandler{service: service, forms: forms, log: log, maxJSON: maxJSON}
}

// SetPagination applies operator-configured bounds to snapshot list endpoints.
func (h *SnapshotsHandler) SetPagination(defaultPage, maxPage int) error {
	if validPagination(defaultPage, maxPage) == false {
		return fmt.Errorf("invalid snapshot page limits")
	}
	h.pageDefault, h.pageMax = defaultPage, maxPage
	return nil
}

func (h *SnapshotsHandler) SnapshotThemeCSS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Robots-Tag", "noindex")
	w.Header().Set("Cache-Control", "no-store")
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.writeThemeError(w, r, err)
		return
	}
	branch := environment.FromContext(r.Context())
	if !branch.Valid() {
		h.writeThemeError(w, r, sitebuilder.ErrNotFound)
		return
	}
	branchID, err := uuid.Parse(branch.String())
	if err != nil {
		h.writeThemeError(w, r, sitebuilder.ErrNotFound)
		return
	}
	css, err := h.service.SnapshotThemeStylesheetForBranch(r.Context(), branchID, id)
	if err != nil {
		h.writeThemeError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "public, max-age=0, must-revalidate")
	_, _ = w.Write([]byte(css))
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

// PreviewRelationUnlink is a read-only, authenticated preview of the content
// and placements affected by removing a structural relation.

// UnlinkContentRelation explicitly forks the selected snapshot through the
// service's previewed revision tuple. IDs come from the route, never the form.

// PreviewRelationSourceEntryDeletion lets content editors inspect the
// published/draft relation impact before they remove a collection record.

func (h *SnapshotsHandler) Create(w http.ResponseWriter, r *http.Request) {
	actor, _ := principal.FromContext(r.Context())
	var req sitebuilder.CreateSnapshotInput
	if err := response.DecodeJSON(w, r, &req, h.maxJSON); err != nil {
		response.WriteDecodeError(w, r, err)
		return
	}
	var record sitebuilder.SnapshotRecord
	var err error
	record, err = h.service.CreateSnapshot(r.Context(), actor.TokenID, req)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	response.JSON(w, http.StatusCreated, record)
}

func (h *SnapshotsHandler) List(w http.ResponseWriter, r *http.Request) {
	if !validPagination(h.pageDefault, h.pageMax) {
		response.Error(w, r, http.StatusInternalServerError, "configuration_error", "Pagination limits are not configured")
		return
	}
	limit, err := response.ParseLimit(r.URL.Query().Get("limit"), h.pageDefault, h.pageMax)
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
	branches, err := h.service.Branches(r.Context())
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	response.JSON(w, http.StatusOK, map[string]any{"items": items, "branches": branches, "nextCursor": nextCursor})
}

func (h *SnapshotsHandler) Events(w http.ResponseWriter, r *http.Request) {
	if !validPagination(h.pageDefault, h.pageMax) {
		response.Error(w, r, http.StatusInternalServerError, "configuration_error", "Pagination limits are not configured")
		return
	}
	branchID, err := uuid.Parse(strings.TrimSpace(r.URL.Query().Get("branchId")))
	if err != nil {
		response.Error(w, r, http.StatusBadRequest, "invalid_branch", "Укажите корректный branchId")
		return
	}
	env := environment.Environment(branchID.String())
	limit, err := response.ParseLimit(r.URL.Query().Get("limit"), h.pageDefault, h.pageMax)
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

func (h *SnapshotsHandler) Runtime(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Robots-Tag", "noindex")
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
	composition, pageStatus, err := h.service.PublicRuntime(r.Context(), environment.FromContext(r.Context()), requestPath, query)
	if err != nil {
		h.writePublicError(w, r, err)
		return
	}
	w.Header().Set("X-Engine-Page-Status", strconv.Itoa(pageStatus))
	response.JSON(w, http.StatusOK, composition)
}

// ContentSnapshots exposes immutable snapshot metadata to all authenticated
// content editors. Environment deployment controls remain site-manager only.
func (h *SnapshotsHandler) ContentSnapshots(w http.ResponseWriter, r *http.Request) {
	if !validPagination(h.pageDefault, h.pageMax) {
		response.Error(w, r, http.StatusInternalServerError, "configuration_error", "Pagination limits are not configured")
		return
	}
	limit, err := response.ParseLimit(r.URL.Query().Get("limit"), h.pageDefault, h.pageMax)
	if err != nil {
		response.Error(w, r, http.StatusBadRequest, "invalid_limit", err.Error())
		return
	}
	var before *int64
	if raw := r.URL.Query().Get("beforeSequence"); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value < 1 {
			response.Error(w, r, http.StatusBadRequest, "invalid_cursor", "beforeSequence должен быть положительным номером снапшота")
			return
		}
		before = &value
	}
	items, err := h.service.List(r.Context(), limit+1, before)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	var next *int64
	if len(items) > limit {
		cursor := items[limit-1].Sequence
		next = &cursor
		items = items[:limit]
	}
	response.JSON(w, http.StatusOK, map[string]any{"items": items, "nextCursor": next})
}

func validPagination(defaultPage, maxPage int) bool {
	return defaultPage > 0 && maxPage >= defaultPage
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
	submission, err := h.forms.Submit(r.Context(), env, key, req.Data)
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
