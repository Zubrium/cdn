// Package adminapi adapts Core identity administration to a bounded HTTP API.
package adminapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Zubrium/cdn/core/access"
	"github.com/Zubrium/cdn/core/platform"
	"github.com/google/uuid"
)

const apiPrefix = "/api/v2/admin"

type Config struct{ MaxJSONBytes int64 }

type IdentityAdminAPI interface {
	Authenticate(context.Context, string) (platform.Session, error)
	CreatePrincipal(context.Context, platform.Session, CreatePrincipalRequest) (PrincipalCreated, error)
	ListPrincipals(context.Context, platform.Session) ([]platform.Principal, error)
	SetPrincipalManagement(context.Context, platform.Session, string, bool) error
	DeactivatePrincipal(context.Context, platform.Session, string) error
	IssueCredential(context.Context, platform.Session, string, *time.Time) (string, string, error)
	ListCredentials(context.Context, platform.Session, string) ([]platform.Credential, error)
	RevokeCredential(context.Context, platform.Session, string) error
	SetGroupAccess(context.Context, platform.Session, string, string, access.Level) error
	GroupAccess(context.Context, platform.Session, string, string) (access.Level, error)
	ListAccessGroups(context.Context, platform.Session) ([]platform.Group, error)
}

type CreatePrincipalRequest struct {
	Name                    string                  `json:"name"`
	ExpiresAt               *time.Time              `json:"expires_at,omitempty"`
	CanManageAdministrators bool                    `json:"can_manage_administrators"`
	GroupAccess             map[string]access.Level `json:"group_access,omitempty"`
}

type PrincipalCreated struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Credential string `json:"credential"`
}

type issuedCredential struct {
	ID         string     `json:"id"`
	PrincipalID string   `json:"principal_id"`
	Credential string    `json:"credential"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
}

type Handler struct {
	api IdentityAdminAPI
	cfg Config
	mux *http.ServeMux
}

func NewHandler(api IdentityAdminAPI, cfg Config) (*Handler, error) {
	if api == nil || cfg.MaxJSONBytes <= 0 || cfg.MaxJSONBytes > 1<<20 {
		return nil, errors.New("invalid Admin API configuration")
	}
	h := &Handler{api: api, cfg: cfg, mux: http.NewServeMux()}
	h.mux.HandleFunc("GET "+apiPrefix+"/principals", h.listPrincipals)
	h.mux.HandleFunc("POST "+apiPrefix+"/principals", h.createPrincipal)
	h.mux.HandleFunc("DELETE "+apiPrefix+"/principals/{id}", h.deactivatePrincipal)
	h.mux.HandleFunc("PATCH "+apiPrefix+"/principals/{id}/management", h.setPrincipalManagement)
	h.mux.HandleFunc("GET "+apiPrefix+"/principals/{id}/credentials", h.listCredentials)
	h.mux.HandleFunc("POST "+apiPrefix+"/principals/{id}/credentials", h.issueCredential)
	h.mux.HandleFunc("GET "+apiPrefix+"/principals/{id}/groups/{group}", h.getGroupAccess)
	h.mux.HandleFunc("PUT "+apiPrefix+"/principals/{id}/groups/{group}", h.setGroupAccess)
	h.mux.HandleFunc("DELETE "+apiPrefix+"/credentials/{id}", h.revokeCredential)
	h.mux.HandleFunc("GET "+apiPrefix+"/access-groups", h.listAccessGroups)
	return h, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	h.mux.ServeHTTP(w, r)
}

func (h *Handler) listPrincipals(w http.ResponseWriter, r *http.Request) {
	session, ok := h.authenticate(w, r)
	if !ok { return }
	principals, err := h.api.ListPrincipals(r.Context(), session)
	if err != nil { writeError(w, err); return }
	writeJSON(w, http.StatusOK, principals)
}

func (h *Handler) createPrincipal(w http.ResponseWriter, r *http.Request) {
	session, ok := h.authenticate(w, r)
	if !ok { return }
	var input CreatePrincipalRequest
	if err := decodeJSON(w, r, h.cfg.MaxJSONBytes, &input); err != nil { writeDecodeError(w, err); return }
	if !validPrincipalName(input.Name) || !validExpiry(input.ExpiresAt) || !validGroupAccess(input.GroupAccess) {
		writeCodeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	created, err := h.api.CreatePrincipal(r.Context(), session, input)
	if err != nil { writeError(w, err); return }
	if !validID(created.ID) || created.Credential == "" {
		writeCodeError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	w.Header().Set("Location", apiPrefix+"/principals/"+created.ID)
	writeJSON(w, http.StatusCreated, created)
}

func (h *Handler) deactivatePrincipal(w http.ResponseWriter, r *http.Request) {
	session, id, ok := h.principalRequest(w, r)
	if !ok { return }
	if err := h.api.DeactivatePrincipal(r.Context(), session, id); err != nil { writeError(w, err); return }
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) setPrincipalManagement(w http.ResponseWriter, r *http.Request) {
	session, id, ok := h.principalRequest(w, r)
	if !ok { return }
	var input struct { Enabled *bool `json:"enabled"` }
	if err := decodeJSON(w, r, h.cfg.MaxJSONBytes, &input); err != nil { writeDecodeError(w, err); return }
	if input.Enabled == nil { writeCodeError(w, http.StatusBadRequest, "invalid_request"); return }
	if err := h.api.SetPrincipalManagement(r.Context(), session, id, *input.Enabled); err != nil { writeError(w, err); return }
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) listCredentials(w http.ResponseWriter, r *http.Request) {
	session, id, ok := h.principalRequest(w, r)
	if !ok { return }
	items, err := h.api.ListCredentials(r.Context(), session, id)
	if err != nil { writeError(w, err); return }
	writeJSON(w, http.StatusOK, items)
}

func (h *Handler) issueCredential(w http.ResponseWriter, r *http.Request) {
	session, id, ok := h.principalRequest(w, r)
	if !ok { return }
	var input struct { ExpiresAt *time.Time `json:"expires_at,omitempty"` }
	if err := decodeJSON(w, r, h.cfg.MaxJSONBytes, &input); err != nil { writeDecodeError(w, err); return }
	if !validExpiry(input.ExpiresAt) { writeCodeError(w, http.StatusBadRequest, "invalid_request"); return }
	credentialID, secret, err := h.api.IssueCredential(r.Context(), session, id, input.ExpiresAt)
	if err != nil { writeError(w, err); return }
	if !validID(credentialID) || secret == "" { writeCodeError(w, http.StatusServiceUnavailable, "unavailable"); return }
	w.Header().Set("Location", apiPrefix+"/credentials/"+credentialID)
	writeJSON(w, http.StatusCreated, issuedCredential{ID: credentialID, PrincipalID: id, Credential: secret, ExpiresAt: input.ExpiresAt})
}

func (h *Handler) revokeCredential(w http.ResponseWriter, r *http.Request) {
	session, ok := h.authenticate(w, r)
	if !ok { return }
	id := r.PathValue("id")
	if !validID(id) { writeCodeError(w, http.StatusNotFound, "not_found"); return }
	if err := h.api.RevokeCredential(r.Context(), session, id); err != nil { writeError(w, err); return }
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) listAccessGroups(w http.ResponseWriter, r *http.Request) {
	session, ok := h.authenticate(w, r)
	if !ok { return }
	groups, err := h.api.ListAccessGroups(r.Context(), session)
	if err != nil { writeError(w, err); return }
	writeJSON(w, http.StatusOK, groups)
}

func (h *Handler) getGroupAccess(w http.ResponseWriter, r *http.Request) {
	session, id, ok := h.principalRequest(w, r)
	if !ok { return }
	group, ok := groupPath(w, r)
	if !ok { return }
	level, err := h.api.GroupAccess(r.Context(), session, id, group)
	if err != nil { writeError(w, err); return }
	writeJSON(w, http.StatusOK, map[string]any{"principal_id": id, "group": group, "level": level})
}

func (h *Handler) setGroupAccess(w http.ResponseWriter, r *http.Request) {
	session, id, ok := h.principalRequest(w, r)
	if !ok { return }
	group, ok := groupPath(w, r)
	if !ok { return }
	var input struct { Level *access.Level `json:"level"` }
	if err := decodeJSON(w, r, h.cfg.MaxJSONBytes, &input); err != nil { writeDecodeError(w, err); return }
	if input.Level == nil || *input.Level > access.LevelWrite { writeCodeError(w, http.StatusBadRequest, "invalid_request"); return }
	if err := h.api.SetGroupAccess(r.Context(), session, id, group, *input.Level); err != nil { writeError(w, err); return }
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) principalRequest(w http.ResponseWriter, r *http.Request) (platform.Session, string, bool) {
	session, ok := h.authenticate(w, r)
	if !ok { return platform.Session{}, "", false }
	id := r.PathValue("id")
	if !validID(id) { writeCodeError(w, http.StatusNotFound, "not_found"); return platform.Session{}, "", false }
	return session, id, true
}

func (h *Handler) authenticate(w http.ResponseWriter, r *http.Request) (platform.Session, bool) {
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") || strings.TrimSpace(strings.TrimPrefix(header, "Bearer ")) == "" {
		w.Header().Set("WWW-Authenticate", `Bearer realm="admin"`)
		writeCodeError(w, http.StatusUnauthorized, "unauthorized")
		return platform.Session{}, false
	}
	session, err := h.api.Authenticate(r.Context(), strings.TrimSpace(strings.TrimPrefix(header, "Bearer ")))
	if err != nil { writeError(w, err); return platform.Session{}, false }
	return session, true
}

func decodeJSON(w http.ResponseWriter, r *http.Request, limit int64, target any) error {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" { return errors.New("invalid content type") }
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) { return err }
		return errors.New("invalid JSON")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) { return errors.New("invalid JSON") }
	return nil
}

func writeDecodeError(w http.ResponseWriter, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) { writeCodeError(w, http.StatusRequestEntityTooLarge, "too_large"); return }
	writeCodeError(w, http.StatusBadRequest, "invalid_request")
}

func writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, platform.ErrUnauthorized):
		w.Header().Set("WWW-Authenticate", `Bearer realm="admin"`)
		writeCodeError(w, http.StatusUnauthorized, "unauthorized")
	case errors.Is(err, platform.ErrForbidden):
		writeCodeError(w, http.StatusForbidden, "forbidden")
	case errors.Is(err, platform.ErrManagerInvariant), errors.Is(err, platform.ErrModuleDataConflict), errors.Is(err, platform.ErrFileSetConflict):
		writeCodeError(w, http.StatusConflict, "conflict")
	default:
		writeCodeError(w, http.StatusServiceUnavailable, "unavailable")
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeCodeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"error": code})
}

func validPrincipalName(name string) bool {
	if name == "" || len(name) > 200 || !utf8.ValidString(name) || strings.TrimSpace(name) != name { return false }
	for _, r := range name { if unicode.IsControl(r) { return false } }
	return true
}

func validExpiry(expires *time.Time) bool { return expires == nil || expires.After(time.Now()) }

func validGroupAccess(groups map[string]access.Level) bool {
	if len(groups) > 128 { return false }
	for group, level := range groups {
		if !access.ValidGroupKey(group) || level > access.LevelWrite { return false }
	}
	return true
}

func groupPath(w http.ResponseWriter, r *http.Request) (string, bool) {
	group := r.PathValue("group")
	if !access.ValidGroupKey(group) { writeCodeError(w, http.StatusNotFound, "not_found"); return "", false }
	return group, true
}

func validID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil
}

var _ http.Handler = (*Handler)(nil)
