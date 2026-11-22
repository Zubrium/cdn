package adminapi

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestNotificationCapabilityProbeIsConditionalAndAuthenticated(t *testing.T) {
	path := apiPrefix + "/notifications"
	sessions := &sessionStub{login: testSessionLogin()}
	handler, err := NewHandlerWithSessions(&fakeIdentityAdmin{}, sessions, Config{
		MaxJSONBytes: 1024, SessionCookieSecure: true, AllowedOrigins: []string{testAdminOrigin}, Notifications: NewNotificationHub(),
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodHead, path, nil)
	request.AddCookie(&http.Cookie{Name: adminSessionCookieName, Value: sessions.login.Token})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || sessions.resolveCalls != 1 {
		t.Fatalf("authenticated capability probe status=%d session resolves=%d", response.Code, sessions.resolveCalls)
	}

	disabled, err := NewHandler(&fakeIdentityAdmin{}, Config{MaxJSONBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	disabled.ServeHTTP(response, httptest.NewRequest(http.MethodHead, path, nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("disabled notification endpoint returned %d, want 404", response.Code)
	}
}

func TestNotificationHubRoutesEventsOnlyToInitiatingPrincipal(t *testing.T) {
	hub := NewNotificationHub()
	principalA, principalB := uuid.NewString(), uuid.NewString()
	eventsA, unsubscribeA := hub.subscribe(principalA)
	defer unsubscribeA()
	eventsB, unsubscribeB := hub.subscribe(principalB)
	defer unsubscribeB()

	event := NotificationEvent{Type: NotificationSnapshotStateChanged, SnapshotID: uuid.NewString(), State: "ready"}
	if err := hub.Publish(principalA, event); err != nil {
		t.Fatal(err)
	}
	if got := <-eventsA; got != event {
		t.Fatalf("initiator received %#v, want %#v", got, event)
	}
	select {
	case got := <-eventsB:
		t.Fatalf("event leaked to another principal: %#v", got)
	default:
	}
}

func TestNotificationHubDisconnectsSlowSubscriberOnQueueOverflow(t *testing.T) {
	hub := NewNotificationHub()
	principalID := uuid.NewString()
	events, unsubscribe := hub.subscribe(principalID)
	defer unsubscribe()

	for i := 0; i < notificationQueueSize; i++ {
		event := NotificationEvent{Type: NotificationSnapshotStateChanged, SnapshotID: uuid.NewString(), State: "ready"}
		if err := hub.Publish(principalID, event); err != nil {
			t.Fatal(err)
		}
	}
	if err := hub.Publish(principalID, NotificationEvent{Type: NotificationSnapshotStateChanged, SnapshotID: uuid.NewString(), State: "ready"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < notificationQueueSize; i++ {
		if _, open := <-events; !open {
			t.Fatalf("subscriber channel closed before buffered event %d was drained", i+1)
		}
	}
	if _, open := <-events; open {
		t.Fatal("slow subscriber channel remained open after buffered events were drained")
	}
}

func TestNotificationStreamDeliversEventsAndRejectsUntrustedOrigin(t *testing.T) {
	login := testSessionLogin()
	login.Principal.ID = uuid.NewString()
	sessions := &sessionStub{login: login}
	hub := NewNotificationHub()
	handler, err := NewHandlerWithSessions(&fakeIdentityAdmin{}, sessions, Config{
		MaxJSONBytes: 1024, SessionCookieSecure: true, AllowedOrigins: []string{testAdminOrigin}, Notifications: hub,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+apiPrefix+"/notifications", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.AddCookie(&http.Cookie{Name: adminSessionCookieName, Value: login.Token})
	request.Header.Set("Origin", testAdminOrigin)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("notification stream status=%d, want %d", response.StatusCode, http.StatusOK)
	}
	if got := response.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
		t.Fatalf("notification stream content type=%q", got)
	}
	if got := response.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("notification stream cache control=%q, want no-store", got)
	}
	if got := response.Header.Get("X-Accel-Buffering"); got != "no" {
		t.Fatalf("notification stream buffering header=%q, want no", got)
	}
	if got := response.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("notification stream nosniff header=%q, want nosniff", got)
	}

	event := NotificationEvent{Type: NotificationSnapshotStateChanged, SnapshotID: uuid.NewString(), State: "ready"}
	if err := hub.Publish(login.Principal.ID, event); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(response.Body)
	var received strings.Builder
	for !strings.Contains(received.String(), "data: ") {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read notification event: %v", err)
		}
		received.WriteString(line)
	}
	if !strings.Contains(received.String(), event.Type) || !strings.Contains(received.String(), event.SnapshotID) {
		t.Fatalf("notification stream returned %q, want event %#v", received.String(), event)
	}

	badOriginRequest, err := http.NewRequest(http.MethodGet, server.URL+apiPrefix+"/notifications", nil)
	if err != nil {
		t.Fatal(err)
	}
	badOriginRequest.AddCookie(&http.Cookie{Name: adminSessionCookieName, Value: login.Token})
	badOriginRequest.Header.Set("Origin", "https://attacker.example.test")
	badOriginResponse, err := http.DefaultClient.Do(badOriginRequest)
	if err != nil {
		t.Fatal(err)
	}
	badOriginResponse.Body.Close()
	if badOriginResponse.StatusCode != http.StatusForbidden {
		t.Fatalf("notification stream with an untrusted Origin returned %d, want %d", badOriginResponse.StatusCode, http.StatusForbidden)
	}
}

func TestNotificationStreamAcceptsSameOriginBrowserGETWithoutOrigin(t *testing.T) {
	sessions := &sessionStub{login: testSessionLogin()}
	handler, err := NewHandlerWithSessions(&fakeIdentityAdmin{}, sessions, Config{
		MaxJSONBytes: 1024, SessionCookieSecure: true, AllowedOrigins: []string{testAdminOrigin}, Notifications: NewNotificationHub(),
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+apiPrefix+"/notifications", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.AddCookie(&http.Cookie{Name: adminSessionCookieName, Value: sessions.login.Token})
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	request.Header.Set("Referer", testAdminOrigin+"/admin/")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("same-origin EventSource without Origin returned %d, want %d", response.StatusCode, http.StatusOK)
	}
}

func TestNotificationHubRejectsInvalidEvent(t *testing.T) {
	hub := NewNotificationHub()
	if err := hub.Publish(uuid.NewString(), NotificationEvent{Type: "unknown", State: "ready"}); err == nil {
		t.Fatal("invalid event was accepted")
	}
}
