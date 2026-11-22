//go:build website_acceptance
// +build website_acceptance

package sitesapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	website "github.com/Zubrium/cdn/engines/website/presentation"
)

type web017API struct {
	*fakeWebsite
	getErr error
}

func (a *web017API) GetSnapshot(ctx context.Context, snapshotID string) (website.Snapshot, error) {
	if a.getErr != nil {
		return website.Snapshot{}, a.getErr
	}
	return a.fakeWebsite.GetSnapshot(ctx, snapshotID)
}

func web017Serve(t *testing.T, handler http.Handler, method, path, body, idempotencyKey, ifMatch string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer user-secret")
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	if ifMatch != "" {
		request.Header.Set("If-Match", ifMatch)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestWebsiteAdminHTTPAcceptanceWEB017(t *testing.T) {
	t.Run("WEB-017", func(t *testing.T) {
		api := &web017API{fakeWebsite: &fakeWebsite{}}
		handler, err := NewHandler(api, Config{MaxJSONBytes: 1 << 20, DefaultPageSize: 10, MaxPageSize: 100, AllowedOrigins: []string{"https://admin.example"}})
		if err != nil {
			t.Fatal(err)
		}
		const pageID = "22222222-2222-4222-8222-222222222222"
		validBody := `{"page_id":"` + pageID + `","name":"Home","payload":{"config":{"format_version":3},"metadata":{}}}`

		missingKey := web017Serve(t, handler, http.MethodPost, "/api/admin/snapshots", validBody, "", "")
		if missingKey.Code != http.StatusBadRequest || !strings.Contains(missingKey.Body.String(), "invalid_request") || api.createSnapshotCalls != 0 {
			t.Fatalf("missing Idempotency-Key: status=%d body=%s calls=%d", missingKey.Code, missingKey.Body.String(), api.createSnapshotCalls)
		}

		unsupportedField := web017Serve(t, handler, http.MethodPost, "/api/admin/snapshots",
			`{"page_id":"`+pageID+`","name":"Home","route":"/public","payload":{"config":{"format_version":3},"metadata":{}}}`,
			"web017-idempotency-0001", "")
		if unsupportedField.Code != http.StatusBadRequest || api.createSnapshotCalls != 0 {
			t.Fatalf("unsupported Router field: status=%d body=%s calls=%d", unsupportedField.Code, unsupportedField.Body.String(), api.createSnapshotCalls)
		}

		versioned := web017Serve(t, handler, http.MethodPost, "/api/v1/admin/snapshots", validBody, "web017-idempotency-0002", "")
		if versioned.Code != http.StatusNotFound || api.createSnapshotCalls != 0 {
			t.Fatalf("versioned route: status=%d body=%s calls=%d", versioned.Code, versioned.Body.String(), api.createSnapshotCalls)
		}

		missingRevision := web017Serve(t, handler, http.MethodDelete, "/api/admin/snapshots/"+pageID, "", "", "")
		if missingRevision.Code != http.StatusPreconditionRequired || api.deleteSnapshotCalls != 0 {
			t.Fatalf("missing If-Match: status=%d body=%s calls=%d", missingRevision.Code, missingRevision.Body.String(), api.deleteSnapshotCalls)
		}

		api.getErr = website.ErrNotFound
		unknown := web017Serve(t, handler, http.MethodGet, "/api/admin/snapshots/"+pageID, "", "", "")
		if unknown.Code != http.StatusNotFound || strings.Contains(unknown.Body.String(), pageID) {
			t.Fatalf("unknown snapshot disclosed identity: status=%d body=%s", unknown.Code, unknown.Body.String())
		}

		api.getErr = website.ErrForbidden
		forbidden := web017Serve(t, handler, http.MethodGet, "/api/admin/snapshots/"+pageID, "", "", "")
		if forbidden.Code != http.StatusForbidden || strings.Contains(forbidden.Body.String(), pageID) {
			t.Fatalf("forbidden snapshot disclosed identity: status=%d body=%s", forbidden.Code, forbidden.Body.String())
		}
	})
}
