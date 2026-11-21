package publiccontentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	contentapi "github.com/Zubrium/cdn/engines/content/presentation"
	router "github.com/Zubrium/cdn/engines/router/presentation"
)

const validRouteConfig = `{"enabled":true,"page_snapshot_id":"01938f7a-68af-7c10-a51d-78b6ca125310","locale":"en-US"}`

type stubReader struct {
	content    contentapi.PublishedContent
	err        error
	calls      int
	snapshotID string
	locale     string
}

func (reader *stubReader) ReadPublishedContent(_ context.Context, snapshotID, locale string) (contentapi.PublishedContent, error) {
	reader.calls++
	reader.snapshotID = snapshotID
	reader.locale = locale
	return reader.content, reader.err
}

func fixtureContent() contentapi.PublishedContent {
	return contentapi.PublishedContent{
		SnapshotID:      "01938f7a-68af-7c10-a51d-78b6ca125310",
		Locale:          "en-US",
		ContentRevision: 7,
		Records: []contentapi.Record{{
			ID:            "record-1",
			TranslationID: "translation-1",
			Locale:        "en-US",
			EntityID:      "entity-1",
			SortOrder:     1,
			Values:        map[string]any{"title": "Hello"},
		}},
		Selections: []contentapi.Selection{{
			PlacementID: "placement-1",
			InputKey:    "items",
			Mode:        "manual",
			RecordIDs:   []string{"record-1"},
		}},
	}
}

func newTestOperation(t *testing.T, reader Reader) *Operation {
	t.Helper()
	operation, err := New(reader)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return operation
}

func getRequest() router.PublicOperationRequest {
	return router.PublicOperationRequest{Method: http.MethodGet, EscapedPath: "/content", Body: http.NoBody}
}

func readAll(t *testing.T, body io.ReadCloser) string {
	t.Helper()
	defer body.Close()
	value, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(value)
}

func TestDescriptor(t *testing.T) {
	operation := newTestOperation(t, &stubReader{})
	descriptor := operation.Descriptor()
	if descriptor.ID != OperationID {
		t.Fatalf("ID = %q, want %q", descriptor.ID, OperationID)
	}
	if descriptor.ContractVersion != ContractVersion || ContractVersion != 1 {
		t.Fatalf("ContractVersion = %d", descriptor.ContractVersion)
	}
	if !reflect.DeepEqual(descriptor.Methods, []string{http.MethodGet, http.MethodHead}) {
		t.Fatalf("Methods = %v", descriptor.Methods)
	}
	if descriptor.MaxRequestBytes != 1 {
		t.Fatalf("MaxRequestBytes = %d", descriptor.MaxRequestBytes)
	}
	if !reflect.DeepEqual(descriptor.AllowedContentTypes, []string{"application/json"}) {
		t.Fatalf("AllowedContentTypes = %v", descriptor.AllowedContentTypes)
	}
}

type typedNilReader struct{}

func (*typedNilReader) ReadPublishedContent(context.Context, string, string) (contentapi.PublishedContent, error) {
	return contentapi.PublishedContent{}, nil
}

func TestNewFailsClosedOnNilReader(t *testing.T) {
	if _, err := New(nil); !errors.Is(err, router.ErrUnavailable) {
		t.Fatalf("New(nil) error = %v, want ErrUnavailable", err)
	}
	var typed *typedNilReader
	if _, err := New(typed); !errors.Is(err, router.ErrUnavailable) {
		t.Fatalf("New(typed nil) error = %v, want ErrUnavailable", err)
	}
}

func TestValidateRouteConfig(t *testing.T) {
	operation := newTestOperation(t, &stubReader{})
	cases := []struct {
		name string
		raw  string
		ok   bool
	}{
		{name: "valid", raw: validRouteConfig, ok: true},
		{name: "unknown field", raw: `{"enabled":true,"page_snapshot_id":"01938f7a-68af-7c10-a51d-78b6ca125310","locale":"en-US","extra":1}`},
		{name: "disabled", raw: `{"enabled":false,"page_snapshot_id":"01938f7a-68af-7c10-a51d-78b6ca125310","locale":"en-US"}`},
		{name: "bad uuid", raw: `{"enabled":true,"page_snapshot_id":"01938F7A-68AF-7C10-A51D-78B6CA125310","locale":"en-US"}`},
		{name: "nil uuid", raw: `{"enabled":true,"page_snapshot_id":"00000000-0000-0000-0000-000000000000","locale":"en-US"}`},
		{name: "locale with slash", raw: `{"enabled":true,"page_snapshot_id":"01938f7a-68af-7c10-a51d-78b6ca125310","locale":"en/US"}`},
		{name: "empty locale", raw: `{"enabled":true,"page_snapshot_id":"01938f7a-68af-7c10-a51d-78b6ca125310","locale":""}`},
		{name: "long locale", raw: `{"enabled":true,"page_snapshot_id":"01938f7a-68af-7c10-a51d-78b6ca125310","locale":"abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyz"}`},
		{name: "empty raw", raw: ``},
		{name: "array", raw: `[]`},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			err := operation.ValidateRouteConfig(json.RawMessage(testCase.raw))
			if testCase.ok && err != nil {
				t.Fatalf("ValidateRouteConfig: %v", err)
			}
			if !testCase.ok && !errors.Is(err, router.ErrInvalidTarget) {
				t.Fatalf("ValidateRouteConfig error = %v, want ErrInvalidTarget", err)
			}
		})
	}
}

func TestExecuteGETSuccess(t *testing.T) {
	content := fixtureContent()
	reader := &stubReader{content: content}
	operation := newTestOperation(t, reader)
	wantBody, err := json.Marshal(content)
	if err != nil {
		t.Fatal(err)
	}

	response, err := operation.Execute(context.Background(), json.RawMessage(validRouteConfig), getRequest())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("StatusCode = %d", response.StatusCode)
	}
	if response.ContentType != "application/json" {
		t.Fatalf("ContentType = %q", response.ContentType)
	}
	if response.ContentLength != int64(len(wantBody)) {
		t.Fatalf("ContentLength = %d, want %d", response.ContentLength, len(wantBody))
	}
	if response.ETag != `"7"` {
		t.Fatalf("ETag = %q", response.ETag)
	}
	if response.CacheControl != "no-store" {
		t.Fatalf("CacheControl = %q", response.CacheControl)
	}
	if response.Body == nil {
		t.Fatal("GET body is nil")
	}
	if got := readAll(t, response.Body); got != string(wantBody) {
		t.Fatalf("body = %s, want %s", got, wantBody)
	}
	if reader.calls != 1 || reader.snapshotID != "01938f7a-68af-7c10-a51d-78b6ca125310" || reader.locale != "en-US" {
		t.Fatalf("reader calls = %d, snapshot = %q, locale = %q", reader.calls, reader.snapshotID, reader.locale)
	}
}

func TestExecuteHEADHasHeadersButNoBody(t *testing.T) {
	content := fixtureContent()
	operation := newTestOperation(t, &stubReader{content: content})
	wantBody, err := json.Marshal(content)
	if err != nil {
		t.Fatal(err)
	}
	request := getRequest()
	request.Method = http.MethodHead

	response, err := operation.Execute(context.Background(), json.RawMessage(validRouteConfig), request)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if response.StatusCode != http.StatusOK || response.Body != nil {
		t.Fatalf("HEAD response status = %d, body = %v", response.StatusCode, response.Body)
	}
	if response.ContentType != "application/json" || response.ContentLength != int64(len(wantBody)) ||
		response.ETag != `"7"` || response.CacheControl != "no-store" {
		t.Fatalf("HEAD headers = %+v", response)
	}
}

func TestExecuteIfNoneMatch(t *testing.T) {
	operation := newTestOperation(t, &stubReader{content: fixtureContent()})
	matching := getRequest()
	matching.IfNoneMatch = `"7"`

	response, err := operation.Execute(context.Background(), json.RawMessage(validRouteConfig), matching)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if response.StatusCode != http.StatusNotModified || response.Body != nil || response.ETag != `"7"` ||
		response.CacheControl != "no-store" {
		t.Fatalf("304 response = %+v", response)
	}

	notMatching := getRequest()
	notMatching.IfNoneMatch = `"6"`
	response, err = operation.Execute(context.Background(), json.RawMessage(validRouteConfig), notMatching)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if response.StatusCode != http.StatusOK || response.Body == nil {
		t.Fatalf("non-matching precondition response = %+v", response)
	}
	_ = response.Body.Close()
}

func TestExecuteRejectsInvalidRequests(t *testing.T) {
	operation := newTestOperation(t, &stubReader{content: fixtureContent()})
	requestWithBody := func(body string, declaredLength int64) router.PublicOperationRequest {
		request := getRequest()
		request.ContentLength = declaredLength
		request.Body = strings.NewReader(body)
		return request
	}
	cases := []struct {
		name    string
		request router.PublicOperationRequest
	}{
		{name: "method", request: router.PublicOperationRequest{Method: http.MethodPost, Body: http.NoBody}},
		{name: "query", request: func() router.PublicOperationRequest {
			request := getRequest()
			request.Query = "locale=fr"
			return request
		}()},
		{name: "declared body", request: requestWithBody("x", 1)},
		{name: "streamed body", request: requestWithBody("x", 0)},
		{name: "nil body", request: router.PublicOperationRequest{Method: http.MethodGet}},
		{name: "content type", request: func() router.PublicOperationRequest {
			request := getRequest()
			request.ContentType = "application/json"
			return request
		}()},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := operation.Execute(context.Background(), json.RawMessage(validRouteConfig), testCase.request)
			if !errors.Is(err, router.ErrInvalid) {
				t.Fatalf("Execute error = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestExecuteRejectsInvalidConfig(t *testing.T) {
	operation := newTestOperation(t, &stubReader{content: fixtureContent()})
	_, err := operation.Execute(context.Background(), json.RawMessage(`{"enabled":true}`), getRequest())
	if !errors.Is(err, router.ErrInvalidTarget) {
		t.Fatalf("Execute error = %v, want ErrInvalidTarget", err)
	}
}

func TestExecuteReaderErrorMapsToUnavailable(t *testing.T) {
	operation := newTestOperation(t, &stubReader{err: errors.New("internal failure")})
	_, err := operation.Execute(context.Background(), json.RawMessage(validRouteConfig), getRequest())
	if !errors.Is(err, router.ErrUnavailable) {
		t.Fatalf("Execute error = %v, want ErrUnavailable", err)
	}
}
