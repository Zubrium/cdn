// Package router adapts Router's typed settings and delivery contracts to HTTP.
package router

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	engine "github.com/Zubrium/cdn/engines/router/presentation"
	httpjson "github.com/Zubrium/cdn/pkg/json"
	adminhttpauth "github.com/Zubrium/cdn/presentation/admin/base/admin/auth"
	"github.com/google/uuid"
)

const (
	routesPath     = "/api/admin/router/routes"
	operationsPath = "/api/admin/router/operations"
)

type Config struct {
	BranchID                string
	MaxJSONBytes            int64
	DefaultPageSize         int
	MaxPageSize             int
	AllowedOrigins          []string
	PublicOperations        engine.PublicOperationCatalog
	FormSessionCookieSecure bool
}

type Handler struct {
	api       API
	cfg       Config
	auth      *adminhttpauth.Transport
	mux       *http.ServeMux
	adminMux  *http.ServeMux
	publicMux *http.ServeMux
}

type API interface {
	engine.SettingsAPI
	engine.DeliveryAPI
}

func NewHandler(api API, cfg Config) (*Handler, error) {
	if isNilAPI(api) || strings.TrimSpace(cfg.BranchID) != cfg.BranchID || cfg.BranchID == "" || len(cfg.BranchID) > 128 || cfg.MaxJSONBytes <= 0 || cfg.MaxJSONBytes > 1<<20 ||
		cfg.DefaultPageSize < 1 || cfg.MaxPageSize < cfg.DefaultPageSize || cfg.MaxPageSize > 100 {
		return nil, errors.New("invalid Router HTTP configuration")
	}
	for _, r := range cfg.BranchID {
		if r < 0x21 || r == 0x7f {
			return nil, errors.New("invalid Router branch ID")
		}
	}
	var auth *adminhttpauth.Transport
	if len(cfg.AllowedOrigins) > 0 {
		var err error
		auth, err = adminhttpauth.New(cfg.AllowedOrigins)
		if err != nil {
			return nil, err
		}
	}
	h := &Handler{api: api, cfg: cfg, auth: auth, mux: http.NewServeMux(), adminMux: http.NewServeMux(), publicMux: http.NewServeMux()}
	h.adminMux.HandleFunc("GET "+operationsPath, h.listOperations)
	h.adminMux.HandleFunc("GET "+routesPath, h.listRoutes)
	h.adminMux.HandleFunc("POST "+routesPath, h.createRoute)
	h.adminMux.HandleFunc("GET "+routesPath+"/{id}", h.getRoute)
	h.adminMux.HandleFunc("PUT "+routesPath+"/{id}/target", h.replaceTarget)
	h.adminMux.HandleFunc("DELETE "+routesPath+"/{id}", h.deleteRoute)
	h.adminMux.HandleFunc("/api/admin/{path...}", h.adminNotFound)
	h.publicMux.HandleFunc("/{$}", h.deliver)
	h.publicMux.HandleFunc("/{path...}", h.deliver)
	h.mux.Handle("/api/admin/", h.adminMux)
	h.mux.HandleFunc("/{$}", h.deliver)
	h.mux.HandleFunc("/{path...}", h.deliver)
	return h, nil
}

func (h *Handler) listOperations(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	items := []engine.PublicOperationDescriptor{}
	if h.cfg.PublicOperations != nil {
		var err error
		items, err = h.cfg.PublicOperations.ListPublicOperations(ctx)
		if err != nil {
			writeEngineError(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, struct {
		Items []engine.PublicOperationDescriptor `json:"items"`
	}{Items: items})
}

func isNilAPI(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	secureResponseHeaders(w)
	h.mux.ServeHTTP(w, r)
}

// AdminHTTPHandler is mounted only on the authenticated Admin API listener.
func (h *Handler) AdminHTTPHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secureResponseHeaders(w)
		h.adminMux.ServeHTTP(w, r)
	})
}

// PublicHTTPHandler contains anonymous route delivery. Deployment must keep
// its listener behind an external proxy; this application does not configure
// the proxy or implement request-rate limiting.
func (h *Handler) PublicHTTPHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secureResponseHeaders(w)
		h.publicMux.ServeHTTP(w, r)
	})
}

func secureResponseHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
}

func (h *Handler) adminNotFound(w http.ResponseWriter, _ *http.Request) {
	writeError(w, http.StatusNotFound, "not_found")
}

func reservedAdminPath(path string) bool {
	return path == "/api/admin" || strings.HasPrefix(path, "/api/admin/") ||
		path == "/api/v2/admin" || strings.HasPrefix(path, "/api/v2/admin/")
}

func (h *Handler) listRoutes(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	for key, values := range query {
		if (key != "limit" && key != "cursor" && key != "host") || len(values) != 1 || strings.TrimSpace(values[0]) != values[0] {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
	}
	limit := h.cfg.DefaultPageSize
	if raw := query.Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > h.cfg.MaxPageSize {
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
	println("ROUTE_DIAG entered")
	ctx, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if len(r.Header.Values("Idempotency-Key")) != 1 || !validIdempotencyKey(key) {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	var input engine.CreateRouteRequest
	if err := decodeJSON(w, r, h.cfg.MaxJSONBytes, &input); err != nil {
		println("ROUTE_DIAG decode_error")
		writeDecodeError(w, err)
		return
	}
	if input.BranchID != "" && input.BranchID != h.cfg.BranchID {
		println("ROUTE_DIAG branch_invalid")
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if !validTargetMatch(input.Match, input.Target.Kind) {
		println("ROUTE_DIAG match_invalid", input.Match, input.Target.Kind)
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if err := h.validateTarget(ctx, input.Target); err != nil {
		println("ROUTE_DIAG target_invalid", input.Target.Kind)
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	input.BranchID = h.cfg.BranchID
	input.IdempotencyKey = key
	route, err := h.api.CreateRoute(ctx, input)
	if err != nil {
		println("ROUTE_DIAG api_create_error", input.Host, input.Match, input.Target.Kind, fmt.Sprintf("%T", err), err.Error())
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
	if err := h.validateTarget(ctx, input.Target); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	current, err := h.api.GetRoute(ctx, id)
	if err != nil {
		writeEngineError(w, err)
		return
	}
	if current.ID == "" || current.BranchID != h.cfg.BranchID {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if current.Revision != expectedRevision {
		writeError(w, http.StatusConflict, "revision_conflict")
		return
	}
	if !validTargetMatch(current.Match, input.Target.Kind) {
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

func validTargetMatch(match, targetKind string) bool {
	if match != "exact" && match != "prefix" {
		return false
	}
	return (targetKind != engine.TargetCoreFile && targetKind != engine.TargetRegisteredOperation) || match == "exact"
}

func (h *Handler) validateTarget(ctx context.Context, target engine.RouteTarget) error {
	if err := target.Validate(); err != nil {
		return err
	}
	if target.Kind != engine.TargetRegisteredOperation {
		return nil
	}
	if h.cfg.PublicOperations == nil {
		return engine.ErrPublicOperationNotRegistered
	}
	return h.cfg.PublicOperations.ValidatePublicOperationTarget(ctx, target)
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
	if reservedAdminPath(r.URL.Path) {
		h.adminNotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if len(r.Header.Values("Content-Type")) > 1 || len(r.Header.Values("Idempotency-Key")) > 1 ||
		len(r.Header.Values("If-None-Match")) > 1 || len(r.Header.Values("If-Modified-Since")) > 1 ||
		len(r.Header.Values("Range")) > 1 || len(r.Header.Values("If-Range")) > 1 {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	formTokens, cookieErr := h.formSessionTokens(r)
	if cookieErr != nil {
		writeError(w, http.StatusBadRequest, "invalid_form_session_cookie")
		return
	}
	if (r.Method == http.MethodGet || r.Method == http.MethodHead) && (r.ContentLength > 0 || len(r.TransferEncoding) > 0) {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	request := engine.DeliveryRequest{
		BranchID: h.cfg.BranchID, Host: r.Host, Method: r.Method,
		EscapedPath: r.URL.EscapedPath(), Query: r.URL.RawQuery,
		ContentType: r.Header.Get("Content-Type"), ContentLength: r.ContentLength,
		IdempotencyKey: r.Header.Get("Idempotency-Key"), Body: r.Body,
		IfNoneMatch: r.Header.Get("If-None-Match"), IfModifiedSince: r.Header.Get("If-Modified-Since"),
		Range: r.Header.Get("Range"), IfRange: r.Header.Get("If-Range"),
		LocalePreference: localePreferenceCookie(r), AcceptLanguage: r.Header.Get("Accept-Language"),
		FormSessionTokens: formTokens,
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
	if change := response.FormSessionCookie; change != nil {
		h.setFormSessionCookie(w, *change)
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

func (h *Handler) formSessionTokens(r *http.Request) (map[string]string, error) {
	if len(r.Header.Values("Cookie")) > 1 {
		return nil, errors.New("multiple Cookie headers")
	}
	prefix := "zubrium_form_"
	if h.cfg.FormSessionCookieSecure {
		prefix = "__Host-zubrium-form-"
	}
	tokens := make(map[string]string)
	for _, cookie := range r.Cookies() {
		if !strings.HasPrefix(cookie.Name, prefix) {
			continue
		}
		formID := strings.TrimPrefix(cookie.Name, prefix)
		if !validID(formID) || !validFormSessionToken(cookie.Value) || len(tokens) >= 32 {
			return nil, errors.New("invalid form session cookie")
		}
		if _, duplicate := tokens[formID]; duplicate {
			return nil, errors.New("duplicate form session cookie")
		}
		tokens[formID] = cookie.Value
	}
	return tokens, nil
}

func localePreferenceCookie(r *http.Request) string {
	if r == nil || len(r.Header.Values("Cookie")) > 1 {
		return ""
	}
	for _, cookie := range r.Cookies() {
		if cookie.Name == "zubrium_locale" {
			return cookie.Value
		}
	}
	return ""
}

func validFormSessionToken(value string) bool {
	if len(value) != 43 {
		return false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	return err == nil && len(decoded) == 32
}

func (h *Handler) setFormSessionCookie(w http.ResponseWriter, change engine.FormSessionCookieChange) {
	name := "zubrium_form_" + change.FormID
	if h.cfg.FormSessionCookieSecure {
		name = "__Host-zubrium-form-" + change.FormID
	}
	cookie := &http.Cookie{Name: name, Path: "/", HttpOnly: true, Secure: h.cfg.FormSessionCookieSecure, SameSite: http.SameSiteStrictMode}
	if change.Clear {
		cookie.MaxAge = -1
		cookie.Expires = time.Unix(1, 0).UTC()
	} else {
		cookie.Value = change.Token
		cookie.Expires = change.ExpiresAt.UTC()
		cookie.MaxAge = max(1, int(time.Until(change.ExpiresAt).Seconds()))
	}
	http.SetCookie(w, cookie)
}

func (h *Handler) authenticate(w http.ResponseWriter, r *http.Request) (context.Context, bool) {
	credential, err := adminhttpauth.Resolve(r)
	if err != nil {
		if errors.Is(err, adminhttpauth.ErrAmbiguous) {
			writeError(w, http.StatusBadRequest, "ambiguous_authentication")
		} else {
			writeError(w, http.StatusUnauthorized, "unauthorized")
		}
		return nil, false
	}
	var ctx context.Context
	if credential.Session {
		if h.auth == nil {
			writeError(w, http.StatusServiceUnavailable, "unavailable")
			return nil, false
		}
		if err := h.auth.ValidateSessionRequest(r); err != nil {
			writeError(w, http.StatusForbidden, "csrf_forbidden")
			return nil, false
		}
		ctx, err = h.api.AuthenticateSession(r.Context(), credential.Value)
	} else {
		ctx, err = h.api.Authenticate(r.Context(), credential.Value)
	}
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
	err := httpjson.DecodeRequest(w, r, maxBytes, target)
	switch {
	case errors.Is(err, httpjson.ErrTooLarge):
		return engine.ErrTooLarge
	case errors.Is(err, httpjson.ErrInvalid):
		return engine.ErrInvalid
	default:
		return err
	}
}

func expectedRevision(w http.ResponseWriter, r *http.Request) (int64, bool) {
	values := r.Header.Values("If-Match")
	if len(values) == 0 {
		writeError(w, http.StatusPreconditionRequired, "revision_required")
		return 0, false
	}
	if len(values) != 1 {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return 0, false
	}
	value := values[0]
	if len(value) < 3 || value[0] != '"' || value[len(value)-1] != '"' {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return 0, false
	}
	raw := value[1 : len(value)-1]
	revision, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || revision <= 0 || strconv.FormatInt(revision, 10) != raw {
		writeError(w, http.StatusBadRequest, "invalid_request")
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
	if change := response.FormSessionCookie; change != nil {
		if !validID(change.FormID) {
			return false
		}
		if change.Clear {
			if change.Token != "" {
				return false
			}
		} else if !validFormSessionToken(change.Token) || change.ExpiresAt.IsZero() || !change.ExpiresAt.After(time.Now()) || change.ExpiresAt.After(time.Now().Add(24*time.Hour)) {
			return false
		}
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
	case errors.Is(err, engine.ErrTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, "too_large")
	case errors.Is(err, engine.ErrInvalid), errors.Is(err, engine.ErrInvalidTarget):
		writeError(w, http.StatusBadRequest, "invalid_request")
	default:
		writeError(w, http.StatusServiceUnavailable, "unavailable")
	}
}

func writeDecodeError(w http.ResponseWriter, err error) {
	if errors.Is(err, engine.ErrTooLarge) {
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
