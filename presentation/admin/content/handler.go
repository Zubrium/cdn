// Package contentapi adapts the Content Engine contract to the Admin HTTP API.
package contentapi

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

	engine "github.com/Zubrium/cdn/engines/content/presentation"
	adminhttpauth "github.com/Zubrium/cdn/presentation/admin/httpauth"
)

const apiPrefix = "/api/v2/admin/content"

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
		return nil, errors.New("invalid Content API configuration")
	}
	transport, err := adminhttpauth.New(config.AllowedOrigins)
	if err != nil {
		return nil, err
	}
	h := &Handler{api: api, cfg: config, transport: transport, mux: http.NewServeMux()}
	h.mux.HandleFunc("GET "+apiPrefix+"/models", h.listModels)
	h.mux.HandleFunc("GET "+apiPrefix+"/models/{modelID}", h.getModel)
	h.mux.HandleFunc("PUT "+apiPrefix+"/models", h.saveModel)
	h.mux.HandleFunc("GET "+apiPrefix+"/records", h.listRecords)
	h.mux.HandleFunc("PUT "+apiPrefix+"/records", h.saveRecord)
	h.mux.HandleFunc("POST "+apiPrefix+"/records/{recordID}/publish", h.publishRecord)
	h.mux.HandleFunc("POST "+apiPrefix+"/records/{recordID}/restore", h.restoreRecord)
	h.mux.HandleFunc("GET "+apiPrefix+"/bindings/{featureID}", h.getBindings)
	h.mux.HandleFunc("PUT "+apiPrefix+"/bindings", h.saveBindings)
	return h, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	h.mux.ServeHTTP(w, r)
}

func (h *Handler) listModels(w http.ResponseWriter, r *http.Request) {
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
	page, err := h.api.ListModels(ctx, query["snapshot_id"], query["cursor"], limit)
	if err != nil {
		writeEngineError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (h *Handler) getModel(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	query, valid := readQuery(r, "snapshot_id")
	modelID := r.PathValue("modelID")
	if !valid || !validID(query["snapshot_id"]) || !validID(modelID) {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	model, err := h.api.GetModel(ctx, query["snapshot_id"], modelID)
	if err != nil {
		writeEngineError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, model)
}

func (h *Handler) saveModel(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	var request engine.SaveModelRequest
	if err := decodeJSON(w, r, h.cfg.MaxJSONBytes, &request); err != nil {
		writeDecodeError(w, err)
		return
	}
	if !validID(request.SnapshotID) || (request.ModelID != "" && !validID(request.ModelID)) || !validText(request.Key, 128) ||
		!validText(request.Name, 256) || (request.Cardinality != "many" && request.Cardinality != "singleton") ||
		request.ExpectedRevision < 0 || len(request.Fields) > 256 {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	for _, field := range request.Fields {
		if (field.ID != "" && !validID(field.ID)) || !validText(field.Key, 128) || !validText(field.Name, 256) || !validText(field.Type, 64) {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
	}
	model, err := h.api.SaveModel(ctx, request)
	if err != nil {
		writeEngineError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, model)
}

func (h *Handler) listRecords(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	query, valid := readQuery(r, "snapshot_id", "model_id", "cursor", "limit")
	if !valid || !validID(query["snapshot_id"]) || !validID(query["model_id"]) || len(query["cursor"]) > 1024 {
		writeError(w, http.StatusBadRequest, "invalid_query")
		return
	}
	limit, valid := parseLimit(query["limit"], h.cfg)
	if !valid {
		writeError(w, http.StatusBadRequest, "invalid_query")
		return
	}
	page, err := h.api.ListRecords(ctx, engine.ListRecordsRequest{SnapshotID: query["snapshot_id"], ModelID: query["model_id"], Cursor: query["cursor"], Limit: limit})
	if err != nil {
		writeEngineError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (h *Handler) saveRecord(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	var request engine.SaveRecordRequest
	if err := decodeJSON(w, r, h.cfg.MaxJSONBytes, &request); err != nil {
		writeDecodeError(w, err)
		return
	}
	if !validID(request.SnapshotID) || !validID(request.ModelID) || (request.RecordID != "" && !validID(request.RecordID)) ||
		request.Values == nil || request.ExpectedRevision < 0 {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	record, err := h.api.SaveRecordDraft(ctx, request)
	if err != nil {
		writeEngineError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, record)
}

func (h *Handler) publishRecord(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	var request struct {
		SnapshotID       string `json:"snapshot_id"`
		ExpectedRevision int64  `json:"expected_revision"`
	}
	if err := decodeJSON(w, r, h.cfg.MaxJSONBytes, &request); err != nil {
		writeDecodeError(w, err)
		return
	}
	recordID := r.PathValue("recordID")
	if !validID(request.SnapshotID) || !validID(recordID) || request.ExpectedRevision < 0 {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	record, err := h.api.PublishRecord(ctx, engine.PublishRecordRequest{SnapshotID: request.SnapshotID, RecordID: recordID, ExpectedRevision: request.ExpectedRevision})
	if err != nil {
		writeEngineError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, record)
}

func (h *Handler) restoreRecord(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	var request struct {
		SnapshotID       string `json:"snapshot_id"`
		HistoryID        string `json:"history_id"`
		ExpectedRevision int64  `json:"expected_revision"`
	}
	if err := decodeJSON(w, r, h.cfg.MaxJSONBytes, &request); err != nil {
		writeDecodeError(w, err)
		return
	}
	recordID := r.PathValue("recordID")
	if !validID(request.SnapshotID) || !validID(request.HistoryID) || !validID(recordID) || request.ExpectedRevision < 0 {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	record, err := h.api.RestoreRecord(ctx, engine.RestoreRecordRequest{SnapshotID: request.SnapshotID, RecordID: recordID, HistoryID: request.HistoryID, ExpectedRevision: request.ExpectedRevision})
	if err != nil {
		writeEngineError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, record)
}

func (h *Handler) getBindings(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	query, valid := readQuery(r, "snapshot_id")
	featureID := r.PathValue("featureID")
	if !valid || !validID(query["snapshot_id"]) || !validID(featureID) {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	bindings, err := h.api.GetBindings(ctx, query["snapshot_id"], featureID)
	if err != nil {
		writeEngineError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, bindings)
}

func (h *Handler) saveBindings(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	var request engine.SaveBindingsRequest
	if err := decodeJSON(w, r, h.cfg.MaxJSONBytes, &request); err != nil {
		writeDecodeError(w, err)
		return
	}
	if !validID(request.SnapshotID) || !validID(request.FeatureID) || request.Values == nil || request.ExpectedRevision < 0 {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	bindings, err := h.api.SaveBindings(ctx, request)
	if err != nil {
		writeEngineError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, bindings)
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
		sessionCtx, err := h.api.AuthenticateSession(r.Context(), credential.Value)
		if err != nil {
			writeEngineError(w, err)
			return nil, false
		}
		if sessionCtx == nil {
			writeError(w, http.StatusServiceUnavailable, "unavailable")
			return nil, false
		}
		return sessionCtx, true
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
		if value := values.Get(key); value != strings.TrimSpace(value) {
			return nil, false
		} else {
			result[key] = value
		}
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
	// Typed nils do not satisfy the API dependency contract.
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
