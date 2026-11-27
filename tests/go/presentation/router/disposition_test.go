package router

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/Zubrium/cdn/pkg/contracts"
)

func deliverCoreFileDisposition(t *testing.T, contentType, configured string, method string, ifNoneMatch string) contracts.DeliveryResponse {
	t.Helper()
	fileID := "01938f7a-68af-7c10-a51d-78b6ca125401"
	files := newCacheScenarioFiles()
	files.objects[fileID] = cacheScenarioFile{
		file: contracts.File{ID: fileID, ContentType: contentType, SHA256: strings.Repeat("a", 64), Size: 4},
		body: "body",
	}
	target := contracts.Target{Kind: contracts.TargetCoreFile, FileID: fileID, Disposition: configured}
	route := cacheScenarioRoute(target, 1)
	route.Path = "/image"
	route.Match = contracts.MatchExact
	routes := &cacheScenarioRoutes{route: route}
	delivery, err := NewCoreDelivery(files, routes, nil)
	if err != nil {
		t.Fatal(err)
	}
	request := cacheScenarioDeliveryRequest("/image")
	request.Method = method
	request.IfNoneMatch = ifNoneMatch
	response, err := delivery.Deliver(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func TestCoreFileDispositionHonorsSafeMIMEDefaultAndExplicitOverride(t *testing.T) {
	for name, scenario := range map[string]struct {
		contentType string
		configured  string
		want        string
	}{
		"webp default":     {contentType: "image/webp", want: "inline"},
		"jpeg default":     {contentType: "image/jpeg", want: "inline"},
		"video default":    {contentType: "video/mp4", want: "inline"},
		"pdf default":      {contentType: "application/pdf", want: "inline"},
		"svg forced safe":  {contentType: "image/svg+xml", configured: "inline", want: "attachment"},
		"html forced safe": {contentType: "text/html; charset=utf-8", configured: "inline", want: "attachment"},
		"octet default":    {contentType: "application/octet-stream", want: "attachment"},
		"image attachment": {contentType: "image/webp", configured: "attachment", want: "attachment"},
		"unknown mime":     {contentType: "application/x-custom", want: "attachment"},
	} {
		t.Run(name, func(t *testing.T) {
			response := deliverCoreFileDisposition(t, scenario.contentType, scenario.configured, http.MethodGet, "")
			if got := response.ContentDisposition; got != scenario.want {
				t.Fatalf("Content-Disposition = %q, want %q", got, scenario.want)
			}
			_ = cacheScenarioBody(t, response)
		})
	}
}

func TestCoreFileDispositionMatchesHEADAndNotModified(t *testing.T) {
	etag := `"sha256-` + strings.Repeat("a", 64) + `"`
	for name, scenario := range map[string]struct {
		method          string
		ifNoneMatch     string
		wantStatus      int
		wantDisposition string
	}{
		"head":         {method: http.MethodHead, wantStatus: http.StatusOK, wantDisposition: "inline"},
		"not modified": {method: http.MethodGet, ifNoneMatch: etag, wantStatus: http.StatusNotModified, wantDisposition: "inline"},
	} {
		t.Run(name, func(t *testing.T) {
			response := deliverCoreFileDisposition(t, "image/webp", "", scenario.method, scenario.ifNoneMatch)
			if response.StatusCode != scenario.wantStatus {
				t.Fatalf("status = %d, want %d", response.StatusCode, scenario.wantStatus)
			}
			if got := response.ContentDisposition; got != scenario.wantDisposition {
				t.Fatalf("Content-Disposition = %q, want %q", got, scenario.wantDisposition)
			}
			_ = cacheScenarioBody(t, response)
		})
	}
}
