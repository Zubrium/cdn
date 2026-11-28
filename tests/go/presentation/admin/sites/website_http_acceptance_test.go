package sitesapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Zubrium/cdn/pkg/contracts"
)

type mediaContractAPI struct {
	contracts.AdminWebsiteAPI
	requests []contracts.AdminWebsiteCreateDerivativeRequest
}

func (api *mediaContractAPI) Authenticate(ctx context.Context, credential string) (context.Context, error) {
	if credential != "fixture-website-credential" {
		return nil, contracts.ErrAdminWebsiteUnauthorized
	}
	return ctx, nil
}

func (api *mediaContractAPI) CreateMediaDerivative(_ context.Context, request contracts.AdminWebsiteCreateDerivativeRequest) (contracts.AdminWebsiteMediaJob, error) {
	api.requests = append(api.requests, request)
	return contracts.AdminWebsiteMediaJob{ID: "media-job", SourceFileID: request.SourceFileID, State: "queued"}, nil
}

func TestWebsiteAdminHTTPAcceptanceWEB017(t *testing.T) {
	const snapshotID = "a53fd73d-8c93-4926-9f27-2f73fcfe2047"
	const sourceID = "f0cb48a1-295d-44b0-8da0-89b92e5aead0"
	const idempotencyKey = "fixture-media-idempotency-key"

	for _, tc := range []struct {
		name string
		kind string
		want int
	}{
		{"arguments", "arguments", http.StatusAccepted},
		{"data inputs", "dataInputs", http.StatusAccepted},
		{"missing schema kind", "", http.StatusBadRequest},
		{"unknown schema kind", "content", http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := &mediaContractAPI{}
			handler, err := NewHandler(api, Config{
				MaxJSONBytes: 4096, DefaultPageSize: 20, MaxPageSize: 100,
				AllowedOrigins: []string{"https://admin.example.test"},
			})
			if err != nil {
				t.Fatal(err)
			}
			body := map[string]string{
				"snapshot_id": snapshotID, "placement_id": "hero", "field_path": "/image", "source_file_id": sourceID,
			}
			if tc.kind != "" {
				body["schema_kind"] = tc.kind
			}
			payload, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, "/api/admin/media/derivatives", strings.NewReader(string(payload)))
			req.Header.Set("Authorization", "Bearer fixture-website-credential")
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Idempotency-Key", idempotencyKey)
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", res.Code, tc.want, res.Body.String())
			}
			if tc.want != http.StatusAccepted {
				if len(api.requests) != 0 || res.Header().Get("Location") != "" {
					t.Fatalf("invalid request reached the application or advertised a job: %v", api.requests)
				}
				return
			}
			want := contracts.AdminWebsiteCreateDerivativeRequest{
				SnapshotID: snapshotID, PlacementID: "hero", SchemaKind: tc.kind,
				FieldPath: "/image", SourceFileID: sourceID, IdempotencyKey: idempotencyKey,
			}
			if len(api.requests) != 1 || api.requests[0] != want {
				t.Fatalf("requests = %+v, want [%+v]", api.requests, want)
			}
			if res.Header().Get("Location") != "/api/admin/media/jobs/media-job" || res.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("unexpected response headers: %v", res.Header())
			}
		})
	}
}
