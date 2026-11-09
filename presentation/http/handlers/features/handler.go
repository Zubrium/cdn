package features

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"websiteengine/features/features"
	"websiteengine/pkg/response"
)

type Handler struct {
	service     *features.Service
	log         *slog.Logger
	pageDefault int
	pageMax     int
}

func NewHandler(service *features.Service, log *slog.Logger) *Handler {
	return &Handler{service: service, log: log}
}

// SetPagination binds list endpoints to operator-configured page limits.
func (h *Handler) SetPagination(defaultPage, maxPage int) error {
	if defaultPage < 1 || maxPage < defaultPage {
		return errors.New("feature pagination limits are invalid")
	}
	h.pageDefault, h.pageMax = defaultPage, maxPage
	return nil
}

// List exposes feature identities for submission filters. It never returns or
// edits a global content schema; the active schema is pinned by each snapshot.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	if h.pageDefault < 1 || h.pageMax < h.pageDefault {
		response.Error(w, r, http.StatusInternalServerError, "configuration_error", "Pagination limits are not configured")
		return
	}
	limit, err := response.ParseLimit(r.URL.Query().Get("limit"), h.pageDefault, h.pageMax)
	if err != nil {
		response.Error(w, r, http.StatusBadRequest, "invalid_limit", err.Error())
		return
	}
	items, next, err := h.service.List(r.Context(), r.URL.Query().Get("status"), r.URL.Query().Get("type"), r.URL.Query().Get("q"), r.URL.Query().Get("cursor"), limit)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	response.JSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": nullableString(next)})
}

// Versions returns immutable manifest schemas associated with imported builds
// so historical form submissions can retain their original field labels.
func (h *Handler) Versions(w http.ResponseWriter, r *http.Request) {
	if h.pageDefault < 1 || h.pageMax < h.pageDefault {
		response.Error(w, r, http.StatusInternalServerError, "configuration_error", "Pagination limits are not configured")
		return
	}
	id, ok := featureID(w, r)
	if !ok {
		return
	}
	limit, err := response.ParseLimit(r.URL.Query().Get("limit"), h.pageDefault, h.pageMax)
	if err != nil {
		response.Error(w, r, http.StatusBadRequest, "invalid_limit", err.Error())
		return
	}
	versions, err := h.service.Versions(r.Context(), id, limit)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	response.JSON(w, http.StatusOK, map[string]any{"items": versions})
}

func featureID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		response.Error(w, r, http.StatusNotFound, "not_found", "Feature was not found")
		return uuid.Nil, false
	}
	return id, true
}

func nullableString(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	var validationErr *features.ValidationError
	switch {
	case errors.As(err, &validationErr):
		response.Error(w, r, http.StatusBadRequest, "validation_failed", validationErr.Message)
	case errors.Is(err, features.ErrNotFound):
		response.Error(w, r, http.StatusNotFound, "not_found", "Feature was not found")
	default:
		h.log.Error("feature query failed", "request_id", response.RequestID(r.Context()), "error", err)
		response.Error(w, r, http.StatusInternalServerError, "internal_error", "Internal server error")
	}
}
