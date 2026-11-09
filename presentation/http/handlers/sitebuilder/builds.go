package sitebuilder

import (
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"websiteengine/features/sitebuilder"
	"websiteengine/pkg/principal"
	"websiteengine/pkg/response"
)

type BuildsHandler struct {
	service  *sitebuilder.BuildService
	log      *slog.Logger
	maxBytes int64
}

func NewBuildsHandler(service *sitebuilder.BuildService, log *slog.Logger, maxBytes int64) *BuildsHandler {
	return &BuildsHandler{service: service, log: log, maxBytes: maxBytes}
}

func (h *BuildsHandler) List(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.List(r.Context())
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	response.JSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *BuildsHandler) Import(w http.ResponseWriter, r *http.Request) {
	actor, ok := principal.FromContext(r.Context())
	if !ok {
		response.Error(w, r, http.StatusUnauthorized, "unauthorized", "Требуется авторизация")
		return
	}
	if h.maxBytes <= 0 {
		response.Error(w, r, http.StatusInternalServerError, "configuration_error", "Feature archive size limit is not configured")
		return
	}
	if r.ContentLength > h.maxBytes {
		response.Error(w, r, http.StatusRequestEntityTooLarge, "artifact_too_large", "Архив фичи превышает допустимый размер")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, h.maxBytes+1))
	if err != nil {
		response.Error(w, r, http.StatusBadRequest, "invalid_artifact", "Не удалось прочитать архив фичи")
		return
	}
	if int64(len(body)) > h.maxBytes {
		response.Error(w, r, http.StatusRequestEntityTooLarge, "artifact_too_large", "Архив фичи превышает допустимый размер")
		return
	}
	if mediaType := strings.ToLower(strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0])); mediaType != "application/zip" && mediaType != "application/octet-stream" {
		response.Error(w, r, http.StatusUnsupportedMediaType, "invalid_content_type", "Ожидается ZIP-архив фичи")
		return
	}
	item, created, err := h.service.Import(r.Context(), actor.TokenID, body)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	response.JSON(w, status, item)
}

func (h *BuildsHandler) Asset(w http.ResponseWriter, r *http.Request) {
	digest, assetPath := r.PathValue("digest"), r.PathValue("asset")
	body, contentType, err := h.service.Asset(r.Context(), digest, assetPath)
	if err != nil {
		if err == pgx.ErrNoRows {
			http.NotFound(w, r)
			return
		}
		h.writeError(w, r, err)
		return
	}
	defer body.Close()
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = io.Copy(w, body)
}

func (h *BuildsHandler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	h.log.Error("feature build request failed", "request_id", response.RequestID(r.Context()), "error", err)
	response.Error(w, r, http.StatusBadRequest, "feature_build_error", err.Error())
}
