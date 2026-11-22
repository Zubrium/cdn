// Package formsapi adapts the Forms Engine contract to the Admin HTTP API.
package formsapi

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	engine "github.com/Zubrium/cdn/engines/forms/presentation"
	httpjson "github.com/Zubrium/cdn/pkg/json"
	adminhttpauth "github.com/Zubrium/cdn/presentation/admin/base/admin/auth"
)

const apiPrefix = "/api/admin/forms"

type Config struct {
	MaxJSONBytes        int64
	MaxPageSize         int
	DefaultPageSize     int
	ExportMaxConcurrent int
	ExportMaxRows       int
	ExportMaxBytes      int64
	AllowedOrigins      []string
}

type Handler struct {
	api         engine.API
	cfg         Config
	transport   *adminhttpauth.Transport
	mux         *http.ServeMux
	exportSlots chan struct{}
}

func NewHandler(api engine.API, config Config) (*Handler, error) {
	if nilInterface(api) || config.MaxJSONBytes < 1 || config.MaxJSONBytes > 16<<20 ||
		config.MaxPageSize < 1 || config.MaxPageSize > 500 || config.DefaultPageSize < 1 || config.DefaultPageSize > config.MaxPageSize ||
		config.ExportMaxConcurrent < 1 || config.ExportMaxConcurrent > 16 ||
		config.ExportMaxRows < 1 || config.ExportMaxRows > 10000 || config.ExportMaxBytes < 1024 || config.ExportMaxBytes > 64<<20 {
		return nil, errors.New("invalid Forms API configuration")
	}
	transport, err := adminhttpauth.New(config.AllowedOrigins)
	if err != nil {
		return nil, err
	}
	h := &Handler{api: api, cfg: config, transport: transport, mux: http.NewServeMux(), exportSlots: make(chan struct{}, config.ExportMaxConcurrent)}
	h.mux.HandleFunc("GET "+apiPrefix+"/schemas", h.listSchemas)
	h.mux.HandleFunc("GET "+apiPrefix+"/schemas/{schemaID}", h.getSchema)
	h.mux.HandleFunc("PUT "+apiPrefix+"/schemas", h.saveSchema)
	h.mux.HandleFunc("GET "+apiPrefix+"/submissions", h.listSubmissions)
	h.mux.HandleFunc("GET "+apiPrefix+"/submissions/export", h.exportSubmissions)
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
	query, valid := readQuery(r, "cursor", "limit")
	if !valid || len(query["cursor"]) > 1024 {
		writeError(w, http.StatusBadRequest, "invalid_query")
		return
	}
	limit, valid := parseLimit(query["limit"], h.cfg)
	if !valid {
		writeError(w, http.StatusBadRequest, "invalid_query")
		return
	}
	page, err := h.api.ListSchemas(ctx, query["cursor"], limit)
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
	_, valid := readQuery(r)
	schemaID := r.PathValue("schemaID")
	if !valid || !validID(schemaID) {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	schema, err := h.api.GetSchema(ctx, schemaID)
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
	if (request.SchemaID != "" && !validID(request.SchemaID)) || !validText(request.Key, 128) ||
		request.Fields == nil || request.ExpectedRevision < 0 ||
		(request.SchemaID == "" && request.ExpectedRevision != 0) ||
		(request.SchemaID != "" && request.ExpectedRevision == 0) {
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
	query, valid := readQuery(r, "form_id", "key", "state", "cursor", "limit")
	if !valid || !validID(query["form_id"]) || (query["key"] != "" && !validText(query["key"], 128)) ||
		len(query["state"]) > 64 || len(query["cursor"]) > 1024 {
		writeError(w, http.StatusBadRequest, "invalid_query")
		return
	}
	limit, valid := parseLimit(query["limit"], h.cfg)
	if !valid {
		writeError(w, http.StatusBadRequest, "invalid_query")
		return
	}
	page, err := h.api.ListSubmissions(ctx, engine.ListSubmissionsRequest{FormID: query["form_id"], Key: query["key"], State: query["state"], Cursor: query["cursor"], Limit: limit})
	if err != nil {
		writeEngineError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (h *Handler) exportSubmissions(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	query, valid := readQuery(r, "form_id", "key", "state")
	if !valid || !validID(query["form_id"]) || (query["key"] != "" && !validText(query["key"], 128)) || len(query["state"]) > 64 {
		writeError(w, http.StatusBadRequest, "invalid_query")
		return
	}
	select {
	case h.exportSlots <- struct{}{}:
		defer func() { <-h.exportSlots }()
	default:
		w.Header().Set("Retry-After", "1")
		writeError(w, http.StatusTooManyRequests, "export_busy")
		return
	}

	rows, err := h.readExportRows(ctx, query["form_id"], query["key"], query["state"])
	if err != nil {
		if errors.Is(err, errExportLimit) {
			writeError(w, http.StatusRequestEntityTooLarge, "export_limit_exceeded")
		} else if errors.Is(err, errExportCursor) {
			writeError(w, http.StatusServiceUnavailable, "unavailable")
		} else {
			writeEngineError(w, err)
		}
		return
	}

	data, err := h.encodeExport(rows)
	if errors.Is(err, errExportLimit) {
		writeError(w, http.StatusRequestEntityTooLarge, "export_limit_exceeded")
		return
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="forms-`+query["form_id"]+`-submissions.csv"`)
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

type exportRow struct {
	submission engine.Submission
	valuesJSON []byte
}

var (
	errExportLimit  = errors.New("Forms CSV export limit exceeded")
	errExportCursor = errors.New("Forms CSV export cursor did not advance")
)

func (h *Handler) readExportRows(ctx context.Context, formID, formKey, state string) ([]exportRow, error) {
	rows := make([]exportRow, 0)
	cursor := ""
	seenCursors := make(map[string]struct{})
	var encodedValuesBytes int64
	maxPages := h.cfg.ExportMaxRows + 1
	for pageNumber := 0; pageNumber < maxPages; pageNumber++ {
		page, err := h.api.ListSubmissions(ctx, engine.ListSubmissionsRequest{
			FormID: formID, Key: formKey, State: state, Cursor: cursor, Limit: h.cfg.DefaultPageSize,
		})
		if err != nil {
			return nil, err
		}
		if len(page.Items) > h.cfg.ExportMaxRows-len(rows) {
			return nil, errExportLimit
		}
		for _, submission := range page.Items {
			values, err := json.Marshal(sanitizeExportValue(submission.Values))
			if err != nil {
				return nil, fmt.Errorf("encode Forms submission values: %w", err)
			}
			encodedValuesBytes += int64(len(values))
			if encodedValuesBytes > h.cfg.ExportMaxBytes {
				return nil, errExportLimit
			}
			submission.Values = nil
			rows = append(rows, exportRow{submission: submission, valuesJSON: values})
		}
		next := page.NextCursor
		if len(page.Items) == 0 && next != "" {
			return nil, errExportCursor
		}
		if next == "" {
			return rows, nil
		}
		if len(next) > 1024 || next == cursor {
			return nil, errExportCursor
		}
		if _, exists := seenCursors[next]; exists {
			return nil, errExportCursor
		}
		seenCursors[next] = struct{}{}
		cursor = next
	}
	return nil, errExportCursor
}

func (h *Handler) encodeExport(rows []exportRow) ([]byte, error) {
	buffer := &limitedBuffer{limit: h.cfg.ExportMaxBytes}
	csvWriter := csv.NewWriter(buffer)
	if err := csvWriter.Write([]string{"id", "form_id", "schema_version", "state", "created_at", "values_json"}); err != nil {
		return nil, err
	}
	for _, row := range rows {
		if err := csvWriter.Write([]string{
			safeCSVCell(row.submission.ID), safeCSVCell(row.submission.FormID),
			safeCSVCell(strconv.FormatInt(row.submission.SchemaVersion, 10)), safeCSVCell(row.submission.State),
			safeCSVCell(row.submission.CreatedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")), safeCSVCell(string(row.valuesJSON)),
		}); err != nil {
			return nil, err
		}
	}
	csvWriter.Flush()
	if err := csvWriter.Error(); err != nil {
		if errors.Is(err, errExportLimit) {
			return nil, errExportLimit
		}
		return nil, err
	}
	return buffer.Bytes(), nil
}

func safeCSVCell(value string) string {
	firstOriginal, _ := utf8.DecodeRuneInString(value)
	if unicode.IsControl(firstOriginal) {
		return "'" + value
	}
	trimmed := strings.TrimLeftFunc(value, unicode.IsSpace)
	if trimmed == "" {
		return value
	}
	first, _ := utf8.DecodeRuneInString(trimmed)
	if strings.ContainsRune("=+-@", first) {
		return "'" + value
	}
	return value
}

func sanitizeExportValue(value any) any {
	switch typed := value.(type) {
	case string:
		return safeCSVCell(typed)
	case map[string]any:
		clean := make(map[string]any, len(typed))
		for key, nested := range typed {
			clean[key] = sanitizeExportValue(nested)
		}
		return clean
	case []any:
		clean := make([]any, len(typed))
		for index, nested := range typed {
			clean[index] = sanitizeExportValue(nested)
		}
		return clean
	default:
		return value
	}
}

type limitedBuffer struct {
	buffer bytes.Buffer
	limit  int64
}

func (buffer *limitedBuffer) Write(value []byte) (int, error) {
	if int64(buffer.buffer.Len())+int64(len(value)) > buffer.limit {
		return 0, errExportLimit
	}
	return buffer.buffer.Write(value)
}

func (buffer *limitedBuffer) Bytes() []byte {
	return buffer.buffer.Bytes()
}

func (h *Handler) getSubmission(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	_, valid := readQuery(r)
	submissionID := r.PathValue("submissionID")
	if !valid || !validID(submissionID) {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	submission, err := h.api.GetSubmission(ctx, submissionID)
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
		Status           string `json:"status"`
		ExpectedRevision int64  `json:"expected_revision"`
	}
	if err := decodeJSON(w, r, h.cfg.MaxJSONBytes, &request); err != nil {
		writeDecodeError(w, err)
		return
	}
	submissionID := r.PathValue("submissionID")
	if !validText(request.Status, 64) || !validID(submissionID) || request.ExpectedRevision < 1 {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	submission, err := h.api.SetSubmissionStatus(ctx, engine.SetSubmissionStatusRequest{SubmissionID: submissionID, Status: request.Status, ExpectedRevision: request.ExpectedRevision})
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
	_, valid := readQuery(r)
	revision, revisionOK := parseIfMatchRevision(r.Header.Values("If-Match"))
	submissionID := r.PathValue("submissionID")
	if !valid || !validID(submissionID) || !revisionOK {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if err := h.api.DeleteSubmission(ctx, engine.DeleteSubmissionRequest{SubmissionID: submissionID, ExpectedRevision: revision}); err != nil {
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

func parseIfMatchRevision(values []string) (int64, bool) {
	if len(values) != 1 {
		return 0, false
	}
	value := values[0]
	if len(value) < 4 || len(value) > 32 || !strings.HasPrefix(value, `"r`) || !strings.HasSuffix(value, `"`) {
		return 0, false
	}
	raw := value[2 : len(value)-1]
	revision, err := strconv.ParseInt(raw, 10, 64)
	return revision, err == nil && revision >= 1 && strconv.FormatInt(revision, 10) == raw
}

func setRevisionETag(w http.ResponseWriter, revision int64) {
	if revision > 0 {
		w.Header().Set("ETag", `"r`+strconv.FormatInt(revision, 10)+`"`)
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, limit int64, target any) error {
	err := httpjson.DecodeRequest(w, r, limit, target)
	switch {
	case errors.Is(err, httpjson.ErrTooLarge):
		return engine.ErrTooLarge
	case errors.Is(err, httpjson.ErrInvalid):
		return engine.ErrInvalid
	default:
		return err
	}
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
