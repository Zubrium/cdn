// Package adminapi adapts Core identity administration to a bounded HTTP API.
package adminapi

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	platform "github.com/Zubrium/cdn/core/presentation"
	adminhttpauth "github.com/Zubrium/cdn/presentation/admin/httpauth"
	"github.com/Zubrium/cdn/presentation/httpjson"
	"github.com/google/uuid"
)

const (
	apiPrefix              = "/api/v2/admin"
	adminSessionCookieName = adminhttpauth.SessionCookieName
	adminCSRFCookieName    = adminhttpauth.CSRFCookieName
)

type Config struct {
	MaxJSONBytes        int64
	SessionCookieSecure bool
	AllowedOrigins      []string
	GroupAccess         SessionGroupAccessResolver
	Notifications       *NotificationHub
}

type IdentityAdminAPI interface {
	Authenticate(context.Context, string) (platform.Session, error)
	CreatePrincipal(context.Context, platform.Session, CreatePrincipalRequest) (PrincipalCreated, error)
	ListPrincipals(context.Context, platform.Session) ([]platform.Principal, error)
	SetPrincipalManagement(context.Context, platform.Session, string, bool) error
	DeactivatePrincipal(context.Context, platform.Session, string) error
	IssueCredential(context.Context, platform.Session, string, *time.Time) (string, string, error)
	ListCredentials(context.Context, platform.Session, string) ([]platform.Credential, error)
	RevokeCredential(context.Context, platform.Session, string) error
	SetGroupAccess(context.Context, platform.Session, string, string, platform.AccessLevel) error
	GroupAccess(context.Context, platform.Session, string, string) (platform.AccessLevel, error)
	ListAccessGroups(context.Context, platform.Session) ([]platform.Group, error)
}

type SessionAdminAPI interface {
	CreateSession(context.Context, string) (platform.CreatedSession, error)
	AuthenticateSession(context.Context, string) (platform.Session, platform.Principal, error)
	RevokeSession(context.Context, string) error
}

// SessionGroupAccessResolver exposes the current principal's direct group
// levels through already-issued Core authorization capabilities. It does not
// mutate Core and never trusts a principal or group list from the request.
type SessionGroupAccessResolver interface {
	ResolveGroupAccess(context.Context, string) (map[string]platform.AccessLevel, error)
}

type CreatePrincipalRequest struct {
	Name                    string                          `json:"name"`
	ExpiresAt               *time.Time                      `json:"expires_at,omitempty"`
	CanManageAdministrators bool                            `json:"can_manage_administrators"`
	GroupAccess             map[string]platform.AccessLevel `json:"group_access,omitempty"`
}

type PrincipalCreated struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Credential string `json:"credential"`
}

type issuedCredential struct {
	ID          string     `json:"id"`
	PrincipalID string     `json:"principal_id"`
	Credential  string     `json:"credential"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
}

type credentialResponse struct {
	ID          string     `json:"id"`
	PrincipalID string     `json:"principal_id"`
	CreatedAt   time.Time  `json:"created_at"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty"`
}

type accessGroupResponse struct {
	Key    string               `json:"key"`
	Active bool                 `json:"active"`
	Level  platform.AccessLevel `json:"level"`
}

type Handler struct {
	api         IdentityAdminAPI
	sessions    SessionAdminAPI
	groupAccess SessionGroupAccessResolver
	cfg         Config
	transport   *adminhttpauth.Transport
	mux         *http.ServeMux
}

func NewHandler(api IdentityAdminAPI, cfg Config) (*Handler, error) {
	return newHandler(api, nil, cfg)
}

func NewHandlerWithSessions(api IdentityAdminAPI, sessions SessionAdminAPI, cfg Config) (*Handler, error) {
	if sessions == nil {
		return nil, errors.New("Core session capability is required")
	}
	return newHandler(api, sessions, cfg)
}

func newHandler(api IdentityAdminAPI, sessions SessionAdminAPI, cfg Config) (*Handler, error) {
	if api == nil || cfg.MaxJSONBytes <= 0 || cfg.MaxJSONBytes > 1<<20 {
		return nil, errors.New("invalid Admin API configuration")
	}
	var transport *adminhttpauth.Transport
	if len(cfg.AllowedOrigins) > 0 {
		var err error
		transport, err = adminhttpauth.New(cfg.AllowedOrigins)
		if err != nil {
			return nil, err
		}
	}
	if sessions != nil && transport == nil {
		return nil, errors.New("Admin API allowed_origins must be configured for cookie sessions")
	}
	if cfg.Notifications != nil && sessions == nil {
		return nil, errors.New("Admin notifications require Core session capability")
	}
	h := &Handler{api: api, sessions: sessions, groupAccess: cfg.GroupAccess, cfg: cfg, transport: transport, mux: http.NewServeMux()}
	if sessions != nil {
		h.mux.HandleFunc("POST "+apiPrefix+"/session", h.createSession)
		h.mux.HandleFunc("GET "+apiPrefix+"/session", h.currentSession)
		h.mux.HandleFunc("DELETE "+apiPrefix+"/session", h.deleteSession)
	}
	if cfg.Notifications != nil {
		h.mux.HandleFunc("HEAD "+apiPrefix+"/notifications", h.probeNotifications)
		h.mux.HandleFunc("GET "+apiPrefix+"/notifications", h.streamNotifications)
	}
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
	w.Header().Set("X-Content-Type-Options", "nosniff")
	h.mux.ServeHTTP(w, r)
}

type principalResponse struct {
	ID                      string                          `json:"id"`
	Name                    string                          `json:"name"`
	Active                  bool                            `json:"active"`
	CanManageAdministrators bool                            `json:"can_manage_administrators"`
	GroupAccess             map[string]platform.AccessLevel `json:"group_access"`
}

func (h *Handler) resolvePrincipal(ctx context.Context, token string, value platform.Principal) (principalResponse, error) {
	response := publicPrincipal(value)
	if h.groupAccess != nil {
		resolved, err := h.groupAccess.ResolveGroupAccess(ctx, token)
		if err != nil {
			return principalResponse{}, err
		}
		response.GroupAccess = resolved
	}
	for group, level := range response.GroupAccess {
		if !platform.ValidAccessGroupKey(group) || level > platform.AccessLevelWrite {
			return principalResponse{}, errors.New("invalid group access resolver result")
		}
	}
	if response.GroupAccess == nil {
		response.GroupAccess = make(map[string]platform.AccessLevel)
	}
	return response, nil
}

func publicPrincipal(value platform.Principal) principalResponse {
	return principalResponse{ID: value.ID, Name: value.Name, Active: value.Active,
		CanManageAdministrators: value.CanManageAdministrators, GroupAccess: map[string]platform.AccessLevel{}}
}

func (h *Handler) createSession(w http.ResponseWriter, r *http.Request) {
	if !h.trustedOrigin(r) {
		writeCodeError(w, http.StatusForbidden, "origin_forbidden")
		return
	}
	var input struct {
		Credential string `json:"credential"`
	}
	if err := decodeJSON(w, r, h.cfg.MaxJSONBytes, &input); err != nil {
		writeDecodeError(w, err)
		return
	}
	if strings.TrimSpace(input.Credential) != input.Credential || input.Credential == "" {
		writeCodeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	created, err := h.sessions.CreateSession(r.Context(), input.Credential)
	if err != nil {
		writeError(w, err)
		return
	}
	now := time.Now()
	if !validSessionToken(created.Token) || created.Principal.ID == "" || !created.Principal.Active ||
		!created.IdleExpiresAt.After(now) || !created.AbsoluteExpiresAt.After(now) || created.IdleExpiresAt.After(created.AbsoluteExpiresAt) {
		if created.Token != "" {
			_ = h.sessions.RevokeSession(r.Context(), created.Token)
		}
		writeCodeError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	csrf, err := newCSRFToken()
	if err != nil {
		_ = h.sessions.RevokeSession(r.Context(), created.Token)
		writeCodeError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	principal, err := h.resolvePrincipal(r.Context(), created.Token, created.Principal)
	if err != nil {
		_ = h.sessions.RevokeSession(r.Context(), created.Token)
		writeCodeError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	setSessionCookies(w, created.Token, csrf, created.AbsoluteExpiresAt, h.cfg.SessionCookieSecure)
	writeJSON(w, http.StatusCreated, map[string]any{"principal": principal})
}

func (h *Handler) currentSession(w http.ResponseWriter, r *http.Request) {
	token, present, valid := sessionTokenCookie(r)
	if !valid {
		writeCodeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if present && len(r.Header.Values("Authorization")) != 0 {
		writeCodeError(w, http.StatusBadRequest, "ambiguous_authentication")
		return
	}
	_, principal, err := h.sessions.AuthenticateSession(r.Context(), token)
	if err != nil {
		writeError(w, err)
		return
	}
	response, err := h.resolvePrincipal(r.Context(), token, principal)
	if err != nil {
		writeCodeError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"principal": response})
}

func (h *Handler) deleteSession(w http.ResponseWriter, r *http.Request) {
	if !h.trustedOrigin(r) {
		writeCodeError(w, http.StatusForbidden, "origin_forbidden")
		return
	}
	token, present, valid := sessionTokenCookie(r)
	if !present {
		clearSessionCookies(w, h.cfg.SessionCookieSecure)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if !valid || len(r.Header.Values("Authorization")) != 0 || !validCSRFProof(r) {
		writeCodeError(w, http.StatusForbidden, "csrf_forbidden")
		return
	}
	if err := h.sessions.RevokeSession(r.Context(), token); err != nil {
		writeError(w, err)
		return
	}
	clearSessionCookies(w, h.cfg.SessionCookieSecure)
	w.WriteHeader(http.StatusNoContent)
}

func sessionTokenCookie(r *http.Request) (string, bool, bool) {
	return adminhttpauth.SessionToken(r)
}

func setSessionCookies(w http.ResponseWriter, token, csrf string, expires time.Time, secure bool) {
	now := time.Now()
	maxAge := int(expires.Sub(now) / time.Second)
	if maxAge < 1 {
		maxAge = 1
	}
	w.Header().Add("Set-Cookie", (&http.Cookie{Name: adminSessionCookieName, Value: token, Path: "/api/v2", Expires: expires,
		MaxAge: maxAge, HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode}).String())
	w.Header().Add("Set-Cookie", (&http.Cookie{Name: adminCSRFCookieName, Value: csrf, Path: "/", Expires: expires,
		MaxAge: maxAge, HttpOnly: false, Secure: secure, SameSite: http.SameSiteStrictMode}).String())
}

func clearSessionCookies(w http.ResponseWriter, secure bool) {
	w.Header().Add("Set-Cookie", (&http.Cookie{Name: adminSessionCookieName, Value: "", Path: "/api/v2", MaxAge: -1,
		HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode}).String())
	w.Header().Add("Set-Cookie", (&http.Cookie{Name: adminCSRFCookieName, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: false, Secure: secure, SameSite: http.SameSiteStrictMode}).String())
}

func newCSRFToken() (string, error) {
	var token [32]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(token[:]), nil
}

func validCSRFProof(r *http.Request) bool {
	return adminhttpauth.ValidCSRFProof(r)
}

func validSessionToken(value string) bool {
	return adminhttpauth.ValidSessionToken(value)
}

func validCSRFToken(value string) bool {
	return adminhttpauth.ValidCSRFToken(value)
}

func (h *Handler) trustedOrigin(r *http.Request) bool {
	return h.transport != nil && h.transport.TrustedOrigin(r)
}

func (h *Handler) listPrincipals(w http.ResponseWriter, r *http.Request) {
	session, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	principals, err := h.api.ListPrincipals(r.Context(), session)
	if err != nil {
		writeError(w, err)
		return
	}
	response := make([]principalResponse, 0, len(principals))
	for _, principal := range principals {
		response = append(response, publicPrincipal(principal))
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) createPrincipal(w http.ResponseWriter, r *http.Request) {
	session, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	var input CreatePrincipalRequest
	if err := decodeJSON(w, r, h.cfg.MaxJSONBytes, &input); err != nil {
		writeDecodeError(w, err)
		return
	}
	if !validPrincipalName(input.Name) || !validExpiry(input.ExpiresAt) || !validGroupAccess(input.GroupAccess) {
		writeCodeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	created, err := h.api.CreatePrincipal(r.Context(), session, input)
	if err != nil {
		writeError(w, err)
		return
	}
	if !validID(created.ID) || created.Credential == "" {
		writeCodeError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	w.Header().Set("Location", apiPrefix+"/principals/"+created.ID)
	writeJSON(w, http.StatusCreated, created)
}

func (h *Handler) deactivatePrincipal(w http.ResponseWriter, r *http.Request) {
	session, id, ok := h.principalRequest(w, r)
	if !ok {
		return
	}
	if err := h.api.DeactivatePrincipal(r.Context(), session, id); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) setPrincipalManagement(w http.ResponseWriter, r *http.Request) {
	session, id, ok := h.principalRequest(w, r)
	if !ok {
		return
	}
	var input struct {
		Enabled *bool `json:"enabled"`
	}
	if err := decodeJSON(w, r, h.cfg.MaxJSONBytes, &input); err != nil {
		writeDecodeError(w, err)
		return
	}
	if input.Enabled == nil {
		writeCodeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if err := h.api.SetPrincipalManagement(r.Context(), session, id, *input.Enabled); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) listCredentials(w http.ResponseWriter, r *http.Request) {
	session, id, ok := h.principalRequest(w, r)
	if !ok {
		return
	}
	items, err := h.api.ListCredentials(r.Context(), session, id)
	if err != nil {
		writeError(w, err)
		return
	}
	response := make([]credentialResponse, 0, len(items))
	for _, item := range items {
		response = append(response, credentialResponse{ID: item.ID, PrincipalID: item.PrincipalID,
			CreatedAt: item.CreatedAt, ExpiresAt: item.ExpiresAt, RevokedAt: item.RevokedAt})
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) issueCredential(w http.ResponseWriter, r *http.Request) {
	session, id, ok := h.principalRequest(w, r)
	if !ok {
		return
	}
	var input struct {
		ExpiresAt *time.Time `json:"expires_at,omitempty"`
	}
	if err := decodeJSON(w, r, h.cfg.MaxJSONBytes, &input); err != nil {
		writeDecodeError(w, err)
		return
	}
	if !validExpiry(input.ExpiresAt) {
		writeCodeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	credentialID, secret, err := h.api.IssueCredential(r.Context(), session, id, input.ExpiresAt)
	if err != nil {
		writeError(w, err)
		return
	}
	if !validID(credentialID) || secret == "" {
		writeCodeError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	w.Header().Set("Location", apiPrefix+"/credentials/"+credentialID)
	writeJSON(w, http.StatusCreated, issuedCredential{ID: credentialID, PrincipalID: id, Credential: secret, ExpiresAt: input.ExpiresAt})
}

func (h *Handler) revokeCredential(w http.ResponseWriter, r *http.Request) {
	session, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if !validID(id) {
		writeCodeError(w, http.StatusNotFound, "not_found")
		return
	}
	if err := h.api.RevokeCredential(r.Context(), session, id); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) listAccessGroups(w http.ResponseWriter, r *http.Request) {
	session, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	groups, err := h.api.ListAccessGroups(r.Context(), session)
	if err != nil {
		writeError(w, err)
		return
	}
	response := make([]accessGroupResponse, 0, len(groups))
	for _, group := range groups {
		response = append(response, accessGroupResponse{Key: group.Key, Active: group.Active, Level: group.Level})
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) getGroupAccess(w http.ResponseWriter, r *http.Request) {
	session, id, ok := h.principalRequest(w, r)
	if !ok {
		return
	}
	group, ok := groupPath(w, r)
	if !ok {
		return
	}
	level, err := h.api.GroupAccess(r.Context(), session, id, group)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"principal_id": id, "group": group, "level": level})
}

func (h *Handler) setGroupAccess(w http.ResponseWriter, r *http.Request) {
	session, id, ok := h.principalRequest(w, r)
	if !ok {
		return
	}
	group, ok := groupPath(w, r)
	if !ok {
		return
	}
	var input struct {
		Level *platform.AccessLevel `json:"level"`
	}
	if err := decodeJSON(w, r, h.cfg.MaxJSONBytes, &input); err != nil {
		writeDecodeError(w, err)
		return
	}
	if input.Level == nil || *input.Level > platform.AccessLevelWrite {
		writeCodeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if err := h.api.SetGroupAccess(r.Context(), session, id, group, *input.Level); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) principalRequest(w http.ResponseWriter, r *http.Request) (platform.Session, string, bool) {
	session, ok := h.authenticate(w, r)
	if !ok {
		return platform.Session{}, "", false
	}
	id := r.PathValue("id")
	if !validID(id) {
		writeCodeError(w, http.StatusNotFound, "not_found")
		return platform.Session{}, "", false
	}
	return session, id, true
}

func (h *Handler) authenticate(w http.ResponseWriter, r *http.Request) (platform.Session, bool) {
	credential, err := adminhttpauth.Resolve(r)
	if err != nil {
		if errors.Is(err, adminhttpauth.ErrAmbiguous) {
			writeCodeError(w, http.StatusBadRequest, "ambiguous_authentication")
		} else {
			writeCodeError(w, http.StatusUnauthorized, "unauthorized")
		}
		return platform.Session{}, false
	}
	if credential.Session {
		if h.sessions == nil {
			writeCodeError(w, http.StatusUnauthorized, "unauthorized")
			return platform.Session{}, false
		}
		if isStateChanging(r.Method) {
			if !h.trustedOrigin(r) {
				writeCodeError(w, http.StatusForbidden, "origin_forbidden")
				return platform.Session{}, false
			}
			if !validCSRFProof(r) {
				writeCodeError(w, http.StatusForbidden, "csrf_forbidden")
				return platform.Session{}, false
			}
		}
		session, _, err := h.sessions.AuthenticateSession(r.Context(), credential.Value)
		if err != nil {
			writeError(w, err)
			return platform.Session{}, false
		}
		return session, true
	}
	session, err := h.api.Authenticate(r.Context(), credential.Value)
	if err != nil {
		writeError(w, err)
		return platform.Session{}, false
	}
	return session, true
}

func isStateChanging(method string) bool {
	return method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions
}

func decodeJSON(w http.ResponseWriter, r *http.Request, limit int64, target any) error {
	return httpjson.DecodeRequest(w, r, limit, target)
}

func writeDecodeError(w http.ResponseWriter, err error) {
	if errors.Is(err, httpjson.ErrTooLarge) {
		writeCodeError(w, http.StatusRequestEntityTooLarge, "too_large")
		return
	}
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
	if name == "" || len(name) > 200 || !utf8.ValidString(name) || strings.TrimSpace(name) != name {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func validExpiry(expires *time.Time) bool { return expires == nil || expires.After(time.Now()) }

func validGroupAccess(groups map[string]platform.AccessLevel) bool {
	if len(groups) > 128 {
		return false
	}
	for group, level := range groups {
		if !platform.ValidAccessGroupKey(group) || level > platform.AccessLevelWrite {
			return false
		}
	}
	return true
}

func groupPath(w http.ResponseWriter, r *http.Request) (string, bool) {
	group := r.PathValue("group")
	if !platform.ValidAccessGroupKey(group) {
		writeCodeError(w, http.StatusNotFound, "not_found")
		return "", false
	}
	return group, true
}

func validID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil
}

var _ http.Handler = (*Handler)(nil)
