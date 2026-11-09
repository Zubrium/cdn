package files

import (
	"errors"
	"fmt"
	"github.com/google/uuid"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"
	"websiteengine/features/files"
	"websiteengine/pkg/principal"
	"websiteengine/pkg/response"
	"websiteengine/presentation/http/middleware"
)

type Handler struct {
	service    *files.Service
	log        *slog.Logger
	maxJSON    int64
	limiter    *ownerLimiter
	trustProxy bool
}

func NewHandler(service *files.Service, log *slog.Logger, maxJSON int64) *Handler {
	cfg := service.Config()
	return &Handler{service: service, log: log, maxJSON: maxJSON, limiter: newOwnerLimiter(cfg.RateLimit, cfg.RateMaxClients, cfg.RateWindow)}
}

func (h *Handler) SetTrustProxy(trust bool) { h.trustProxy = trust }

func (h *Handler) anonymousKey(r *http.Request) string {
	return "ip:" + middleware.ClientIP(r, h.trustProxy)
}

type ownerKey struct{}

func (h *Handler) RegisterServiceRoutes(mux *http.ServeMux) {
	h.register(mux, "/api/v2/files", nil)
	mux.HandleFunc("GET /api/v2/files/objects/{id}/content", h.Content)
	mux.HandleFunc("HEAD /api/v2/files/objects/{id}/content", h.Content)
}
func (h *Handler) RegisterAdminRoutes(mux *http.ServeMux, auth func(http.HandlerFunc) http.Handler) {
	h.register(mux, "/api/v2/admin/files", auth)
	mux.Handle("GET /api/v2/admin/files/limits", auth(h.limits))
	mux.Handle("GET /api/v2/admin/files/objects/{id}/content", auth(h.AdminContent))
	mux.Handle("HEAD /api/v2/admin/files/objects/{id}/content", auth(h.AdminContent))
}
func (h *Handler) register(mux *http.ServeMux, prefix string, auth func(http.HandlerFunc) http.Handler) {
	routes := []struct {
		method, path, permission string
		handler                  func(http.ResponseWriter, *http.Request, files.Owner)
	}{{"POST", "/uploads", "upload", h.create}, {"GET", "/uploads/{id}", "upload", h.status}, {"PUT", "/uploads/{id}/parts/{number}", "upload", h.part}, {"POST", "/uploads/{id}/commit", "upload", h.commit}, {"DELETE", "/uploads/{id}", "upload", h.abort}, {"GET", "", "list", h.list}, {"GET", "/objects/{id}", "read", h.get}, {"GET", "/objects/{id}/references", "delete", h.references}, {"POST", "/objects/{id}/links", "link", h.link}, {"DELETE", "/objects/{id}", "delete", h.delete}}
	for _, route := range routes {
		fn := func(w http.ResponseWriter, r *http.Request) {
			var owner files.Owner
			var err error
			if auth != nil {
				p, ok := principal.FromContext(r.Context())
				if !ok {
					h.error(w, r, files.ErrForbidden)
					return
				}
				// Deleting the shared administrator library remains admin-only, as
				// in the existing media API. Aborting an upload is not file deletion.
				if route.method == "DELETE" && route.path == "/objects/{id}" && p.Role != principal.RoleAdmin {
					h.error(w, r, files.ErrForbidden)
					return
				}
				owner = h.service.AdminOwner()
			} else {
				owner, err = h.service.Authenticate(bearer(r), route.permission)
				if err != nil {
					if !h.rateAllowed(w, r, h.anonymousKey(r)) {
						return
					}
					h.error(w, r, err)
					return
				}
			}
			if !h.rateAllowed(w, r, "owner:"+owner.ID.String()) {
				return
			}
			route.handler(w, r, owner)
		}
		var handler http.Handler = http.HandlerFunc(fn)
		if auth != nil {
			handler = auth(fn)
		}
		mux.Handle(route.method+" "+prefix+route.path, handler)
	}
}

func (h *Handler) limits(w http.ResponseWriter, r *http.Request) {
	if !h.rateAllowed(w, r, "owner:"+h.service.AdminOwner().ID.String()) {
		return
	}
	config := h.service.Config()
	response.JSON(w, http.StatusOK, map[string]any{
		"maxBytes": config.MaxBytes, "partBytes": config.PartBytes,
		"linkMaxTTLSeconds": int64(config.LinkMaxTTL / time.Second),
	})
}
func (h *Handler) create(w http.ResponseWriter, r *http.Request, o files.Owner) {
	var in files.UploadInput
	if e := response.DecodeJSON(w, r, &in, h.maxJSON); e != nil {
		response.WriteDecodeError(w, r, e)
		return
	}
	u, e := h.service.CreateUpload(r.Context(), o, in)
	if e != nil {
		h.error(w, r, e)
		return
	}
	response.JSON(w, http.StatusCreated, u)
}
func (h *Handler) status(w http.ResponseWriter, r *http.Request, o files.Owner) {
	id, ok := id(w, r)
	if !ok {
		return
	}
	u, e := h.service.UploadStatus(r.Context(), o, id)
	if e != nil {
		h.error(w, r, e)
		return
	}
	response.JSON(w, http.StatusOK, u)
}
func (h *Handler) part(w http.ResponseWriter, r *http.Request, o files.Owner) {
	id, ok := id(w, r)
	if !ok {
		return
	}
	number, e := strconv.Atoi(r.PathValue("number"))
	if e != nil || r.ContentLength < 1 || r.ContentLength > h.service.Config().PartBytes {
		h.error(w, r, files.ErrInvalid)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, r.ContentLength+1)
	u, e := h.service.PutPart(r.Context(), o, id, number, r.ContentLength, r.Header.Get("X-Content-SHA256"), r.Body)
	if e != nil {
		h.error(w, r, e)
		return
	}
	response.JSON(w, http.StatusOK, u)
}
func (h *Handler) commit(w http.ResponseWriter, r *http.Request, o files.Owner) {
	id, ok := id(w, r)
	if !ok {
		return
	}
	f, e := h.service.Commit(r.Context(), o, id)
	if e != nil {
		h.error(w, r, e)
		return
	}
	response.JSON(w, http.StatusOK, f)
}
func (h *Handler) abort(w http.ResponseWriter, r *http.Request, o files.Owner) {
	id, ok := id(w, r)
	if !ok {
		return
	}
	if e := h.service.Abort(r.Context(), o, id); e != nil {
		h.error(w, r, e)
		return
	}
	response.NoContent(w)
}
func (h *Handler) list(w http.ResponseWriter, r *http.Request, o files.Owner) {
	var before *uuid.UUID
	if raw := r.URL.Query().Get("before"); raw != "" {
		parsed, e := uuid.Parse(raw)
		if e != nil {
			h.error(w, r, files.ErrInvalid)
			return
		}
		before = &parsed
	}
	items, next, e := h.service.List(r.Context(), o, before, r.URL.Query().Get("q"))
	if e != nil {
		h.error(w, r, e)
		return
	}
	response.JSON(w, http.StatusOK, map[string]any{"items": items, "nextCursor": next})
}
func (h *Handler) get(w http.ResponseWriter, r *http.Request, o files.Owner) {
	id, ok := id(w, r)
	if !ok {
		return
	}
	f, e := h.service.Get(r.Context(), o, id)
	if e != nil {
		h.error(w, r, e)
		return
	}
	response.JSON(w, http.StatusOK, f)
}
func (h *Handler) references(w http.ResponseWriter, r *http.Request, o files.Owner) {
	id, ok := id(w, r)
	if !ok {
		return
	}
	refs, e := h.service.References(r.Context(), o, id)
	if e != nil {
		h.error(w, r, e)
		return
	}
	response.JSON(w, http.StatusOK, map[string]any{"items": refs, "revision": files.ReferencesRevision(refs), "externalReferencesDiscoverable": false})
}
func (h *Handler) delete(w http.ResponseWriter, r *http.Request, o files.Owner) {
	id, ok := id(w, r)
	if !ok {
		return
	}
	e := h.service.DeleteIfReferencesUnchanged(r.Context(), o, id, r.URL.Query().Get("confirm") == "true", r.URL.Query().Get("referencesRevision"))
	if errors.Is(e, files.ErrInUse) {
		refs, _ := h.service.References(r.Context(), o, id)
		details := []response.ErrorDetail{}
		for _, ref := range refs {
			details = append(details, response.ErrorDetail{Path: ref.Kind + ":" + ref.ID, Code: "file_reference", Message: ref.Description})
		}
		response.Error(w, r, http.StatusConflict, "file_in_use", e.Error(), details...)
		return
	}
	if e != nil {
		h.error(w, r, e)
		return
	}
	response.NoContent(w)
}
func (h *Handler) link(w http.ResponseWriter, r *http.Request, o files.Owner) {
	id, ok := id(w, r)
	if !ok {
		return
	}
	var req struct {
		TTLSeconds int64 `json:"ttlSeconds"`
	}
	if e := response.DecodeJSON(w, r, &req, h.maxJSON); e != nil {
		response.WriteDecodeError(w, r, e)
		return
	}
	if req.TTLSeconds < 1 || req.TTLSeconds > int64(h.service.Config().LinkMaxTTL/time.Second) {
		h.error(w, r, files.ErrInvalid)
		return
	}
	url, expires, e := h.service.Link(r.Context(), o, id, time.Duration(req.TTLSeconds)*time.Second)
	if e != nil {
		h.error(w, r, e)
		return
	}
	response.JSON(w, http.StatusCreated, map[string]any{"url": url, "expiresAt": expires})
}

// Content authorizes owner keys or expiring signatures; public objects need neither.
func (h *Handler) Content(w http.ResponseWriter, r *http.Request) {
	id, ok := id(w, r)
	if !ok {
		return
	}
	h.serve(w, r, id, nil)
}
func (h *Handler) AdminContent(w http.ResponseWriter, r *http.Request) {
	id, ok := id(w, r)
	if !ok {
		return
	}
	owner := h.service.AdminOwner()
	h.serve(w, r, id, &owner)
}
func (h *Handler) ServeMedia(w http.ResponseWriter, r *http.Request) {
	raw := r.PathValue("key")
	if raw == "" {
		raw = r.PathValue("id")
	}
	id, e := uuid.Parse(raw)
	if e != nil {
		h.error(w, r, files.ErrNotFound)
		return
	}
	h.serve(w, r, id, nil)
}
func (h *Handler) serve(w http.ResponseWriter, r *http.Request, id uuid.UUID, admin *files.Owner) {
	key := h.anonymousKey(r)
	var authenticatedOwner *files.Owner
	if admin != nil {
		authenticatedOwner = admin
	} else if token := bearer(r); token != "" {
		owner, err := h.service.Authenticate(token, "read")
		if err == nil {
			authenticatedOwner = &owner
		}
	}
	if authenticatedOwner != nil {
		key = "owner:" + authenticatedOwner.ID.String()
	}
	if !h.rateAllowed(w, r, key) {
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	f, e := h.service.PublicFile(r.Context(), id)
	if e != nil {
		h.error(w, r, e)
		return
	}
	authorized := f.Visibility == "public"
	private := f.Visibility == "private"
	if admin != nil {
		authorized = f.OwnerID == admin.ID
	} else if bearer(r) != "" {
		authorized = authenticatedOwner != nil && authenticatedOwner.ID == f.OwnerID
	} else if h.service.ValidSignature(id, r.URL.Query().Get("expires"), r.URL.Query().Get("signature")) {
		authorized = true
	}
	if !authorized {
		h.error(w, r, files.ErrNotFound)
		return
	}
	object, e := h.service.ResolveObject(r.Context(), f, r.URL.Query().Get("profile"), r.URL.Query().Get("variant"))
	if e != nil {
		h.error(w, r, e)
		return
	}
	etag := `"` + f.SHA256 + `"`
	if f.SHA256 == "" {
		etag = `"` + id.String() + `"`
	}
	if r.URL.Query().Get("profile") != "" || r.URL.Query().Get("variant") != "" {
		etag = `"` + id.String() + "-" + r.URL.Query().Get("profile") + "-" + r.URL.Query().Get("variant") + `"`
	}
	w.Header().Set("ETag", etag)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	if private {
		w.Header().Set("Cache-Control", "private, no-store")
	} else {
		w.Header().Set("Cache-Control", "public, no-cache")
	}
	if files.ConditionalMatch(r, etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	contentType := object.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	disposition := "attachment"
	if files.InlineSafe(contentType) && r.URL.Query().Get("download") != "1" {
		disposition = "inline"
	}
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": f.Name}))
	rawRange := r.Header.Get("Range")
	if ifRange := r.Header.Get("If-Range"); ifRange != "" && ifRange != etag {
		rawRange = ""
	}
	start, end, partial, e := files.ParseRange(rawRange, object.Size)
	if e != nil {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", object.Size))
		w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
		return
	}
	if object.Size >= 0 {
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
	}
	status := http.StatusOK
	if partial {
		status = http.StatusPartialContent
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, object.Size))
	}
	if r.Method == http.MethodHead {
		w.WriteHeader(status)
		return
	}
	reader, e := h.service.Open(r.Context(), object, start, end)
	if e != nil {
		w.Header().Del("Content-Length")
		w.Header().Del("Content-Range")
		h.error(w, r, e)
		return
	}
	defer reader.Close()
	w.WriteHeader(status)
	if object.Size >= 0 {
		_, e = io.CopyN(w, reader, end-start+1)
	} else {
		_, e = io.Copy(w, reader)
	}
	if e != nil && r.Context().Err() == nil {
		h.log.Warn("file stream interrupted", "file_id", id, "error", e)
	}
}

// UploadAdminMedia preserves the existing media response shape while using disk-backed streaming.
func (h *Handler) UploadAdminMedia(w http.ResponseWriter, r *http.Request) {
	if !h.rateAllowed(w, r, "owner:"+h.service.AdminOwner().ID.String()) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, h.service.Config().MaxBytes+h.maxJSON)
	if e := r.ParseMultipartForm(0); e != nil {
		h.error(w, r, fmt.Errorf("%w: invalid multipart or upload too large", files.ErrInvalid))
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	file, header, e := r.FormFile("file")
	if e != nil {
		h.error(w, r, files.ErrInvalid)
		return
	}
	defer file.Close()
	visibility := r.FormValue("visibility")
	if visibility == "" {
		visibility = "public"
	}
	f, e := h.service.UploadStream(r.Context(), h.service.AdminOwner(), header.Filename, header.Size, visibility, file)
	if e != nil {
		h.error(w, r, e)
		return
	}
	response.JSON(w, http.StatusCreated, map[string]any{"id": f.ID, "object_key": f.ObjectKey, "original_filename": f.Name, "content_type": f.ContentType, "size_bytes": f.Size, "status": "ready", "variants": map[string]string{"original": f.ObjectKey}, "ownerId": f.OwnerID, "visibility": f.Visibility, "sha256": f.SHA256, "created_at": f.CreatedAt, "updated_at": f.CreatedAt})
}
func (h *Handler) DeleteAdminMedia(w http.ResponseWriter, r *http.Request) {
	h.delete(w, r, h.service.AdminOwner())
}
func bearer(r *http.Request) string {
	raw := r.Header.Get("Authorization")
	if !strings.HasPrefix(raw, "Bearer ") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(raw, "Bearer "))
}
func id(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	parsed, e := uuid.Parse(r.PathValue("id"))
	if e != nil {
		response.Error(w, r, http.StatusNotFound, "file_not_found", "Файл не найден")
		return uuid.Nil, false
	}
	return parsed, true
}
func (h *Handler) error(w http.ResponseWriter, r *http.Request, e error) {
	status, code, message := http.StatusInternalServerError, "file_error", "Не удалось выполнить операцию с файлом"
	switch {
	case errors.Is(e, files.ErrNotFound):
		status, code, message = http.StatusNotFound, "file_not_found", "Файл не найден"
	case errors.Is(e, files.ErrForbidden):
		status, code, message = http.StatusForbidden, "file_access_denied", "Недостаточно прав для операции"
	case errors.Is(e, files.ErrQuota):
		status, code, message = http.StatusRequestEntityTooLarge, "file_quota", "Превышена квота владельца или число загрузок"
	case errors.Is(e, files.ErrExpired):
		status, code, message = http.StatusGone, "upload_expired", "Срок загрузки истёк"
	case errors.Is(e, files.ErrConflict), errors.Is(e, files.ErrInUse):
		status, code, message = http.StatusConflict, "file_conflict", e.Error()
	case errors.Is(e, files.ErrInvalid):
		status, code, message = http.StatusUnprocessableEntity, "invalid_file", e.Error()
	}
	if status == 500 {
		h.log.Error("file operation failed", "request_id", response.RequestID(r.Context()), "error", e)
	}
	response.Error(w, r, status, code, message)
}

func (h *Handler) AdminMediaAccess(w http.ResponseWriter, r *http.Request) bool {
	id, ok := id(w, r)
	if !ok {
		return false
	}
	_, err := h.service.Get(r.Context(), h.service.AdminOwner(), id)
	if err != nil {
		h.error(w, r, err)
		return false
	}
	return true
}
