// Package contentapi adapts the Content Engine contract to the Admin HTTP API.
package contentapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"

	engine "github.com/Zubrium/cdn/pkg/contracts"
	httpjson "github.com/Zubrium/cdn/pkg/json"
	adminhttpauth "github.com/Zubrium/cdn/presentation/httpauth"
)

const apiPrefix = "/api/admin/content"

type Config struct {
	MaxJSONBytes    int64
	MaxPageSize     int
	DefaultPageSize int
	AllowedOrigins  []string
}

type Handler struct {
	api       engine.AdminContentAPI
	schemas   SchemaSnapshots
	cfg       Config
	transport *adminhttpauth.Transport
	mux       *http.ServeMux
}

func NewHandler(api engine.AdminContentAPI, schemas SchemaSnapshots, config Config) (*Handler, error) {
	if nilInterface(api) || nilInterface(schemas) {
		return nil, errors.New("Content API and schema projection provider are required")
	}
	if config.MaxJSONBytes < 1 || config.MaxJSONBytes > 16<<20 ||
		config.MaxPageSize < 1 || config.MaxPageSize > 500 || config.DefaultPageSize < 1 || config.DefaultPageSize > config.MaxPageSize {
		return nil, errors.New("invalid Content API configuration")
	}
	transport, err := adminhttpauth.New(config.AllowedOrigins)
	if err != nil {
		return nil, err
	}
	h := &Handler{api: api, schemas: schemas, cfg: config, transport: transport, mux: http.NewServeMux()}
	h.mux.HandleFunc("GET "+apiPrefix+"/locales", h.getLocaleConfig)
	h.mux.HandleFunc("PUT "+apiPrefix+"/locales", h.replaceLocaleConfig)
	h.mux.HandleFunc("GET "+apiPrefix+"/snapshots/{snapshotID}/workspace", h.getWorkspace)
	h.mux.HandleFunc("PUT "+apiPrefix+"/snapshots/{snapshotID}/workspace", h.saveWorkspaceDraft)
	h.mux.HandleFunc("POST "+apiPrefix+"/snapshots/{snapshotID}/workspace/publish", h.publishWorkspace)
	h.mux.HandleFunc("GET "+apiPrefix+"/snapshots/{snapshotID}/history", h.listHistory)
	h.mux.HandleFunc("POST "+apiPrefix+"/snapshots/{snapshotID}/history/{historyID}/restore", h.restoreHistory)
	return h, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	h.mux.ServeHTTP(w, r)
}

func (h *Handler) projections(ctx context.Context, pageSnapshotID, locale string) (engine.AdminContentProjections, error) {
	if nilInterface(h.schemas) {
		return engine.AdminContentProjections{}, engine.ErrAdminContentUnavailable
	}
	projections, err := h.schemas.GetContentProjections(ctx, pageSnapshotID)
	if err != nil {
		// Preserve typed authorization/scope failures; writeEngineError maps
		// unknown provider errors to 503 without exposing their details.
		return engine.AdminContentProjections{}, err
	}
	projections.Locale = locale
	if !projections.VerifyScope(pageSnapshotID) {
		return engine.AdminContentProjections{}, engine.ErrAdminContentUnavailable
	}
	return projections, nil
}

func (h *Handler) getLocaleConfig(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	config, err := h.api.GetLocaleConfig(ctx)
	if err != nil {
		writeEngineError(w, err)
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(config.Revision, 10)+`"`)
	writeJSON(w, http.StatusOK, config)
}

func (h *Handler) replaceLocaleConfig(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	idempotencyKey, valid := readIdempotencyKey(r)
	if !valid {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	var request engine.AdminContentReplaceLocaleConfigRequest
	if err := decodeJSON(w, r, h.cfg.MaxJSONBytes, &request); err != nil {
		writeDecodeError(w, err)
		return
	}
	if request.ExpectedRevision < 0 || request.Locales == nil || len(request.Locales) > 128 || request.DefaultLocale == "" {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	request.IdempotencyKey = idempotencyKey
	config, err := h.api.ReplaceLocaleConfig(ctx, request)
	if err != nil {
		writeEngineError(w, err)
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(config.Revision, 10)+`"`)
	writeJSON(w, http.StatusOK, config)
}

func (h *Handler) getWorkspace(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	snapshotID := r.PathValue("snapshotID")
	query, valid := readQuery(r, "locale")
	if !valid || query["locale"] == "" || !validID(snapshotID) {
		writeError(w, http.StatusBadRequest, "invalid_query")
		return
	}
	projection, err := h.projections(ctx, snapshotID, query["locale"])
	if err != nil {
		writeEngineError(w, err)
		return
	}
	workspace, err := h.api.GetWorkspace(ctx, projection)
	if err != nil {
		writeEngineError(w, err)
		return
	}
	if workspace.SnapshotID != snapshotID || workspace.Records == nil || workspace.Selections == nil || workspace.Placements == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	writeJSON(w, http.StatusOK, workspace)
}

func (h *Handler) saveWorkspaceDraft(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	var request engine.AdminContentSaveWorkspaceDraftRequest
	if err := decodeJSON(w, r, h.cfg.MaxJSONBytes, &request); err != nil {
		writeDecodeError(w, err)
		return
	}
	request.SnapshotID = r.PathValue("snapshotID")
	request.IdempotencyKey, ok = readIdempotencyKey(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_idempotency_key")
		return
	}
	if !validID(request.SnapshotID) || request.ExpectedRevision < 0 || request.ExpectedSelectionRevision < 0 || request.Records == nil || request.Selections == nil ||
		len(request.Records) > 50000 || len(request.Selections) > 50000 || !validWorkspaceInput(request) {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	query, valid := readQuery(r, "locale")
	if !valid || query["locale"] == "" {
		writeError(w, http.StatusBadRequest, "invalid_query")
		return
	}
	projection, err := h.projections(ctx, request.SnapshotID, query["locale"])
	if err != nil {
		writeEngineError(w, err)
		return
	}
	workspace, err := h.api.SaveWorkspaceDraft(ctx, projection, request)
	if err != nil {
		writeEngineError(w, err)
		return
	}
	if workspace.SnapshotID != request.SnapshotID || workspace.Records == nil || workspace.Selections == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	writeJSON(w, http.StatusOK, workspace)
}

func (h *Handler) publishWorkspace(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	var body struct {
		ExpectedRevision          int64 `json:"expected_revision"`
		ExpectedSelectionRevision int64 `json:"expected_selection_revision"`
	}
	if err := decodeJSON(w, r, h.cfg.MaxJSONBytes, &body); err != nil {
		writeDecodeError(w, err)
		return
	}
	snapshotID := r.PathValue("snapshotID")
	idempotencyKey, valid := readIdempotencyKey(r)
	if !valid || !validID(snapshotID) || body.ExpectedRevision < 0 || body.ExpectedSelectionRevision < 0 {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	query, valid := readQuery(r, "locale")
	if !valid || query["locale"] == "" {
		writeError(w, http.StatusBadRequest, "invalid_query")
		return
	}
	projection, err := h.projections(ctx, snapshotID, query["locale"])
	if err != nil {
		writeEngineError(w, err)
		return
	}
	workspace, err := h.api.PublishWorkspace(ctx, projection, engine.AdminContentPublishWorkspaceRequest{SnapshotID: snapshotID, IdempotencyKey: idempotencyKey, ExpectedRevision: body.ExpectedRevision, ExpectedSelectionRevision: body.ExpectedSelectionRevision})
	if err != nil {
		writeEngineError(w, err)
		return
	}
	if workspace.SnapshotID != snapshotID {
		writeError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	writeJSON(w, http.StatusOK, workspace)
}

func (h *Handler) listHistory(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	query, valid := readQuery(r, "cursor", "limit", "locale")
	if !valid || query["locale"] == "" || len(query["cursor"]) > 1024 || !validID(r.PathValue("snapshotID")) {
		writeError(w, http.StatusBadRequest, "invalid_query")
		return
	}
	limit, valid := parseLimit(query["limit"], h.cfg)
	if !valid {
		writeError(w, http.StatusBadRequest, "invalid_query")
		return
	}
	snapshotID := r.PathValue("snapshotID")
	projection, err := h.projections(ctx, snapshotID, query["locale"])
	if err != nil {
		writeEngineError(w, err)
		return
	}
	page, err := h.api.ListHistory(ctx, projection, query["cursor"], limit)
	if err != nil {
		writeEngineError(w, err)
		return
	}
	if page.Items == nil {
		page.Items = []engine.AdminContentHistoryItem{}
	}
	writeJSON(w, http.StatusOK, page)
}

func (h *Handler) restoreHistory(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	var body struct {
		ExpectedRevision          int64 `json:"expected_revision"`
		ExpectedSelectionRevision int64 `json:"expected_selection_revision"`
	}
	if err := decodeJSON(w, r, h.cfg.MaxJSONBytes, &body); err != nil {
		writeDecodeError(w, err)
		return
	}
	snapshotID, historyID := r.PathValue("snapshotID"), r.PathValue("historyID")
	idempotencyKey, valid := readIdempotencyKey(r)
	if !valid || !validID(snapshotID) || !validID(historyID) || body.ExpectedRevision < 0 || body.ExpectedSelectionRevision < 0 {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	query, valid := readQuery(r, "locale")
	if !valid || query["locale"] == "" {
		writeError(w, http.StatusBadRequest, "invalid_query")
		return
	}
	projection, err := h.projections(ctx, snapshotID, query["locale"])
	if err != nil {
		writeEngineError(w, err)
		return
	}
	workspace, err := h.api.RestoreHistory(ctx, projection, engine.AdminContentRestoreHistoryRequest{SnapshotID: snapshotID, HistoryID: historyID, IdempotencyKey: idempotencyKey, ExpectedRevision: body.ExpectedRevision, ExpectedSelectionRevision: body.ExpectedSelectionRevision})
	if err != nil {
		writeEngineError(w, err)
		return
	}
	if workspace.SnapshotID != snapshotID {
		writeError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	writeJSON(w, http.StatusOK, workspace)
}

func validWorkspaceInput(request engine.AdminContentSaveWorkspaceDraftRequest) bool {
	ids := make(map[string]struct{}, len(request.Records))
	clientKeys := make(map[string]struct{}, len(request.Records))
	for _, record := range request.Records {
		if !validID(record.EntityID) || record.SortOrder < 0 || record.Values == nil || (record.ID == "") == (record.ClientKey == "") {
			return false
		}
		if record.ID != "" {
			if !validID(record.ID) {
				return false
			}
			if _, duplicate := ids[record.ID]; duplicate {
				return false
			}
			ids[record.ID] = struct{}{}
		} else {
			if !validClientKey(record.ClientKey) {
				return false
			}
			if _, duplicate := clientKeys[record.ClientKey]; duplicate {
				return false
			}
			clientKeys[record.ClientKey] = struct{}{}
		}
	}
	for id := range ids {
		if _, duplicate := clientKeys[id]; duplicate {
			return false
		}
	}
	pairs := make(map[string]struct{}, len(request.Selections))
	for _, selection := range request.Selections {
		if !validID(selection.PlacementID) || !validText(selection.InputKey, 128) || (selection.Mode != "all" && selection.Mode != "ids") {
			return false
		}
		key := selection.PlacementID + "\x00" + selection.InputKey
		if _, duplicate := pairs[key]; duplicate {
			return false
		}
		pairs[key] = struct{}{}
		if selection.Mode == "all" && len(selection.RecordIDs) != 0 {
			return false
		}
		seen := make(map[string]struct{}, len(selection.RecordIDs))
		for _, id := range selection.RecordIDs {
			if _, exists := clientKeys[id]; exists {
				// Client keys may happen to be syntactically valid server IDs; resolve
				// them against their explicit request-local namespace first.
			} else if _, exists := ids[id]; !exists || !validID(id) {
				return false
			}
			if _, duplicate := seen[id]; duplicate {
				return false
			}
			seen[id] = struct{}{}
		}
	}
	return true
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

func readIdempotencyKey(r *http.Request) (string, bool) {
	values := r.Header.Values("Idempotency-Key")
	if len(values) != 1 {
		return "", false
	}
	value := values[0]
	if len(value) == 0 || len(value) > 128 || strings.TrimSpace(value) != value {
		return "", false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || strings.ContainsRune("._:-", char) {
			continue
		}
		return "", false
	}
	return value, true
}

func decodeJSON(w http.ResponseWriter, r *http.Request, limit int64, target any) error {
	err := httpjson.DecodeRequest(w, r, limit, target)
	switch {
	case errors.Is(err, httpjson.ErrTooLarge):
		return engine.ErrAdminContentTooLarge
	case errors.Is(err, httpjson.ErrInvalid):
		return engine.ErrAdminContentInvalid
	default:
		return err
	}
}

func writeDecodeError(w http.ResponseWriter, err error) {
	if errors.Is(err, engine.ErrAdminContentTooLarge) {
		writeError(w, http.StatusRequestEntityTooLarge, "too_large")
		return
	}
	writeError(w, http.StatusBadRequest, "invalid_request")
}

func writeEngineError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, engine.ErrAdminContentUnauthorized):
		writeError(w, http.StatusUnauthorized, "unauthorized")
	case errors.Is(err, engine.ErrAdminContentForbidden):
		writeError(w, http.StatusForbidden, "forbidden")
	case errors.Is(err, engine.ErrAdminContentNotFound):
		writeError(w, http.StatusNotFound, "not_found")
	case errors.Is(err, engine.ErrAdminContentConflict):
		writeError(w, http.StatusConflict, "conflict")
	case errors.Is(err, engine.ErrAdminContentInvalid):
		writeError(w, http.StatusBadRequest, "invalid_request")
	case errors.Is(err, engine.ErrAdminContentTooLarge):
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

func validClientKey(value string) bool {
	return validText(value, 128) && !strings.ContainsAny(value, "\r\n\t")
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
