// Package notification owns the Admin notification hub and its SSE endpoint.
// It is a separate Presentation module from Admin identity and must not
// register or serve any other Admin API paths.
package notification

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/Zubrium/cdn/pkg/contracts"
	adminhttpauth "github.com/Zubrium/cdn/presentation/httpauth"
	adminapi "github.com/Zubrium/cdn/presentation/admin/base/admin/main"
	"github.com/google/uuid"
)

const (
	NotificationSnapshotStateChanged       = "snapshot.state_changed"
	NotificationFeatureImportStateChanged  = "feature_import.state_changed"
	NotificationMediaRenditionStateChanged = "media_rendition.state_changed"
	notificationsPath                      = "/api/admin/notifications"
	notificationQueueSize                  = 16
	notificationHeartbeat                  = 10 * time.Second
	notificationWriteTimeout               = 30 * time.Second
)

type NotificationEvent struct {
	Type          string `json:"type"`
	SnapshotID    string `json:"snapshot_id,omitempty"`
	ImportID      string `json:"import_id,omitempty"`
	MediaID       string `json:"media_id,omitempty"`
	ProfileDigest string `json:"profile_digest,omitempty"`
	State         string `json:"state"`
	ErrorCode     string `json:"error_code,omitempty"`
}

type notificationSubscriber struct {
	principalID string
	events      chan NotificationEvent
}

type Hub struct {
	mu          sync.Mutex
	subscribers map[*notificationSubscriber]struct{}
}

func NewHub() *Hub {
	return &Hub{subscribers: make(map[*notificationSubscriber]struct{})}
}

func (h *Hub) Publish(principalID string, event NotificationEvent) error {
	if h == nil || !validNotification(principalID, event) {
		return errors.New("invalid Admin notification")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for subscriber := range h.subscribers {
		if subscriber.principalID != principalID {
			continue
		}
		select {
		case subscriber.events <- event:
		default:
			delete(h.subscribers, subscriber)
			close(subscriber.events)
		}
	}
	return nil
}

func (h *Hub) subscribe(principalID string) (<-chan NotificationEvent, func()) {
	subscriber := &notificationSubscriber{principalID: principalID, events: make(chan NotificationEvent, notificationQueueSize)}
	h.mu.Lock()
	h.subscribers[subscriber] = struct{}{}
	h.mu.Unlock()
	return subscriber.events, func() {
		h.mu.Lock()
		if _, ok := h.subscribers[subscriber]; ok {
			delete(h.subscribers, subscriber)
			close(subscriber.events)
		}
		h.mu.Unlock()
	}
}

func validNotification(principalID string, event NotificationEvent) bool {
	if id, err := uuid.Parse(principalID); err != nil || id == uuid.Nil || len(event.State) == 0 || len(event.State) > 64 {
		return false
	}
	for _, character := range event.State {
		if !(character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '_' || character == '-') {
			return false
		}
	}
	if len(event.ErrorCode) > 64 {
		return false
	}
	switch event.Type {
	case NotificationSnapshotStateChanged:
		return validNotificationID(event.SnapshotID) && event.ImportID == "" && event.MediaID == "" && event.ProfileDigest == ""
	case NotificationFeatureImportStateChanged:
		return validNotificationID(event.ImportID) && event.SnapshotID == "" && event.MediaID == "" && event.ProfileDigest == ""
	case NotificationMediaRenditionStateChanged:
		return validNotificationID(event.MediaID) && validProfileDigest(event.ProfileDigest) && event.SnapshotID == "" && event.ImportID == ""
	default:
		return false
	}
}

func validNotificationID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil
}

func validProfileDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if !(character >= '0' && character <= '9' || character >= 'a' && character <= 'f') {
			return false
		}
	}
	return true
}

type Handler struct {
	hub       *Hub
	sessions  adminapi.SessionAdminAPI
	identity  adminapi.IdentityAdminAPI
	transport *adminhttpauth.Transport
	mux       *http.ServeMux
}

func NewHandler(hub *Hub, sessions adminapi.SessionAdminAPI, identity adminapi.IdentityAdminAPI, allowedOrigins []string) (*Handler, error) {
	if hub == nil || sessions == nil || identity == nil {
		return nil, errors.New("Admin notification dependencies are incomplete")
	}
	var transport *adminhttpauth.Transport
	if len(allowedOrigins) > 0 {
		var err error
		transport, err = adminhttpauth.New(allowedOrigins)
		if err != nil {
			return nil, err
		}
	}
	h := &Handler{hub: hub, sessions: sessions, identity: identity, transport: transport, mux: http.NewServeMux()}
	h.mux.HandleFunc("HEAD "+notificationsPath, h.probeNotifications)
	h.mux.HandleFunc("GET "+notificationsPath, h.streamNotifications)
	return h, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	h.mux.ServeHTTP(w, r)
}

func (h *Handler) authenticate(w http.ResponseWriter, r *http.Request) bool {
	credential, err := adminhttpauth.Resolve(r)
	if err != nil {
		if errors.Is(err, adminhttpauth.ErrAmbiguous) {
			writeCodeError(w, http.StatusBadRequest, "ambiguous_authentication")
		} else {
			writeCodeError(w, http.StatusUnauthorized, "unauthorized")
		}
		return false
	}
	if credential.Session {
		if _, _, err := h.sessions.AuthenticateSession(r.Context(), credential.Value); err != nil {
			writeError(w, err)
			return false
		}
		return true
	}
	if _, err := h.identity.Authenticate(r.Context(), credential.Value); err != nil {
		writeError(w, err)
		return false
	}
	return true
}

func (h *Handler) notificationPrincipal(w http.ResponseWriter, r *http.Request) (contracts.Principal, func(), bool) {
	credential, err := adminhttpauth.Resolve(r)
	if err != nil {
		if errors.Is(err, adminhttpauth.ErrAmbiguous) {
			writeCodeError(w, http.StatusBadRequest, "ambiguous_authentication")
		} else {
			writeCodeError(w, http.StatusUnauthorized, "unauthorized")
		}
		return contracts.Principal{}, nil, false
	}
	if credential.Session {
		if !h.trustedReadOrigin(r) {
			writeCodeError(w, http.StatusForbidden, "origin_forbidden")
			return contracts.Principal{}, nil, false
		}
		_, principal, err := h.sessions.AuthenticateSession(r.Context(), credential.Value)
		if err != nil {
			writeError(w, err)
			return contracts.Principal{}, nil, false
		}
		return principal, func() {}, true
	}
	created, err := h.sessions.CreateSession(r.Context(), credential.Value)
	if err != nil {
		writeError(w, err)
		return contracts.Principal{}, nil, false
	}
	return created.Principal, func() { _ = h.sessions.RevokeSession(context.Background(), created.Token) }, true
}

func (h *Handler) trustedReadOrigin(r *http.Request) bool {
	return h.transport != nil && h.transport.TrustedReadOrigin(r)
}

func (h *Handler) probeNotifications(w http.ResponseWriter, r *http.Request) {
	if h.authenticate(w, r) {
		w.WriteHeader(http.StatusNoContent)
	}
}

func (h *Handler) streamNotifications(w http.ResponseWriter, r *http.Request) {
	principal, release, ok := h.notificationPrincipal(w, r)
	if !ok {
		return
	}
	defer release()
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeCodeError(w, http.StatusInternalServerError, "stream_unavailable")
		return
	}
	if err := setNotificationWriteDeadline(w); err != nil && !errors.Is(err, http.ErrNotSupported) {
		writeCodeError(w, http.StatusInternalServerError, "stream_unavailable")
		return
	}
	events, unsubscribe := h.hub.subscribe(principal.ID)
	defer unsubscribe()
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ticker := time.NewTicker(notificationHeartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case event, open := <-events:
			if !open {
				return
			}
			if err := writeNotification(w, flusher, event); err != nil {
				return
			}
		case <-ticker.C:
			if err := writeSSE(w, flusher, ": heartbeat\n\n"); err != nil {
				return
			}
		}
	}
}

func writeNotification(w http.ResponseWriter, flusher http.Flusher, event NotificationEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	return writeSSE(w, flusher, fmt.Sprintf("event: %s\ndata: %s\n\n", event.Type, data))
}

func writeSSE(w http.ResponseWriter, flusher http.Flusher, value string) error {
	if err := setNotificationWriteDeadline(w); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return err
	}
	if _, err := io.WriteString(w, value); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

func setNotificationWriteDeadline(w http.ResponseWriter) error {
	return http.NewResponseController(w).SetWriteDeadline(time.Now().Add(notificationWriteTimeout))
}

func writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, contracts.ErrAccessUnauthorized):
		w.Header().Set("WWW-Authenticate", `Bearer realm="admin"`)
		writeCodeError(w, http.StatusUnauthorized, "unauthorized")
	case errors.Is(err, contracts.ErrAccessForbidden):
		writeCodeError(w, http.StatusForbidden, "forbidden")
	case errors.Is(err, contracts.ErrRecordConflict):
		writeCodeError(w, http.StatusConflict, "conflict")
	default:
		writeCodeError(w, http.StatusServiceUnavailable, "unavailable")
	}
}

func writeCodeError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
}
