package sitebuilder

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"websiteengine/features/sitebuilder"
	"websiteengine/pkg/response"
)

func TestSitepackImportErrorReturnsSafeLocatorAndWholeArchiveRejection(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v2/admin/site/snapshots/import", nil)
	req = req.WithContext(response.WithRequestID(req.Context(), "req-sitepack-test"))
	res := httptest.NewRecorder()
	writeSitepackImportError(res, req, &sitebuilder.SitepackImportFailure{Diagnostic: sitebuilder.SitepackImportDiagnostic{
		Path: "files[1].renditions[0]", Code: "invalid_rendition_file", Message: "Содержимое варианта файла не прошло проверку",
	}})
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", res.Code)
	}
	var envelope struct {
		Error struct {
			Message   string `json:"message"`
			RequestID string `json:"request_id"`
			Details   []struct {
				Path    string `json:"path"`
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(envelope.Error.Message, "целиком") || envelope.Error.RequestID != "req-sitepack-test" || len(envelope.Error.Details) != 1 {
		t.Fatalf("unexpected error envelope: %#v", envelope.Error)
	}
	if got := envelope.Error.Details[0]; got.Path != "files[1].renditions[0]" || got.Code != "invalid_rendition_file" || got.Message == "" {
		t.Fatalf("unexpected item diagnostic: %#v", got)
	}
}

func TestSitepackImportErrorDoesNotExposeUnknownErrorText(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v2/admin/site/snapshots/import", nil)
	res := httptest.NewRecorder()
	writeSitepackImportError(res, req, errors.New("TEST_SECRET_CANARY storage backend details"))
	if res.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d, want 500", res.Code)
	}
	if strings.Contains(res.Body.String(), "TEST_SECRET_CANARY") || strings.Contains(res.Body.String(), "storage backend details") {
		t.Fatalf("unknown error leaked through API: %s", res.Body.String())
	}
}
