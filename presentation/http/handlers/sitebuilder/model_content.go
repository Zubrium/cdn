package sitebuilder

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	featureSitebuilder "websiteengine/features/sitebuilder"
	"websiteengine/pkg/principal"
	"websiteengine/pkg/response"
)

type modelContentRevisionRequest struct {
	Revision int64 `json:"revision"`
}

func (h *SnapshotsHandler) ModelContentWorkspace(w http.ResponseWriter, r *http.Request) {
	id, ok := snapshotID(w, r)
	if !ok {
		return
	}
	workspace, err := h.service.ModelContentWorkspace(r.Context(), id)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	response.JSON(w, http.StatusOK, workspace)
}

func (h *SnapshotsHandler) SaveModelContentWorkspace(w http.ResponseWriter, r *http.Request) {
	id, ok := snapshotID(w, r)
	if !ok {
		return
	}
	var input featureSitebuilder.ModelContentDraftInput
	if err := response.DecodeJSON(w, r, &input, h.maxJSON); err != nil {
		response.WriteDecodeError(w, r, err)
		return
	}
	actor, _ := principal.FromContext(r.Context())
	workspace, err := h.service.SaveModelContentWorkspace(r.Context(), actor.TokenID, id, input)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	response.JSON(w, http.StatusOK, workspace)
}

func (h *SnapshotsHandler) PublishModelContentWorkspace(w http.ResponseWriter, r *http.Request) {
	id, ok := snapshotID(w, r)
	if !ok {
		return
	}
	var req modelContentRevisionRequest
	if err := response.DecodeJSON(w, r, &req, h.maxJSON); err != nil {
		response.WriteDecodeError(w, r, err)
		return
	}
	actor, _ := principal.FromContext(r.Context())
	workspace, err := h.service.PublishModelContentWorkspace(r.Context(), actor.TokenID, id, req.Revision)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	response.JSON(w, http.StatusOK, workspace)
}

func (h *SnapshotsHandler) ModelContentHistory(w http.ResponseWriter, r *http.Request) {
	id, ok := snapshotID(w, r)
	if !ok {
		return
	}
	limit, err := h.modelHistoryLimit(r)
	if err != nil {
		response.Error(w, r, http.StatusBadRequest, "invalid_limit", err.Error())
		return
	}
	var before *int64
	if value := strings.TrimSpace(r.URL.Query().Get("before")); value != "" {
		parsed, parseErr := strconv.ParseInt(value, 10, 64)
		if parseErr != nil || parsed < 1 {
			response.Error(w, r, http.StatusBadRequest, "invalid_cursor", "Некорректный курсор истории")
			return
		}
		before = &parsed
	}
	items, err := h.service.ModelContentHistory(r.Context(), id, limit, before)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	response.JSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *SnapshotsHandler) RestoreModelContentPublication(w http.ResponseWriter, r *http.Request) {
	id, ok := snapshotID(w, r)
	if !ok {
		return
	}
	publicationID, err := uuid.Parse(r.PathValue("publicationId"))
	if err != nil {
		response.Error(w, r, http.StatusBadRequest, "invalid_id", "Некорректный идентификатор публикации")
		return
	}
	var req modelContentRevisionRequest
	if err := response.DecodeJSON(w, r, &req, h.maxJSON); err != nil {
		response.WriteDecodeError(w, r, err)
		return
	}
	actor, _ := principal.FromContext(r.Context())
	workspace, err := h.service.RestoreModelContentPublication(r.Context(), actor.TokenID, id, publicationID, req.Revision)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	response.JSON(w, http.StatusOK, workspace)
}

func (h *SnapshotsHandler) modelHistoryLimit(r *http.Request) (int, error) {
	if h.pageDefault < 1 || h.pageMax < 1 {
		return 0, errors.New("история контента не настроена")
	}
	raw := strings.TrimSpace(r.URL.Query().Get("limit"))
	if raw == "" {
		return h.pageDefault, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 || value > h.pageMax {
		return 0, errors.New("limit должен быть положительным и не превышать настроенный максимум")
	}
	return value, nil
}
