// Package formsapi adapts the Forms Engine contract to the Admin HTTP API.
package formsapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"

	engine "github.com/Zubrium/cdn/engines/forms/presentation"
	adminhttpauth "github.com/Zubrium/cdn/presentation/admin/httpauth"
)

const apiPrefix = "/api/v2/admin/forms"

type Config struct {
	MaxJSONBytes    int64
	MaxPageSize     int
	DefaultPageSize int
	AllowedOrigins  []string
}

type Handler struct {
	api       engine.API
	cfg       Config
	transport *adminhttpauth.Transport
	mux       *http.ServeMux
}

func NewHandler(api engine.API, config Config) (*Handler, error) {
	if nilInterface(api) || config.MaxJSONBytes < 1 || config.MaxJSONBytes > 16<<20 ||
		config.MaxPageSize < 1 || config.MaxPageSize > 500 || config.DefaultPageSize < 1 || config.DefaultPageSize > config.MaxPageSize {
		return nil, errors.New("invalid Forms API configuration")
	}
	transport, err := adminhttpauth.New(config.AllowedOrigins)
	if err != nil {
		return nil, err
	}
	h := &Handler{api: api, cfg: config, transport: transport, mux: http.NewServeMux()}
	h.mux.HandleFunc("GET "+apiPrefix+"/schemas", h.listSchemas)
	h.mux.HandleFunc("GET "+apiPrefix+"/schemas/{schemaID}", h.getSchema)
	h.mux.HandleFunc("PUT "+apiPrefix+"/schemas", h.saveSchema)
	h.mux.HandleFunc("GET "+apiPrefix+"/submissions", h.listSubmissions)
	h.mux.HandleFunc("GET "+apiPrefix+"/submissions/{submissionID}", h.getSubmission)
	h.mux.HandleFunc("PATCH "+apiPrefix+"/submissions/{submissionID}/status", h.setSubmissionStatus)
	h.mux.HandleFunc("DELETE "+apiPrefix+"/submissions/{submissionID}", h.deleteSubmission)
	return h, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	h.mux.ServeHTTP(w, r)
}

func (h *Handler) listSchemas(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	query, valid := readQuery(r, "snapshot_id", "cursor", "limit")
	if !valid || !validID(query["snapshot_id"]) || len(query["cursor"]) > 1024 {
		writeError(w, http.StatusBadRequest, "invalid_query")
		return
	}
	limit, valid := parseLimit(query["limit"], h.cfg)
	if !valid {
		writeError(w, http.StatusBadRequest, "invalid_query")
		return
	}
	page, err := h.api.ListSchemas(ctx, query["snapshot_id"], query["cursor"], limit)
	if err != nil {
		writeEngineError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (h *Handler) getSchema(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	query, valid := readQuery(r, "snapshot_id")
	schemaID := r.PathValue("schemaID")
	if !valid || !validID(query["snapshot_id"]) || !validID(schemaID) {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	schema, err := h.api.GetSchema(ctx, query["snapshot_id"], schemaID)
	if err != nil {
		writeEngineError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, schema)
}

func (h *Handler) saveSchema(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	var request engine.SaveSchemaRequest
	if err := decodeJSON(w, r, h.cfg.MaxJSONBytes, &request); err != nil {
		writeDecodeError(w, err)
		return
	}
	if !validID(request.SnapshotID) || (request.SchemaID != "" && !validID(request.SchemaID)) || !validText(request.Key, 128) ||
		request.Fields == nil || request.ExpectedRevision < 0 {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	schema, err := h.api.SaveSchema(ctx, request)
	if err != nil {
		writeEngineError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, schema)
}

func (h *Handler) listSubmissions(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	query, valid := readQuery(r, "snapshot_id", "form_id", "state", "cursor", "limit")
	if !valid || !validID(query["snapshot_id"]) || (query["form_id"] != "" && !validID(query["form_id"])) ||
		len(query["state"]) > 64 || len(query["cursor"]) > 1024 {
		writeError(w, http.StatusBadRequest, "invalid_query")
		return
	}
	limit, valid := parseLimit(query["limit"], h.cfg)
	if !valid {
		writeError(w, http.StatusBadRequest, "invalid_query")
		return
	}
	page, err := h.api.ListSubmissions(ctx, engine.ListSubmissionsRequest{SnapshotID: query["snapshot_id"], FormID: query["form_id"], State: query["state"], Cursor: query["cursor"], Limit: limit})
	if err != nil {
		writeEngineError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (h *Handler) getSubmission(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	query, valid := readQuery(r, "snapshot_id")
	submissionID := r.PathValue("submissionID")
	if !valid || !validID(query["snapshot_id"]) || !validID(submissionID) {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	submission, err := h.api.GetSubmission(ctx, query["snapshot_id"], submissionID)
	if err != nil {
		writeEngineError(w, err)
		return
	}
	setRevisionETag(w, submission.Revision)
	writeJSON(w, http.StatusOK, submission)
}

func (h *Handler) setSubmissionStatus(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	var request struct {
		SnapshotID       string `json:"snapshot_id"`
		Status           string `json:"status"`
		ExpectedRevision int64  `json:"expected_revision"`
	}
	if err := decodeJSON(w, r, h.cfg.MaxJSONBytes, &request); err != nil {
		writeDecodeError(w, err)
		return
	}
	submissionID := r.PathValue("submissionID")
	if !validID(request.SnapshotID) || !validText(request.Status, 64) || !validID(submissionID) || request.ExpectedRevision < 0 {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	submission, err := h.api.SetSubmissionStatus(ctx, engine.SetSubmissionStatusRequest{SnapshotID: request.SnapshotID,
		SubmissionID: submissionID, Status: request.Status, ExpectedRevision: request.ExpectedRevision})
	if err != nil {
		writeEngineError(w, err)
		return
	}
	setRevisionETag(w, submission.Revision)
	writeJSON(w, http.StatusOK, submission)
}

func (h *Handler) deleteSubmission(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	query, valid := readQuery(r, "snapshot_id")
	revision, revisionOK := parseIfMatchRevision(r.Header.Get("If-Match"))
	submissionID := r.PathValue("submissionID")
	if !valid || !validID(query["snapshot_id"]) || !validID(submissionID) || !revisionOK {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if err := h.api.DeleteSubmission(ctx, engine.DeleteSubmissionRequest{SnapshotID: query["snapshot_id"], SubmissionID: submissionID, ExpectedRevision: revision}); err != nil {
		writeEngineError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
	if credential.Session {
		if err := h.transport.ValidateSessionRequest(r); err != nil {
			writeError(w, http.StatusForbidden, "csrf_forbidden")
			return nil, false
		}
		ctx, err := h.api.AuthenticateSession(r.Context(), credential.Value)
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
	ctx, err := h.api.Authenticate(r.Context(), credential.Value)
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

func readQuery(r *http.Request, allowed ...string) (map[string]string, bool) {
	allow := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		allow[key] = struct{}{}
	}
	values := r.URL.Query()
	for key, items := range values {
		if _, ok := allow[key]; !ok || len(items) != 1 {
			return nil, false
		}
	}
	result := make(map[string]string, len(allowed))
	for _, key := range allowed {
		value := values.Get(key)
		if value != strings.TrimSpace(value) {
			return nil, false
		}
		result[key] = value
	}
	return result, true
}

func parseLimit(value string, config Config) (int, bool) {
	if value == "" {
		return config.DefaultPageSize, true
	}
	limit, err := strconv.Atoi(value)
	return limit, err == nil && limit > 0 && limit <= config.MaxPageSize
}

func parseIfMatchRevision(value string) (int64, bool) {
	if len(value) < 4 || len(value) > 32 || !strings.HasPrefix(value, `"r`) || !strings.HasSuffix(value, `"`) {
		return 0, false
	}
	revision, err := strconv.ParseInt(value[2:len(value)-1], 10, 64)
	return revision, err == nil && revision >= 0
}

func setRevisionETag(w http.ResponseWriter, revision int64) {
	if revision > 0 {
		w.Header().Set("ETag", `"r`+strconv.FormatInt(revision, 10)+`"`)
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, limit int64, target any) error {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return engine.ErrInvalid
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return engine.ErrTooLarge
		}
		return engine.ErrInvalid
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return engine.ErrInvalid
	}
	return nil
}

func writeDecodeError(w http.ResponseWriter, err error) {
	if errors.Is(err, engine.ErrTooLarge) {
		writeError(w, http.StatusRequestEntityTooLarge, "too_large")
		return
	}
	writeError(w, http.StatusBadRequest, "invalid_request")
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
	case errors.Is(err, engine.ErrInvalid):
		writeError(w, http.StatusBadRequest, "invalid_request")
	case errors.Is(err, engine.ErrTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, "too_large")
	default:
		writeError(w, http.StatusServiceUnavailable, "unavailable")
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"error": code})
}

func validID(value string) bool {
	if value == "" || len(value) > 128 || !utf8.ValidString(value) {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || strings.ContainsRune("_-.", char) {
			continue
		}
		return false
	}
	return true
}

func validText(value string, maximum int) bool {
	return value != "" && len(value) <= maximum && utf8.ValidString(value) && strings.TrimSpace(value) == value && !strings.ContainsRune(value, '\x00')
}

func nilInterface(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
