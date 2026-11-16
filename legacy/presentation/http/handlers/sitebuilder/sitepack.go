package sitebuilder

import (
	"errors"
	"log/slog"
	"net/http"

	"websiteengine/features/sitebuilder"
	"websiteengine/pkg/principal"
	"websiteengine/pkg/response"
)

type SitepackHandler struct {
	service *sitebuilder.SnapshotService
	options sitebuilder.SitepackOptions
	log     *slog.Logger
}

func NewSitepackHandler(service *sitebuilder.SnapshotService, options sitebuilder.SitepackOptions, log *slog.Logger) *SitepackHandler {
	return &SitepackHandler{service, options, log}
}

func (h *SitepackHandler) Limits(w http.ResponseWriter, r *http.Request) {
	response.JSON(w, http.StatusOK, map[string]int64{
		"maxBytes":             h.options.MaxBytes,
		"browserFallbackBytes": h.options.MaxBrowserBlobBytes,
	})
}

func (h *SitepackHandler) Export(w http.ResponseWriter, r *http.Request) {
	id, ok := snapshotID(w, r)
	if !ok {
		return
	}
	// Validate the requested record before sending response headers. Later
	// streaming failures leave an incomplete ZIP, which the importer rejects.
	if _, err := h.service.Snapshot(r.Context(), id); err != nil {
		response.Error(w, r, http.StatusNotFound, "not_found", "Снапшот не найден")
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="snapshot-`+id.String()+`.sitepack"`)
	w.Header().Set("Cache-Control", "private, no-store")
	if err := h.service.ExportSitepack(r.Context(), id, w, h.options); err != nil {
		if h.log != nil {
			h.log.Error("sitepack export interrupted", "snapshot_id", id, "error", err)
		}
		panic(http.ErrAbortHandler)
	}
}

func (h *SitepackHandler) Import(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Content-Type") != "application/zip" {
		response.Error(w, r, http.StatusUnsupportedMediaType, "unsupported_media_type", "Ожидается архив application/zip")
		return
	}
	actor, _ := principal.FromContext(r.Context())
	report, err := h.service.ImportSitepack(r.Context(), actor.TokenID, http.MaxBytesReader(w, r.Body, h.options.MaxBytes), h.options)
	if err != nil {
		if h.log != nil {
			h.log.Warn("sitepack import failed", "error", err)
		}
		writeSitepackImportError(w, r, err)
		return
	}
	response.JSON(w, http.StatusCreated, report)
}

func writeSitepackImportError(w http.ResponseWriter, r *http.Request, err error) {
	var rejected *sitebuilder.SitepackImportFailure
	if errors.As(err, &rejected) {
		diagnostic := rejected.Diagnostic
		response.Error(w, r, http.StatusBadRequest, "invalid_sitepack", "Импорт отклонён целиком; новый снапшот не создан.", response.ErrorDetail{
			Path: diagnostic.Path, Code: diagnostic.Code, Message: diagnostic.Message,
		})
		return
	}
	response.Error(w, r, http.StatusInternalServerError, "sitepack_import_failed", "Не удалось завершить импорт. Проверьте список снапшотов перед повторной попыткой.")
}
