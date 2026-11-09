package media

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"websiteengine/features/media"
	"websiteengine/features/sitebuilder"
	"websiteengine/pkg/principal"

	"github.com/google/uuid"

	"websiteengine/pkg/response"
)

type FileHandler interface {
	ServeMedia(http.ResponseWriter, *http.Request)
	UploadAdminMedia(http.ResponseWriter, *http.Request)
	DeleteAdminMedia(http.ResponseWriter, *http.Request)
	AdminMediaAccess(http.ResponseWriter, *http.Request) bool
}

type Handler struct {
	files            FileHandler
	service          *media.Service
	profiles         MediaProfileProvider
	log              *slog.Logger
	maxUpload        int64
	pageDefault      int
	pageMax          int
	redirectCacheTTL time.Duration
}

type MediaProfileProvider interface {
	MediaProfile(context.Context, uuid.UUID, string, string, string, string, string) (sitebuilder.MediaProfileLookup, error)
}

func NewHandler(service *media.Service, log *slog.Logger, maxUpload int64) *Handler {
	return &Handler{service: service, log: log, maxUpload: maxUpload}
}

// SetRedirectCacheTTL applies the configured TTL to legacy public media
// redirects. The canonical file service serves directly and manages its own
// no-cache policy.
func (h *Handler) SetRedirectCacheTTL(ttl time.Duration) error {
	if ttl < time.Second || ttl%time.Second != 0 {
		return fmt.Errorf("MEDIA_REDIRECT_CACHE_TTL must be a positive whole number of seconds")
	}
	h.redirectCacheTTL = ttl
	return nil
}

func (h *Handler) SetFileHandler(handler FileHandler) { h.files = handler }

func (h *Handler) SetProfileProvider(provider MediaProfileProvider) {
	h.profiles = provider
}

// SetPagination binds the media list to operator-configured page limits.
func (h *Handler) SetPagination(defaultPage, maxPage int) error {
	if defaultPage < 1 || maxPage < defaultPage {
		return fmt.Errorf("media pagination limits are invalid")
	}
	h.pageDefault, h.pageMax = defaultPage, maxPage
	return nil
}

func (h *Handler) Upload(w http.ResponseWriter, r *http.Request) {
	if h.files != nil {
		h.files.UploadAdminMedia(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, h.maxUpload+(1<<20))
	if err := r.ParseMultipartForm(h.maxUpload + 1); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) || strings.Contains(err.Error(), "too large") {
			response.Error(w, r, http.StatusRequestEntityTooLarge, "file_too_large", "Файл превышает допустимый размер")
			return
		}
		response.Error(w, r, http.StatusBadRequest, "invalid_multipart", "Ожидается multipart/form-data с полем file")
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		response.Error(w, r, http.StatusBadRequest, "file_required", "Поле file обязательно")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, h.maxUpload+1))
	if err != nil {
		response.Error(w, r, http.StatusBadRequest, "invalid_file", "Не удалось прочитать файл")
		return
	}
	if int64(len(data)) > h.maxUpload {
		response.Error(w, r, http.StatusRequestEntityTooLarge, "file_too_large", "Файл превышает допустимый размер")
		return
	}
	m, err := h.service.Upload(r.Context(), currentActor(r), header.Filename, data)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	response.JSON(w, http.StatusCreated, m)
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	if h.pageDefault < 1 || h.pageMax < h.pageDefault {
		response.Error(w, r, http.StatusInternalServerError, "configuration_error", "Pagination limits are not configured")
		return
	}
	q := r.URL.Query()
	limit, err := response.ParseLimit(q.Get("limit"), h.pageDefault, h.pageMax)
	if err != nil {
		response.Error(w, r, http.StatusBadRequest, "invalid_limit", err.Error())
		return
	}
	filters, err := parseListFilters(q)
	if err != nil {
		response.Error(w, r, http.StatusBadRequest, "invalid_filters", err.Error())
		return
	}
	items, next, err := h.service.List(r.Context(), filters, q.Get("cursor"), limit)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	response.JSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": nullable(next)})
}

// parseListFilters reads the optional list filters from the query string.
func parseListFilters(q url.Values) (media.ListFilters, error) {
	f := media.ListFilters{
		Status: q.Get("status"),
		Search: q.Get("q"),
		Type:   q.Get("type"),
		Sort:   q.Get("sort"),
	}
	var err error
	if f.CreatedFrom, err = parseTimeParam(q.Get("created_from"), false); err != nil {
		return f, fmt.Errorf("created_from должен быть датой в формате YYYY-MM-DD или RFC 3339")
	}
	if f.CreatedTo, err = parseTimeParam(q.Get("created_to"), true); err != nil {
		return f, fmt.Errorf("created_to должен быть датой в формате YYYY-MM-DD или RFC 3339")
	}
	if f.MinSize, err = parseSizeParam(q.Get("min_size")); err != nil {
		return f, fmt.Errorf("min_size должен быть положительным числом байт")
	}
	if f.MaxSize, err = parseSizeParam(q.Get("max_size")); err != nil {
		return f, fmt.Errorf("max_size должен быть положительным числом байт")
	}
	if f.MinSize != nil && f.MaxSize != nil && *f.MinSize > *f.MaxSize {
		return f, fmt.Errorf("min_size не должен превышать max_size")
	}
	return f, nil
}

// parseTimeParam accepts a plain date or a full RFC 3339 instant. A plain
// date covers the whole day, so for the end of a range the exclusive upper
// bound is midnight of the next day.
func parseTimeParam(raw string, endOfDay bool) (*time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		t = t.UTC()
		return &t, nil
	}
	if t, err := time.Parse("2006-01-02", raw); err == nil {
		t = t.UTC()
		if endOfDay {
			t = t.AddDate(0, 0, 1)
		}
		return &t, nil
	}
	return nil, errors.New("invalid date")
}

// parseSizeParam reads a byte count.
func parseSizeParam(raw string) (*int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	size, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || size < 0 {
		return nil, errors.New("invalid size")
	}
	return &size, nil
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	if h.files != nil && !h.files.AdminMediaAccess(w, r) {
		return
	}
	id, ok := mediaID(w, r)
	if !ok {
		return
	}
	m, err := h.service.Get(r.Context(), id)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	response.JSON(w, http.StatusOK, m)
}

type updateMediaRequest struct {
	Filename string `json:"filename"`
}

// Update renames the display metadata of a media object.
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	if h.files != nil && !h.files.AdminMediaAccess(w, r) {
		return
	}
	id, ok := mediaID(w, r)
	if !ok {
		return
	}
	var req updateMediaRequest
	if err := response.DecodeJSON(w, r, &req, h.maxUpload); err != nil {
		response.WriteDecodeError(w, r, err)
		return
	}
	m, err := h.service.Rename(r.Context(), id, req.Filename)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	response.JSON(w, http.StatusOK, m)
}

func (h *Handler) Retry(w http.ResponseWriter, r *http.Request) {
	if h.files != nil && !h.files.AdminMediaAccess(w, r) {
		return
	}
	id, ok := mediaID(w, r)
	if !ok {
		return
	}
	m, err := h.service.Retry(r.Context(), id)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	response.JSON(w, http.StatusOK, m)
}

type renditionRequest struct {
	SnapshotID   uuid.UUID `json:"snapshotId"`
	FeatureKey   string    `json:"featureKey"`
	BuildVersion string    `json:"buildVersion"`
	SchemaKind   string    `json:"schemaKind"`
	FieldPath    string    `json:"fieldPath"`
	BuildDigest  string    `json:"buildDigest"`
}

func (h *Handler) PrepareRendition(w http.ResponseWriter, r *http.Request) {
	if h.files != nil && !h.files.AdminMediaAccess(w, r) {
		return
	}
	id, ok := mediaID(w, r)
	if !ok {
		return
	}
	if h.profiles == nil {
		response.Error(w, r, http.StatusServiceUnavailable, "media_profiles_unavailable", "Схема медиа-полей недоступна")
		return
	}
	var req renditionRequest
	if err := response.DecodeJSON(w, r, &req, 32<<10); err != nil {
		response.WriteDecodeError(w, r, err)
		return
	}
	profile, err := h.profiles.MediaProfile(r.Context(), req.SnapshotID, req.FeatureKey, req.BuildVersion, req.SchemaKind, req.FieldPath, req.BuildDigest)
	if err != nil {
		h.writeProfileError(w, r, err)
		return
	}
	if !profile.HasProfile {
		profile.Profile = media.FieldProfile{Accept: "file"}
	}
	state, err := h.service.Prepare(r.Context(), id, profile.Profile)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	state.BuildDigest = profile.BuildDigest
	response.JSON(w, http.StatusAccepted, state)
}

func (h *Handler) RenditionState(w http.ResponseWriter, r *http.Request) {
	if h.files != nil && !h.files.AdminMediaAccess(w, r) {
		return
	}
	id, ok := mediaID(w, r)
	if !ok {
		return
	}
	if h.profiles == nil {
		response.Error(w, r, http.StatusServiceUnavailable, "media_profiles_unavailable", "Схема медиа-полей недоступна")
		return
	}
	query := r.URL.Query()
	snapshotID, err := uuid.Parse(query.Get("snapshotId"))
	if err != nil || snapshotID == uuid.Nil {
		response.Error(w, r, http.StatusBadRequest, "invalid_snapshot_id", "Укажите корректный ID снапшота")
		return
	}
	profile, err := h.profiles.MediaProfile(r.Context(), snapshotID, query.Get("featureKey"), query.Get("buildVersion"), query.Get("schemaKind"), query.Get("fieldPath"), query.Get("buildDigest"))
	if err != nil {
		h.writeProfileError(w, r, err)
		return
	}
	if !profile.HasProfile {
		response.JSON(w, http.StatusOK, media.RenditionState{MediaID: id.String(), Status: "ready", Outputs: map[string]media.Output{}})
		return
	}
	state, err := h.service.State(r.Context(), id, profile.Profile)
	if err != nil {
		if errors.Is(err, media.ErrNotFound) {
			profileHash, hashErr := media.ProfileHash(profile.Profile)
			if hashErr == nil {
				state = media.RenditionState{MediaID: id.String(), ProfileHash: profileHash, Status: "pending", Outputs: map[string]media.Output{}}
				state.BuildDigest = profile.BuildDigest
				response.JSON(w, http.StatusOK, state)
				return
			}
		}
		h.writeError(w, r, err)
		return
	}
	state.BuildDigest = profile.BuildDigest
	response.JSON(w, http.StatusOK, state)
}

func (h *Handler) writeProfileError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, sitebuilder.ErrNotFound):
		response.Error(w, r, http.StatusNotFound, "media_field_not_found", "Поле медиа не найдено в выбранном снапшоте")
	case errors.Is(err, sitebuilder.ErrConflict):
		response.Error(w, r, http.StatusConflict, "feature_build_changed", err.Error())
	case errors.Is(err, sitebuilder.ErrInvalid):
		response.Error(w, r, http.StatusUnprocessableEntity, "invalid_media_field", err.Error())
	case errors.Is(err, sitebuilder.ErrSnapshotUnavailable):
		response.Error(w, r, http.StatusConflict, "snapshot_unavailable", "Выбранный снапшот недоступен")
	default:
		h.writeError(w, r, err)
	}
}

func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	if h.files != nil {
		h.files.DeleteAdminMedia(w, r)
		return
	}
	id, ok := mediaID(w, r)
	if !ok {
		return
	}
	if err := h.service.Delete(r.Context(), id); err != nil {
		h.writeError(w, r, err)
		return
	}
	response.NoContent(w)
}

// Redirect responds with 302 to a public object URL. No user-supplied URL is
// ever used, which prevents open redirects.
func (h *Handler) Redirect(w http.ResponseWriter, r *http.Request) {
	if h.files == nil && (h.redirectCacheTTL < time.Second || h.redirectCacheTTL%time.Second != 0) {
		response.Error(w, r, http.StatusInternalServerError, "configuration_error", "Media redirect cache TTL is not configured")
		return
	}
	if h.files != nil {
		h.files.ServeMedia(w, r)
		return
	}
	id, err := uuid.Parse(r.PathValue("key"))
	if err != nil {
		response.Error(w, r, http.StatusNotFound, "not_found", "Медиаобъект не найден")
		return
	}
	var url string
	if profileHash := r.URL.Query().Get("profile"); profileHash != "" {
		url, err = h.service.RedirectRenditionURL(r.Context(), id, profileHash, r.URL.Query().Get("variant"))
	} else {
		url, err = h.service.RedirectURL(r.Context(), id, r.URL.Query().Get("variant"))
	}
	if err != nil {
		if errors.Is(err, media.ErrNotFound) {
			response.Error(w, r, http.StatusNotFound, "not_found", "Медиаобъект не найден")
			return
		}
		h.log.Error("media redirect failed", "request_id", response.RequestID(r.Context()), "error", err)
		response.Error(w, r, http.StatusInternalServerError, "internal_error", "Внутренняя ошибка сервера")
		return
	}
	w.Header().Set("Cache-Control", redirectCacheControl(h.redirectCacheTTL))
	w.Header().Set("Location", url)
	w.WriteHeader(http.StatusFound)
}

func redirectCacheControl(ttl time.Duration) string {
	return fmt.Sprintf("public, max-age=%d", int64(ttl/time.Second))
}

// SystemAsset streams a font or logo through the engine origin so public
// themes do not depend on bucket CORS configuration. The route's CORS
// middleware owns origin policy; this handler must not override it.
func (h *Handler) SystemAsset(w http.ResponseWriter, r *http.Request) {
	if h.files != nil {
		h.files.ServeMedia(w, r)
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	media, reader, err := h.service.ReadAsset(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer reader.Close()
	w.Header().Set("Content-Type", media.ContentType)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = io.Copy(w, reader)
}

func mediaID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		response.Error(w, r, http.StatusNotFound, "not_found", "Медиаобъект не найден")
		return uuid.Nil, false
	}
	return id, true
}

func currentActor(r *http.Request) principal.Principal {
	p, _ := principal.FromContext(r.Context())
	return p
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	var validationErr *media.ValidationError
	var conflictErr *media.ConflictError
	switch {
	case errors.As(err, &validationErr):
		code := "validation_failed"
		status := http.StatusUnprocessableEntity
		if strings.Contains(validationErr.Message, "тип") {
			status = http.StatusUnsupportedMediaType
			code = "unsupported_media_type"
		} else if strings.Contains(validationErr.Message, "размер") {
			status = http.StatusRequestEntityTooLarge
			code = "file_too_large"
		}
		response.Error(w, r, status, code, validationErr.Message)
	case errors.As(err, &conflictErr):
		response.Error(w, r, http.StatusConflict, "conflict", conflictErr.Message)
	case errors.Is(err, media.ErrProcessing):
		response.Error(w, r, http.StatusUnprocessableEntity, "media_processing_failed", "Не удалось обработать медиафайл")
	case errors.Is(err, media.ErrNotFound):
		response.Error(w, r, http.StatusNotFound, "not_found", "Медиаобъект не найден")
	default:
		h.log.Error("media operation failed", "request_id", response.RequestID(r.Context()), "error", err)
		response.Error(w, r, http.StatusInternalServerError, "internal_error", "Внутренняя ошибка сервера")
	}
}
