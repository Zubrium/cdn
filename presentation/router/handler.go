// Package router adapts Router's typed settings and delivery contracts to HTTP.
package router

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	engine "github.com/Zubrium/cdn/engines/router/presentation"
	"github.com/google/uuid"
)

const routesPath = "/api/v2/admin/router/routes"

type Config struct {
	BranchID     string
	MaxJSONBytes int64
}

type Handler struct {
	api API
	cfg Config
	mux *http.ServeMux
}

type API interface {
	engine.SettingsAPI
	engine.DeliveryAPI
}

func NewHandler(api API, cfg Config) (*Handler, error) {
	if api == nil || strings.TrimSpace(cfg.BranchID) != cfg.BranchID || cfg.BranchID == "" || len(cfg.BranchID) > 128 || cfg.MaxJSONBytes <= 0 || cfg.MaxJSONBytes > 1<<20 {
		return nil, errors.New("invalid Router HTTP configuration")
	}
	for _, r := range cfg.BranchID {
		if r < 0x21 || r == 0x7f {
			return nil, errors.New("invalid Router branch ID")
		}
	}
	h := &Handler{api: api, cfg: cfg, mux: http.NewServeMux()}
	h.mux.HandleFunc("GET "+routesPath, h.listRoutes)
	h.mux.HandleFunc("POST "+routesPath, h.createRoute)
	h.mux.HandleFunc("GET "+routesPath+"/{id}", h.getRoute)
	h.mux.HandleFunc("PUT "+routesPath+"/{id}/target", h.replaceTarget)
	h.mux.HandleFunc("DELETE "+routesPath+"/{id}", h.deleteRoute)
	h.mux.HandleFunc("GET /{$}", h.deliver)
	h.mux.HandleFunc("GET /{path...}", h.deliver)
	return h, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	h.mux.ServeHTTP(w, r)
}

func (h *Handler) listRoutes(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	limit := 50
	if raw := query.Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		limit = parsed
	}
	if len(query.Get("cursor")) > 1024 || len(query.Get("host")) > 253 {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	page, err := h.api.ListRoutes(ctx, engine.ListRoutesRequest{BranchID: h.cfg.BranchID, Host: query.Get("host"), Cursor: query.Get("cursor"), Limit: limit})
	if err != nil {
		writeEngineError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (h *Handler) createRoute(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if !validIdempotencyKey(key) {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	var input engine.CreateRouteRequest
	if err := decodeJSON(w, r, h.cfg.MaxJSONBytes, &input); err != nil {
		writeDecodeError(w, err)
		return
	}
	if input.BranchID != "" && input.BranchID != h.cfg.BranchID {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if err := input.Target.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	input.BranchID = h.cfg.BranchID
	input.IdempotencyKey = key
	route, err := h.api.CreateRoute(ctx, input)
	if err != nil {
		writeEngineError(w, err)
		return
	}
	if route.ID == "" {
		writeError(w, http.StatusBadGateway, "unavailable")
		return
	}
	w.Header().Set("Location", routesPath+"/"+route.ID)
	setRouteETag(w, route.Revision)
	writeJSON(w, http.StatusCreated, route)
}

func (h *Handler) getRoute(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if !validID(id) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	route, err := h.api.GetRoute(ctx, id)
	if err != nil {
		writeEngineError(w, err)
		return
	}
	if route.ID == "" || route.BranchID != h.cfg.BranchID {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	setRouteETag(w, route.Revision)
	writeJSON(w, http.StatusOK, route)
}

func (h *Handler) replaceTarget(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if !validID(id) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	expectedRevision, ok := expectedRevision(w, r)
	if !ok {
		return
	}
	var input struct {
		Target engine.RouteTarget `json:"target"`
	}
	if err := decodeJSON(w, r, h.cfg.MaxJSONBytes, &input); err != nil {
		writeDecodeError(w, err)
		return
	}
	if err := input.Target.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	route, err := h.api.ReplaceRouteTarget(ctx, engine.ReplaceRouteTargetRequest{RouteID: id, Target: input.Target, ExpectedRevision: expectedRevision})
	if err != nil {
		writeEngineError(w, err)
		return
	}
	if route.ID == "" || route.BranchID != h.cfg.BranchID {
		writeError(w, http.StatusBadGateway, "unavailable")
		return
	}
	setRouteETag(w, route.Revision)
	writeJSON(w, http.StatusOK, route)
}

func (h *Handler) deleteRoute(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if !validID(id) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	expected, ok := expectedRevision(w, r)
	if !ok {
		return
	}
	if err := h.api.DeleteRoute(ctx, engine.DeleteRouteRequest{RouteID: id, ExpectedRevision: expected}); err != nil {
		writeEngineError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) deliver(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	request := engine.DeliveryRequest{
		BranchID: h.cfg.BranchID, Host: r.Host, Method: r.Method,
		EscapedPath: r.URL.EscapedPath(), Query: r.URL.RawQuery,
		IfNoneMatch: r.Header.Get("If-None-Match"), IfModifiedSince: r.Header.Get("If-Modified-Since"),
		Range: r.Header.Get("Range"), IfRange: r.Header.Get("If-Range"),
	}
	response, err := h.api.Deliver(r.Context(), request)
	if err != nil {
		writeEngineError(w, err)
		return
	}
	if response.Body != nil {
		defer response.Body.Close()
	}
	if !validDeliveryResponse(response) {
		writeError(w, http.StatusBadGateway, "unavailable")
		return
	}
	cacheControl := response.CacheControl
	if cacheControl == "" {
		cacheControl = "no-store"
	}
	w.Header().Set("Cache-Control", cacheControl)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if response.ContentType != "" {
		mediaType, parameters, parseErr := mime.ParseMediaType(response.ContentType)
		if parseErr != nil {
			writeError(w, http.StatusBadGateway, "unavailable")
			return
		}
		w.Header().Set("Content-Type", mime.FormatMediaType(mediaType, parameters))
	}
	if response.ETag != "" {
		w.Header().Set("ETag", response.ETag)
	}
	if response.LastModified != "" {
		modified, parseErr := http.ParseTime(response.LastModified)
		if parseErr != nil {
			writeError(w, http.StatusBadGateway, "unavailable")
			return
		}
		w.Header().Set("Last-Modified", modified.UTC().Format(http.TimeFormat))
	}
	if response.ContentDisposition != "" {
		value, parseErr := canonicalDisposition(response.ContentDisposition)
		if parseErr != nil {
			writeError(w, http.StatusBadGateway, "unavailable")
			return
		}
		w.Header().Set("Content-Disposition", value)
	}
	if response.AcceptRanges == "bytes" {
		w.Header().Set("Accept-Ranges", "bytes")
	}
	if response.ContentLength >= 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(response.ContentLength, 10))
	}
	w.WriteHeader(response.StatusCode)
	if r.Method == http.MethodHead || response.StatusCode == http.StatusNoContent || response.StatusCode == http.StatusNotModified || response.Body == nil {
		return
	}
	_, _ = io.Copy(w, response.Body)
}

func (h *Handler) authenticate(w http.ResponseWriter, r *http.Request) (context.Context, bool) {
	value := r.Header.Get("Authorization")
	if !strings.HasPrefix(value, "Bearer ") || strings.TrimSpace(strings.TrimPrefix(value, "Bearer ")) == "" {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return nil, false
	}
	ctx, err := h.api.Authenticate(r.Context(), strings.TrimSpace(strings.TrimPrefix(value, "Bearer ")))
	if err != nil {
		writeEngineError(w, err)
		return nil, false
	}
	if ctx == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable")
		return nil, false
	}
	return ctx, true
}

func decodeJSON(w http.ResponseWriter, r *http.Request, maxBytes int64, target any) error {
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		return engine.ErrInvalid
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return errors.New("request too large")
		}
		return engine.ErrInvalid
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return engine.ErrInvalid
	}
	return nil
}

func expectedRevision(w http.ResponseWriter, r *http.Request) (int64, bool) {
	value := strings.TrimSpace(r.Header.Get("If-Match"))
	value = strings.Trim(value, "\"")
	revision, err := strconv.ParseInt(value, 10, 64)
	if err != nil || revision <= 0 {
		writeError(w, http.StatusPreconditionRequired, "revision_required")
		return 0, false
	}
	return revision, true
}

func validID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil
}

func validIdempotencyKey(value string) bool {
	if len(value) < 16 || len(value) > 128 || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune("-_.:", r) {
			continue
		}
		return false
	}
	return true
}

func validDeliveryResponse(response engine.DeliveryResponse) bool {
	if response.StatusCode < 200 || response.StatusCode > 599 || response.StatusCode == http.StatusSwitchingProtocols || response.ContentLength < -1 {
		return false
	}
	if response.CacheControl == "" {
		response.CacheControl = "no-store"
	}
	if strings.ContainsAny(response.CacheControl, "\r\n") || len(response.CacheControl) > 128 || !validCacheControl(response.CacheControl) {
		return false
	}
	return response.ETag == "" || validETag(response.ETag)
}

func validCacheControl(value string) bool {
	if value == "no-store" {
		return true
	}
	if !strings.HasPrefix(value, "public, max-age=") {
		return false
	}
	parts := strings.Split(value, ", ")
	if len(parts) < 2 || len(parts) > 3 || (len(parts) == 3 && parts[2] != "immutable") {
		return false
	}
	seconds := strings.TrimPrefix(parts[1], "max-age=")
	n, err := strconv.ParseUint(seconds, 10, 32)
	return err == nil && n > 0
}

func validETag(value string) bool {
	if strings.ContainsAny(value, "\r\n") || len(value) < 2 || len(value) > 128 {
		return false
	}
	if strings.HasPrefix(value, "W/") {
		value = strings.TrimPrefix(value, "W/")
	}
	return strings.HasPrefix(value, "\"") && strings.HasSuffix(value, "\"") && !strings.ContainsAny(value[1:len(value)-1], "\"")
}

func canonicalDisposition(value string) (string, error) {
	if strings.ContainsAny(value, "\r\n") {
		return "", errors.New("invalid content disposition")
	}
	kind, rawParameters, found := strings.Cut(value, ";")
	kind = strings.TrimSpace(kind)
	if kind != "inline" && kind != "attachment" {
		return "", errors.New("invalid content disposition")
	}
	parameterized := "application/octet-stream"
	if found {
		parameterized += ";" + rawParameters
	}
	_, parameters, err := mime.ParseMediaType(parameterized)
	if err != nil {
		return "", errors.New("invalid content disposition")
	}
	formatted := mime.FormatMediaType("application/octet-stream", parameters)
	if formatted == "" {
		return "", errors.New("invalid content disposition")
	}
	return kind + strings.TrimPrefix(formatted, "application/octet-stream"), nil
}

func setRouteETag(w http.ResponseWriter, revision int64) {
	if revision > 0 {
		w.Header().Set("ETag", fmt.Sprintf("\"%d\"", revision))
	}
}

func writeEngineError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, engine.ErrUnauthorized):
		writeError(w, http.StatusUnauthorized, "unauthorized")
	case errors.Is(err, engine.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden")
	case errors.Is(err, engine.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found")
	case errors.Is(err, engine.ErrConflict):
		writeError(w, http.StatusConflict, "conflict")
	case errors.Is(err, engine.ErrInvalid), errors.Is(err, engine.ErrInvalidTarget):
		writeError(w, http.StatusBadRequest, "invalid_request")
	default:
		writeError(w, http.StatusServiceUnavailable, "unavailable")
	}
}

func writeDecodeError(w http.ResponseWriter, err error) {
	if strings.Contains(err.Error(), "too large") {
		writeError(w, http.StatusRequestEntityTooLarge, "too_large")
		return
	}
	writeError(w, http.StatusBadRequest, "invalid_request")
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"error": code})
}

var _ http.Handler = (*Handler)(nil)
