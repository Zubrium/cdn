// Package coordinatoradmin exposes the authenticated Admin API for workflows,
// their task graphs, and operator task retries.
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
	"time"

	"github.com/Zubrium/cdn/pkg/contracts"
	adminhttpauth "github.com/Zubrium/cdn/presentation/admin/base/admin/auth"
	"github.com/google/uuid"
)

const (
	apiPrefix      = "/api/coordinator/workflows"
	maxPageLimit   = 200
	maxCursorBytes = 256
)

type Permission string

const (
	ReadWorkflows Permission = "read"
	RetryTasks    Permission = "retry"
)

type WorkflowControl interface {
	List(context.Context, contracts.AdminWorkflowListFilter) (contracts.AdminWorkflowPage, error)
	Get(context.Context, uuid.UUID, contracts.AdminWorkflowDetailFilter) (contracts.AdminWorkflowDetail, error)
	RetryTask(context.Context, uuid.UUID, uuid.UUID) error
}

// AuthorizeFunc must authenticate the bearer credential and enforce the
// configured operation permission on every request. It must fail closed.
type AuthorizeFunc func(context.Context, string, Permission) error

// GroupAuthorization authenticates a credential and rechecks direct group
// access against PostgreSQL for every request.
type GroupAuthorization interface {
	Authenticate(context.Context, string) (contracts.AuthorizationSubject, error)
	RequireGroupLevel(context.Context, contracts.AuthorizationSubject, string, contracts.AccessLevel) error
}

// NewGroupAuthorizer maps workflow reads to group read and task retry to group
// write. No administrator-management or global-role shortcut is accepted.
func NewGroupAuthorizer(core GroupAuthorization, group string) (AuthorizeFunc, error) {
	if nilInterface(core) || !contracts.ValidAccessGroupKey(group) {
		return nil, contracts.ErrWorkflowInputInvalid
	}
	return func(ctx context.Context, credential string, permission Permission) error {
		if ctx == nil || credential == "" {
			return contracts.ErrAccessUnauthorized
		}
		session, err := core.Authenticate(ctx, credential)
		if err != nil {
			return err
		}
		level := contracts.AccessLevelRead
		if permission == RetryTasks {
			level = contracts.AccessLevelWrite
		} else if permission != ReadWorkflows {
			return contracts.ErrAccessForbidden
		}
		return core.RequireGroupLevel(ctx, session, group, level)
	}, nil
}

type Config struct {
	DefaultPageSize  int
	MaxPageSize      int
	AllowedOrigins   []string
	SessionAuthorize AuthorizeFunc
}

type Handler struct {
	control          WorkflowControl
	authorize        AuthorizeFunc
	sessionAuthorize AuthorizeFunc
	transport        *adminhttpauth.Transport
	cfg              Config
	mux              *http.ServeMux
}

type Cursor struct {
	CreatedAt string `json:"created_at"`
	ID        string `json:"id"`
}

type decodedCursor struct {
	CreatedAt time.Time
	ID        uuid.UUID
}

type WorkflowSummary struct {
	ID            string    `json:"id"`
	OwnerEngineID string    `json:"owner_engine_id"`
	State         string    `json:"state"`
	TaskCount     int       `json:"task_count"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type WorkflowListResponse struct {
	Items      []WorkflowSummary `json:"items"`
	NextCursor string            `json:"next_cursor,omitempty"`
}

type TaskSummary struct {
	ID           string              `json:"id"`
	WorkflowID   string              `json:"workflow_id"`
	Kind         contracts.AdminTaskKind   `json:"kind"`
	Operation    *OperationReference `json:"worker,omitempty"`
	ParentTaskID string              `json:"parent_task_id,omitempty"`
	DependsOn    []string            `json:"depends_on"`
	EmissionKey  string              `json:"emission_key,omitempty"`
	State        string              `json:"state"`
	Attempts     int                 `json:"attempts"`
	MaxAttempts  int                 `json:"max_attempts"`
	AvailableAt  time.Time           `json:"available_at"`
	LeaseUntil   *time.Time          `json:"lease_until,omitempty"`
	ErrorCode    string              `json:"error_code,omitempty"`
	CreatedAt    time.Time           `json:"created_at"`
	UpdatedAt    time.Time           `json:"updated_at"`
}

type OperationReference struct {
	ComponentID     string `json:"component_id"`
	OperationID     string `json:"operation_id"`
	ContractVersion int    `json:"contract_version"`
}

type TaskPage struct {
	Items      []TaskSummary `json:"items"`
	NextCursor string        `json:"next_cursor,omitempty"`
}

type WorkflowView struct {
	ID            string    `json:"id"`
	OwnerEngineID string    `json:"owner_engine_id"`
	State         string    `json:"state"`
	RootTaskID    string    `json:"root_task_id"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type WorkflowDetailResponse struct {
	Workflow WorkflowView `json:"workflow"`
	Tasks    TaskPage     `json:"tasks"`
}

type PermissionsResponse struct {
	CanRetryTasks bool `json:"can_retry_tasks"`
}

type errorResponse struct {
	Error string `json:"error"`
}

func NewHandler(control WorkflowControl, authorize AuthorizeFunc, config Config) (*Handler, error) {
	if nilInterface(control) || authorize == nil || config.DefaultPageSize < 1 || config.MaxPageSize < config.DefaultPageSize || config.MaxPageSize > maxPageLimit {
		return nil, contracts.ErrWorkflowInputInvalid
	}
	var transport *adminhttpauth.Transport
	if len(config.AllowedOrigins) > 0 {
		var err error
		transport, err = adminhttpauth.New(config.AllowedOrigins)
		if err != nil {
			return nil, contracts.ErrWorkflowInputInvalid
		}
	}
	if config.SessionAuthorize != nil && transport == nil {
		return nil, contracts.ErrWorkflowInputInvalid
	}
	handler := &Handler{control: control, authorize: authorize, sessionAuthorize: config.SessionAuthorize,
		transport: transport, cfg: config, mux: http.NewServeMux()}
	handler.mux.HandleFunc("GET "+apiPrefix, handler.list)
	handler.mux.HandleFunc("GET "+apiPrefix+"/permissions", handler.permissions)
	handler.mux.HandleFunc("GET "+apiPrefix+"/{workflowID}", handler.get)
	handler.mux.HandleFunc("POST "+apiPrefix+"/{workflowID}/tasks/{taskID}/retry", handler.retryTask)
	return handler, nil
}

func (h *Handler) permissions(w http.ResponseWriter, r *http.Request) {
	if !h.authorizeRequest(w, r, ReadWorkflows) {
		return
	}
	err := h.authorizationError(r, RetryTasks)
	if errors.Is(err, contracts.ErrAccessForbidden) {
		writeJSON(w, http.StatusOK, PermissionsResponse{CanRetryTasks: false})
		return
	}
	if err != nil {
		writeAuthorizationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, PermissionsResponse{CanRetryTasks: true})
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	h.mux.ServeHTTP(w, r)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	if !h.authorizeRequest(w, r, ReadWorkflows) {
		return
	}
	query := r.URL.Query()
	for key := range query {
		if key != "state" && key != "limit" && key != "before" {
			writeError(w, http.StatusBadRequest, "invalid_query")
			return
		}
	}
	state, valid := singleQueryValue(query, "state")
	if !valid {
		writeError(w, http.StatusBadRequest, "invalid_query")
		return
	}
	limit, valid := h.pageLimit(query)
	if !valid {
		writeError(w, http.StatusBadRequest, "invalid_query")
		return
	}
	beforeValue, valid := singleQueryValue(query, "before")
	if !valid {
		writeError(w, http.StatusBadRequest, "invalid_query")
		return
	}
	var before *contracts.AdminWorkflowCursor
	if beforeValue != "" {
		cursor, err := decodeWorkflowCursor(beforeValue)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_cursor")
			return
		}
		before = cursor
	}
	page, err := h.control.List(r.Context(), contracts.AdminWorkflowListFilter{State: state, Limit: limit, Before: before})
	if err != nil {
		writeOperationError(w, err)
		return
	}
	response := WorkflowListResponse{Items: make([]WorkflowSummary, 0, len(page.Items))}
	for _, item := range page.Items {
		response.Items = append(response.Items, WorkflowSummary{ID: item.ID.String(), OwnerEngineID: item.OwnerClassID,
			State: item.State, TaskCount: item.TaskCount, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt})
	}
	if page.Next != nil {
		response.NextCursor = encodeCursor(page.Next.CreatedAt, page.Next.ID)
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	if !h.authorizeRequest(w, r, ReadWorkflows) {
		return
	}
	workflowID, err := parsePathID(r.PathValue("workflowID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_workflow_id")
		return
	}
	query := r.URL.Query()
	for key := range query {
		if key != "limit" && key != "before" {
			writeError(w, http.StatusBadRequest, "invalid_query")
			return
		}
	}
	limit, valid := h.pageLimit(query)
	if !valid {
		writeError(w, http.StatusBadRequest, "invalid_query")
		return
	}
	beforeValue, valid := singleQueryValue(query, "before")
	if !valid {
		writeError(w, http.StatusBadRequest, "invalid_query")
		return
	}
	var before *contracts.AdminTaskCursor
	if beforeValue != "" {
		before, err = decodeTaskCursor(beforeValue)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_cursor")
			return
		}
	}
	detail, err := h.control.Get(r.Context(), workflowID, contracts.AdminWorkflowDetailFilter{Limit: limit, Before: before})
	if err != nil {
		writeOperationError(w, err)
		return
	}
	response := WorkflowDetailResponse{Workflow: WorkflowView{ID: detail.Workflow.ID.String(),
		OwnerEngineID: detail.Workflow.OwnerClassID, State: detail.Workflow.State,
		RootTaskID: detail.Workflow.RootTaskID.String(), CreatedAt: detail.Workflow.CreatedAt, UpdatedAt: detail.Workflow.UpdatedAt},
		Tasks: TaskPage{Items: make([]TaskSummary, 0, len(detail.TaskPage.Items))}}
	for _, item := range detail.TaskPage.Items {
		task := TaskSummary{ID: item.ID.String(), WorkflowID: item.WorkflowID.String(), Kind: externalTaskKind(item.Kind),
			DependsOn: make([]string, 0, len(item.DependsOn)), EmissionKey: item.EmissionKey,
			State: item.State, Attempts: item.Attempts, MaxAttempts: item.MaxAttempts, AvailableAt: item.AvailableAt,
			LeaseUntil: item.LeaseUntil, ErrorCode: item.ErrorCode, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt}
		if item.Operation != nil {
			task.Operation = &OperationReference{ComponentID: item.Operation.ClassID, OperationID: item.Operation.ID,
				ContractVersion: item.Operation.Version}
		}
		if item.ParentTaskID != uuid.Nil {
			task.ParentTaskID = item.ParentTaskID.String()
		}
		for _, dependency := range item.DependsOn {
			task.DependsOn = append(task.DependsOn, dependency.String())
		}
		response.Tasks.Items = append(response.Tasks.Items, task)
	}
	if detail.TaskPage.Next != nil {
		response.Tasks.NextCursor = encodeCursor(detail.TaskPage.Next.CreatedAt, detail.TaskPage.Next.ID)
	}
	writeJSON(w, http.StatusOK, response)
}

func externalTaskKind(kind contracts.AdminTaskKind) contracts.AdminTaskKind {
	if kind == contracts.AdminTaskKindOperation {
		return "worker"
	}
	return kind
}

func (h *Handler) retryTask(w http.ResponseWriter, r *http.Request) {
	if !h.authorizeRequest(w, r, RetryTasks) {
		return
	}
	if r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
		writeError(w, http.StatusBadRequest, "body_not_allowed")
		return
	}
	workflowID, err := parsePathID(r.PathValue("workflowID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_workflow_id")
		return
	}
	taskID, err := parsePathID(r.PathValue("taskID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_task_id")
		return
	}
	if err := h.control.RetryTask(r.Context(), workflowID, taskID); err != nil {
		writeOperationError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) pageLimit(query map[string][]string) (int, bool) {
	value, valid := singleQueryValue(query, "limit")
	if !valid {
		return 0, false
	}
	if value == "" {
		return h.cfg.DefaultPageSize, true
	}
	limit, err := strconv.Atoi(value)
	if err != nil || limit < 1 || limit > h.cfg.MaxPageSize {
		return 0, false
	}
	return limit, true
}

func (h *Handler) authorizeRequest(w http.ResponseWriter, r *http.Request, permission Permission) bool {
	if err := h.authorizationError(r, permission); err != nil {
		writeAuthorizationError(w, err)
		return false
	}
	return true
}

func (h *Handler) authorizationError(r *http.Request, permission Permission) error {
	credential, err := adminhttpauth.Resolve(r)
	if err != nil {
		return err
	}
	if credential.Session {
		if h.sessionAuthorize == nil || h.transport == nil {
			return errors.New("Coordinator session authorization unavailable")
		}
		if err := h.transport.ValidateSessionRequest(r); err != nil {
			return adminhttpauth.ErrForbidden
		}
		return invokeAuthorizer(h.sessionAuthorize, r.Context(), credential.Value, permission)
	}
	return invokeAuthorizer(h.authorize, r.Context(), credential.Value, permission)
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

func encodeCursor(createdAt time.Time, id uuid.UUID) string {
	payload, err := json.Marshal(Cursor{CreatedAt: createdAt.UTC().Format(time.RFC3339Nano), ID: id.String()})
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(payload)
}

func decodeWorkflowCursor(value string) (*contracts.AdminWorkflowCursor, error) {
	cursor, err := decodeCursor(value)
	if err != nil {
		return nil, err
	}
	return &contracts.AdminWorkflowCursor{CreatedAt: cursor.CreatedAt, ID: cursor.ID}, nil
}

func decodeTaskCursor(value string) (*contracts.AdminTaskCursor, error) {
	cursor, err := decodeCursor(value)
	if err != nil {
		return nil, err
	}
	return &contracts.AdminTaskCursor{CreatedAt: cursor.CreatedAt, ID: cursor.ID}, nil
}

func decodeCursor(value string) (decodedCursor, error) {
	if value == "" || len(value) > maxCursorBytes {
		return decodedCursor{}, contracts.ErrWorkflowInputInvalid
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || base64.RawURLEncoding.EncodeToString(decoded) != value {
		return decodedCursor{}, contracts.ErrWorkflowInputInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(decoded))
	decoder.DisallowUnknownFields()
	var cursor Cursor
	if err := decoder.Decode(&cursor); err != nil {
		return decodedCursor{}, contracts.ErrWorkflowInputInvalid
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return decodedCursor{}, contracts.ErrWorkflowInputInvalid
	}
	canonical, err := json.Marshal(cursor)
	if err != nil || !bytes.Equal(canonical, decoded) {
		return decodedCursor{}, contracts.ErrWorkflowInputInvalid
	}
	createdAt, err := time.Parse(time.RFC3339Nano, cursor.CreatedAt)
	if err != nil || createdAt.IsZero() || createdAt.UTC().Format(time.RFC3339Nano) != cursor.CreatedAt {
		return decodedCursor{}, contracts.ErrWorkflowInputInvalid
	}
	id, err := uuid.Parse(cursor.ID)
	if err != nil || id == uuid.Nil || id.String() != cursor.ID {
		return decodedCursor{}, contracts.ErrWorkflowInputInvalid
	}
	return decodedCursor{CreatedAt: createdAt.UTC(), ID: id}, nil
}

func parsePathID(value string) (uuid.UUID, error) {
	id, err := uuid.Parse(value)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, contracts.ErrWorkflowInputInvalid
	}
	return id, nil
}

func writeAuthorizationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, contracts.ErrAccessUnauthorized), errors.Is(err, adminhttpauth.ErrUnauthorized):
		writeError(w, http.StatusUnauthorized, "unauthorized")
	case errors.Is(err, contracts.ErrAccessForbidden), errors.Is(err, adminhttpauth.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden")
	case errors.Is(err, adminhttpauth.ErrAmbiguous):
		writeError(w, http.StatusBadRequest, "ambiguous_authentication")
	default:
		writeError(w, http.StatusServiceUnavailable, "authorization_unavailable")
	}
}

func writeOperationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, contracts.ErrWorkflowInputInvalid):
		writeError(w, http.StatusBadRequest, "invalid_request")
	case errors.Is(err, contracts.ErrWorkflowTaskMissing):
		writeError(w, http.StatusNotFound, "not_found")
	case errors.Is(err, contracts.ErrWorkflowConflict), errors.Is(err, contracts.ErrWorkflowStale):
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
