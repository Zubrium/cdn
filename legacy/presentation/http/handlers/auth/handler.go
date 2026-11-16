package auth

import (
	"errors"

	"log/slog"
	"net/http"
	"time"
	"websiteengine/features/auth"
	"websiteengine/pkg/principal"

	"github.com/google/uuid"

	"websiteengine/pkg/response"
)

type Handler struct {
	service *auth.Service
	log     *slog.Logger
	maxJSON int64
}

func NewHandler(service *auth.Service, log *slog.Logger, maxJSON int64) *Handler {
	return &Handler{service: service, log: log, maxJSON: maxJSON}
}

func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	p, ok := principal.FromContext(r.Context())
	if !ok {
		response.Error(w, r, http.StatusUnauthorized, "unauthorized", "Требуется авторизация")
		return
	}
	response.JSON(w, http.StatusOK, p)
}

type createTokenRequest struct {
	Name      string     `json:"name"`
	Role      string     `json:"role"`
	ExpiresAt *time.Time `json:"expires_at"`
}

type createdTokenResponse struct {
	ID        uuid.UUID  `json:"id"`
	Name      string     `json:"name"`
	Token     string     `json:"token"`
	Role      string     `json:"role"`
	ExpiresAt *time.Time `json:"expires_at"`
	CreatedAt time.Time  `json:"created_at"`
}

func (h *Handler) CreateToken(w http.ResponseWriter, r *http.Request) {
	// The bearer secret is returned only once; never allow an intermediary or
	// browser cache to persist this response.
	w.Header().Set("Cache-Control", "no-store")
	p, _ := principal.FromContext(r.Context())
	var req createTokenRequest
	if err := response.DecodeJSON(w, r, &req, h.maxJSON); err != nil {
		response.WriteDecodeError(w, r, err)
		return
	}
	created, err := h.service.Create(r.Context(), p, req.Name, req.Role, req.ExpiresAt)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	response.JSON(w, http.StatusCreated, createdTokenResponse{
		ID:        created.Token.ID,
		Name:      created.Token.Name,
		Token:     created.Secret,
		Role:      string(created.Token.Role),
		ExpiresAt: created.Token.ExpiresAt,
		CreatedAt: created.Token.CreatedAt,
	})
}

func (h *Handler) ListTokens(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	p, _ := principal.FromContext(r.Context())
	cursor := r.URL.Query().Get("cursor")
	if len(cursor) > 256 {
		h.writeError(w, r, auth.ErrValidation)
		return
	}
	items, next, err := h.service.List(r.Context(), p, cursor)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	var nextCursor any
	if next != "" {
		nextCursor = next
	}
	response.JSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": nextCursor})
}

func (h *Handler) GetToken(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	p, _ := principal.FromContext(r.Context())
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		response.Error(w, r, http.StatusNotFound, "not_found", "Токен не найден")
		return
	}
	token, err := h.service.Get(r.Context(), p, id)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	response.JSON(w, http.StatusOK, token)
}

func (h *Handler) RevokeToken(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.FromContext(r.Context())
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		response.Error(w, r, http.StatusNotFound, "not_found", "Токен не найден")
		return
	}
	if err := h.service.Revoke(r.Context(), p, id); err != nil {
		h.writeError(w, r, err)
		return
	}
	response.NoContent(w)
}

func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, auth.ErrForbidden):
		response.Error(w, r, http.StatusForbidden, "forbidden", "Недостаточно прав")
	case errors.Is(err, auth.ErrValidation):
		response.Error(w, r, http.StatusBadRequest, "validation_failed", "Некорректные параметры токена")
	case errors.Is(err, auth.ErrNotFound):
		response.Error(w, r, http.StatusNotFound, "not_found", "Токен не найден")
	default:
		h.log.Error("token operation failed", "request_id", response.RequestID(r.Context()), "error", err)
		response.Error(w, r, http.StatusInternalServerError, "internal_error", "Внутренняя ошибка сервера")
	}
}
