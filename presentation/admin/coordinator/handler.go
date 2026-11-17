// Package coordinatoradmin exposes the authenticated Admin API for durable
// Coordinator job summaries and operator retries.
package coordinatoradmin

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/Zubrium/cdn/coordinator"
	"github.com/Zubrium/cdn/core/access"
	"github.com/Zubrium/cdn/core/platform"
	"github.com/google/uuid"
)

const (
	apiPrefix      = "/api/v2/coordinator/jobs"
	maxPageLimit   = 200
	maxCursorBytes = 256
)

type Permission string

const (
	ReadJobs  Permission = "read"
	RetryJobs Permission = "retry"
)

type JobControl interface {
	List(context.Context, coordinator.JobListFilter) (platform.CoordinatorJobPage, error)
	Retry(context.Context, uuid.UUID) error
}

// AuthorizeFunc must authenticate the bearer credential and enforce the
// configured operation permission on every request. It must fail closed.
type AuthorizeFunc func(context.Context, string, Permission) error

// GroupAuthorization is the deliberately narrow Core surface required by this
// presentation. It authenticates a credential and rechecks direct group access
// against PostgreSQL for every request.
type GroupAuthorization interface {
	Authenticate(context.Context, string) (platform.Session, error)
	RequireGroupLevel(context.Context, platform.Session, string, access.Level) error
}

// NewGroupAuthorizer maps job listing to group read and manual retry to group
// write. No administrator-management or global-role shortcut is accepted.
func NewGroupAuthorizer(core GroupAuthorization, group string) (AuthorizeFunc, error) {
	if nilInterface(core) || !access.ValidGroupKey(group) {
		return nil, coordinator.ErrInvalid
	}
	return func(ctx context.Context, credential string, permission Permission) error {
		if ctx == nil || credential == "" {
			return platform.ErrUnauthorized
		}
		session, err := core.Authenticate(ctx, credential)
		if err != nil {
			return err
		}
		level := access.LevelRead
		if permission == RetryJobs {
			level = access.LevelWrite
		} else if permission != ReadJobs {
			return platform.ErrForbidden
		}
		return core.RequireGroupLevel(ctx, session, group, level)
	}, nil
}

type Config struct {
	DefaultPageSize int
	MaxPageSize     int
}

type Handler struct {
	jobs      JobControl
	authorize AuthorizeFunc
	cfg       Config
	mux       *http.ServeMux
}

type Cursor struct {
	CreatedAt string `json:"created_at"`
	ID        string `json:"id"`
}

type JobSummary struct {
	ID          string     `json:"id"`
	Type        string     `json:"type"`
	State       string     `json:"state"`
	Attempts    int        `json:"attempts"`
	MaxAttempts int        `json:"max_attempts"`
	AvailableAt time.Time  `json:"available_at"`
	LeaseUntil  *time.Time `json:"lease_until,omitempty"`
	ErrorCode   string     `json:"error_code,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

type ListResponse struct {
	Items      []JobSummary `json:"items"`
	NextCursor string       `json:"next_cursor,omitempty"`
}

type PermissionsResponse struct {
	CanRetry bool `json:"can_retry"`
}

type errorResponse struct {
	Error string `json:"error"`
}

func NewHandler(jobs JobControl, authorize AuthorizeFunc, config Config) (*Handler, error) {
	if nilInterface(jobs) || authorize == nil || config.DefaultPageSize < 1 || config.MaxPageSize < config.DefaultPageSize || config.MaxPageSize > maxPageLimit {
		return nil, coordinator.ErrInvalid
	}
	handler := &Handler{jobs: jobs, authorize: authorize, cfg: config, mux: http.NewServeMux()}
	handler.mux.HandleFunc("GET "+apiPrefix, handler.list)
	handler.mux.HandleFunc("GET "+apiPrefix+"/permissions", handler.permissions)
	handler.mux.HandleFunc("POST "+apiPrefix+"/{jobID}/retry", handler.retry)
	return handler, nil
}

func (h *Handler) permissions(w http.ResponseWriter, r *http.Request) {
	if !h.authorizeRequest(w, r, ReadJobs) {
		return
	}
	credential, ok := bearerCredential(r.Header.Values("Authorization")[0])
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	err := invokeAuthorizer(h.authorize, r.Context(), credential, RetryJobs)
	if errors.Is(err, platform.ErrForbidden) {
		writeJSON(w, http.StatusOK, PermissionsResponse{CanRetry: false})
		return
	}
	if err != nil {
		writeAuthorizationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, PermissionsResponse{CanRetry: true})
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	h.mux.ServeHTTP(w, r)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	if !h.authorizeRequest(w, r, ReadJobs) {
		return
	}
	query := r.URL.Query()
	for key := range query {
		if key != "type" && key != "state" && key != "limit" && key != "before" {
			writeError(w, http.StatusBadRequest, "invalid_query")
			return
		}
	}
	typeValue, valid := singleQueryValue(query, "type")
	if !valid {
		writeError(w, http.StatusBadRequest, "invalid_query")
		return
	}
	stateValue, valid := singleQueryValue(query, "state")
	if !valid {
		writeError(w, http.StatusBadRequest, "invalid_query")
		return
	}
	limitValue, valid := singleQueryValue(query, "limit")
	if !valid {
		writeError(w, http.StatusBadRequest, "invalid_query")
		return
	}
	limit := h.cfg.DefaultPageSize
	if limitValue != "" {
		parsed, err := strconv.Atoi(limitValue)
		if err != nil || parsed < 1 || parsed > h.cfg.MaxPageSize {
			writeError(w, http.StatusBadRequest, "invalid_query")
			return
		}
		limit = parsed
	}
	beforeValue, valid := singleQueryValue(query, "before")
	if !valid {
		writeError(w, http.StatusBadRequest, "invalid_query")
		return
	}
	var before *platform.CoordinatorJobCursor
	if beforeValue != "" {
		cursor, err := decodeCursor(beforeValue)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_cursor")
			return
		}
		before = cursor
	}
	page, err := h.jobs.List(r.Context(), coordinator.JobListFilter{
		Type: typeValue, State: stateValue, Limit: limit, Before: before,
	})
	if err != nil {
		writeOperationError(w, err)
		return
	}
	response := ListResponse{Items: make([]JobSummary, 0, len(page.Items))}
	for _, item := range page.Items {
		response.Items = append(response.Items, JobSummary{
			ID: item.ID.String(), Type: item.Type, State: item.State, Attempts: item.Attempts,
			MaxAttempts: item.MaxAttempts, AvailableAt: item.AvailableAt, LeaseUntil: item.LeaseUntil,
			ErrorCode: item.ErrorCode, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt,
		})
	}
	if page.Next != nil {
		response.NextCursor = encodeCursor(*page.Next)
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) retry(w http.ResponseWriter, r *http.Request) {
	if !h.authorizeRequest(w, r, RetryJobs) {
		return
	}
	if r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
		writeError(w, http.StatusBadRequest, "body_not_allowed")
		return
	}
	jobID, err := uuid.Parse(r.PathValue("jobID"))
	if err != nil || jobID == uuid.Nil {
		writeError(w, http.StatusBadRequest, "invalid_job_id")
		return
	}
	if err := h.jobs.Retry(r.Context(), jobID); err != nil {
		writeOperationError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) authorizeRequest(w http.ResponseWriter, r *http.Request, permission Permission) bool {
	values := r.Header.Values("Authorization")
	if len(values) != 1 {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return false
	}
	credential, ok := bearerCredential(values[0])
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return false
	}
	if err := invokeAuthorizer(h.authorize, r.Context(), credential, permission); err != nil {
		writeAuthorizationError(w, err)
		return false
	}
	return true
}

func bearerCredential(value string) (string, bool) {
	if !strings.HasPrefix(value, "Bearer ") {
		return "", false
	}
	credential := strings.TrimPrefix(value, "Bearer ")
	if credential == "" || len(credential) > 512 || strings.TrimSpace(credential) != credential || strings.ContainsAny(credential, " \t\r\n") {
		return "", false
	}
	return credential, true
}

func invokeAuthorizer(authorize AuthorizeFunc, ctx context.Context, credential string, permission Permission) (err error) {
	defer func() {
		if recover() != nil {
			err = errors.New("authorization failed")
		}
	}()
	return authorize(ctx, credential, permission)
}

func singleQueryValue(query map[string][]string, key string) (string, bool) {
	values, exists := query[key]
	if !exists {
		return "", true
	}
	if len(values) != 1 || len(values[0]) > 256 {
		return "", false
	}
	return values[0], true
}

func encodeCursor(cursor platform.CoordinatorJobCursor) string {
	payload, err := json.Marshal(Cursor{CreatedAt: cursor.CreatedAt.UTC().Format(time.RFC3339Nano), ID: cursor.ID.String()})
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(payload)
}

func decodeCursor(value string) (*platform.CoordinatorJobCursor, error) {
	if value == "" || len(value) > maxCursorBytes {
		return nil, coordinator.ErrInvalid
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || base64.RawURLEncoding.EncodeToString(decoded) != value {
		return nil, coordinator.ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(decoded))
	decoder.DisallowUnknownFields()
	var cursor Cursor
	if err := decoder.Decode(&cursor); err != nil {
		return nil, coordinator.ErrInvalid
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, coordinator.ErrInvalid
	}
	canonical, err := json.Marshal(cursor)
	if err != nil || !bytes.Equal(canonical, decoded) {
		return nil, coordinator.ErrInvalid
	}
	createdAt, err := time.Parse(time.RFC3339Nano, cursor.CreatedAt)
	if err != nil || createdAt.IsZero() || createdAt.UTC().Format(time.RFC3339Nano) != cursor.CreatedAt {
		return nil, coordinator.ErrInvalid
	}
	id, err := uuid.Parse(cursor.ID)
	if err != nil || id == uuid.Nil || id.String() != cursor.ID {
		return nil, coordinator.ErrInvalid
	}
	return &platform.CoordinatorJobCursor{CreatedAt: createdAt.UTC(), ID: id}, nil
}

func writeAuthorizationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, platform.ErrUnauthorized):
		writeError(w, http.StatusUnauthorized, "unauthorized")
	case errors.Is(err, platform.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden")
	default:
		writeError(w, http.StatusServiceUnavailable, "authorization_unavailable")
	}
}

func writeOperationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, coordinator.ErrInvalid):
		writeError(w, http.StatusBadRequest, "invalid_request")
	case errors.Is(err, platform.ErrCoordinatorJobMissing):
		writeError(w, http.StatusNotFound, "not_found")
	case errors.Is(err, platform.ErrCoordinatorConflict), errors.Is(err, platform.ErrCoordinatorStale):
		writeError(w, http.StatusConflict, "conflict")
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		writeError(w, http.StatusServiceUnavailable, "unavailable")
	default:
		writeError(w, http.StatusServiceUnavailable, "unavailable")
	}
}

func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, errorResponse{Error: code})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func nilInterface(value any) bool {
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
