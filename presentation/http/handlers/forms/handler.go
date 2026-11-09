package forms

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
	"websiteengine/features/forms"
	"websiteengine/pkg/principal"

	"github.com/google/uuid"

	"websiteengine/pkg/environment"
	"websiteengine/pkg/response"
)

type Handler struct {
	service     *forms.Service
	log         *slog.Logger
	maxJSON     int64
	pageDefault int
	pageMax     int
}

func NewHandler(service *forms.Service, log *slog.Logger, maxJSON int64) *Handler {
	return &Handler{service: service, log: log, maxJSON: maxJSON}
}

// Submit handles POST /api/v1/forms/{key}/submissions. The request body is the
// form data itself, not an envelope.
func (h *Handler) Submit(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, h.maxJSON))
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			response.Error(w, r, http.StatusRequestEntityTooLarge, "request_too_large", "Тело заявки превышает допустимый размер")
			return
		}
		response.Error(w, r, http.StatusBadRequest, "invalid_body", "Не удалось прочитать тело заявки")
		return
	}
	var obj map[string]any
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&obj); err != nil || obj == nil {
		response.Error(w, r, http.StatusBadRequest, "invalid_json", "Тело заявки должно быть JSON-объектом")
		return
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		response.Error(w, r, http.StatusBadRequest, "invalid_json", "Ожидается ровно один JSON-объект")
		return
	}
	submission, err := h.service.Submit(r.Context(), environment.FromContext(r.Context()), r.PathValue("key"), body)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	response.JSON(w, http.StatusCreated, map[string]any{"id": submission.ID, "status": "accepted"})
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	if !validPagination(h.pageDefault, h.pageMax) {
		response.Error(w, r, http.StatusInternalServerError, "configuration_error", "Pagination limits are not configured")
		return
	}
	filter, err := parseFilterWithLimits(w, r, true, h.pageDefault, h.pageMax)
	if err != nil {
		response.Error(w, r, http.StatusBadRequest, "invalid_query", err.Error())
		return
	}
	items, next, fields, err := h.service.List(r.Context(), r.PathValue("key"), filter)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	if items == nil {
		items = []forms.Submission{}
	}
	if fields == nil {
		fields = []forms.FieldDefinition{}
	}
	response.JSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": nullable(next), "fields": fields})
}

func (h *Handler) Export(w http.ResponseWriter, r *http.Request) {
	if !validPagination(h.pageDefault, h.pageMax) {
		response.Error(w, r, http.StatusInternalServerError, "configuration_error", "Pagination limits are not configured")
		return
	}
	filter, err := parseFilterWithLimits(w, r, false, h.pageDefault, h.pageMax)
	if err != nil {
		response.Error(w, r, http.StatusBadRequest, "invalid_query", err.Error())
		return
	}
	items, fields, err := h.service.Export(r.Context(), r.PathValue("key"), filter)
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", "submissions-"+r.PathValue("key")+".csv"))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	cw := csv.NewWriter(w)
	header := []string{"id", "status", "created_at", "updated_at", "processed_by", "branchId", "formKey"}
	for _, field := range fields {
		header = append(header, field.Key)
	}
	_ = cw.Write(header)
	for _, s := range items {
		row := []string{
			s.ID.String(), s.Status, s.CreatedAt.UTC().Format(time.RFC3339), s.UpdatedAt.UTC().Format(time.RFC3339),
		}
		if s.ProcessedBy != nil {
			row = append(row, s.ProcessedBy.String())
		} else {
			row = append(row, "")
		}
		row = append(row, s.Environment, s.FeatureKey)
		var data map[string]any
		dec := json.NewDecoder(strings.NewReader(string(s.Data)))
		dec.UseNumber()
		_ = dec.Decode(&data)
		for _, field := range fields {
			row = append(row, csvValue(data[field.Key]))
		}
		_ = cw.Write(row)
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		h.log.Error("csv export failed", "request_id", response.RequestID(r.Context()), "error", err)
	}
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	id, ok := submissionID(w, r)
	if !ok {
		return
	}
	submission, err := h.service.Get(r.Context(), id)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	response.JSON(w, http.StatusOK, submission)
}

type statusRequest struct {
	Status string `json:"status"`
}

func (h *Handler) UpdateStatus(w http.ResponseWriter, r *http.Request) {
	id, ok := submissionID(w, r)
	if !ok {
		return
	}
	actor, _ := principal.FromContext(r.Context())
	var req statusRequest
	if err := response.DecodeJSON(w, r, &req, h.maxJSON); err != nil {
		response.WriteDecodeError(w, r, err)
		return
	}
	submission, err := h.service.SetStatus(r.Context(), id, req.Status, actor.TokenID)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	response.JSON(w, http.StatusOK, submission)
}

func (h *Handler) Archive(w http.ResponseWriter, r *http.Request) {
	id, ok := submissionID(w, r)
	if !ok {
		return
	}
	submission, err := h.service.Archive(r.Context(), id)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	response.JSON(w, http.StatusOK, submission)
}

func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	id, ok := submissionID(w, r)
	if !ok {
		return
	}
	if err := h.service.Delete(r.Context(), id); err != nil {
		h.writeError(w, r, err)
		return
	}
	response.NoContent(w)
}

func parseFilterWithLimits(w http.ResponseWriter, r *http.Request, withCursor bool, defaultPage, maxPage int) (forms.ListFilter, error) {
	q := r.URL.Query()
	limit, err := response.ParseLimit(q.Get("limit"), defaultPage, maxPage)
	if err != nil {
		return forms.ListFilter{}, err
	}
	filter := forms.ListFilter{
		Status:          strings.TrimSpace(q.Get("status")),
		Search:          strings.TrimSpace(q.Get("q")),
		SearchField:     strings.TrimSpace(q.Get("field")),
		IncludeArchived: q.Get("include_archived") == "true",
		Sort:            strings.ToLower(q.Get("sort")),
		Limit:           limit,
	}
	branchID := strings.TrimSpace(q.Get("branchId"))
	legacy := strings.TrimSpace(q.Get("environment"))
	if branchID != "" && !strings.EqualFold(branchID, "all") {
		parsedBranchID, err := uuid.Parse(branchID)
		if err != nil {
			return forms.ListFilter{}, errors.New("branchId должен быть идентификатором ветки")
		}
		canonicalBranchID := parsedBranchID.String()
		if legacy != "" && !strings.EqualFold(legacy, "all") {
			legacyBranchID, err := uuid.Parse(legacy)
			if err != nil || legacyBranchID.String() != canonicalBranchID {
				return forms.ListFilter{}, errors.New("branchId и environment задают разные ветки")
			}
		}
		filter.Environment = environment.Environment(canonicalBranchID)
	} else if raw := legacy; raw != "" && !strings.EqualFold(raw, "all") {
		env, err := environment.Parse(raw)
		if err != nil {
			return forms.ListFilter{}, err
		}
		filter.Environment = env
	}
	if filter.Sort == "" {
		filter.Sort = "desc"
	}
	if filter.Sort != "asc" && filter.Sort != "desc" {
		return forms.ListFilter{}, errors.New("sort должен быть asc или desc")
	}
	if raw := q.Get("created_from"); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return forms.ListFilter{}, errors.New("created_from должен быть RFC 3339")
		}
		filter.CreatedFrom = &t
	}
	if raw := q.Get("created_to"); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return forms.ListFilter{}, errors.New("created_to должен быть RFC 3339")
		}
		filter.CreatedTo = &t
	}
	if filter.CreatedFrom != nil && filter.CreatedTo != nil && !filter.CreatedTo.After(*filter.CreatedFrom) {
		return forms.ListFilter{}, errors.New("Конец периода должен быть позже начала")
	}
	if withCursor && q.Get("cursor") != "" {
		cursor, ok := response.DecodeCursor(q.Get("cursor"))
		if !ok {
			return forms.ListFilter{}, errors.New("Некорректный cursor")
		}
		id, _ := uuid.Parse(cursor.ID)
		filter.BeforeTime = &cursor.CreatedAt
		filter.BeforeID = &id
	}
	return filter, nil
}

func submissionID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		response.Error(w, r, http.StatusNotFound, "not_found", "Заявка не найдена")
		return uuid.Nil, false
	}
	return id, true
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func csvValue(value any) string {
	if value == nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return safeCSV(v)
	case json.Number:
		return v.String()
	case bool:
		return strconv.FormatBool(v)
	default:
		raw, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		return safeCSV(string(raw))
	}
}

// safeCSV prevents spreadsheet formula injection when a CSV is opened in Excel.
func safeCSV(value string) string {
	if value == "" {
		return value
	}
	trimmed := strings.TrimLeft(value, " \t\r\n")
	if strings.HasPrefix(value, "\t") || strings.HasPrefix(value, "\r") ||
		(trimmed != "" && strings.ContainsRune("=+-@", rune(trimmed[0]))) {
		return "'" + value
	}
	return value
}

func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	var validationErr *forms.ValidationError
	var conflictErr *forms.ConflictError
	var exportLimitErr *forms.ExportLimitError
	switch {
	case errors.As(err, &exportLimitErr):
		response.Error(w, r, http.StatusRequestEntityTooLarge, "export_limit_exceeded", exportLimitErr.Error())
	case errors.As(err, &validationErr):
		details := make([]response.ErrorDetail, 0, len(validationErr.Details))
		for _, d := range validationErr.Details {
			details = append(details, response.ErrorDetail{Path: d.Path, Code: d.Code, Message: d.Message})
		}
		if len(details) > 0 {
			response.Error(w, r, http.StatusUnprocessableEntity, "schema_validation_failed", validationErr.Message, details...)
			return
		}
		response.Error(w, r, http.StatusBadRequest, "validation_failed", validationErr.Message)
	case errors.As(err, &conflictErr):
		response.Error(w, r, http.StatusConflict, "conflict", conflictErr.Message)
	case errors.Is(err, forms.ErrNotFound):
		response.Error(w, r, http.StatusNotFound, "not_found", "Форма или заявка не найдены")
	case errors.Is(err, forms.ErrConflict):
		response.Error(w, r, http.StatusConflict, "conflict", "Конфликт состояния заявки")
	default:
		h.log.Error("form operation failed", "request_id", response.RequestID(r.Context()), "error", err)
		response.Error(w, r, http.StatusInternalServerError, "internal_error", "Внутренняя ошибка сервера")
	}
}

// Branches exposes only names required by submission filters, not management
// settings, ports or assignments which remain site-manager protected.
func (h *Handler) Branches(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.BranchChoices(r.Context())
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	response.JSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) SetLimits(defaultPage, maxPage int) error {
	if !validPagination(defaultPage, maxPage) {
		return errors.New("submission pagination limits are invalid")
	}
	h.pageDefault = defaultPage
	h.pageMax = maxPage
	return nil
}

func validPagination(defaultPage, maxPage int) bool {
	return defaultPage > 0 && maxPage >= defaultPage
}
