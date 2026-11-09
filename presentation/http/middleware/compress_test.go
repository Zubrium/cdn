package middleware

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCompressTextGzipsPublicHTMLAndPreservesValidators(t *testing.T) {
	body := strings.Repeat("semantic public content ", 80)
	handler := CompressText(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("ETag", `"html-identity"`)
		w.Header().Set("Vary", "Origin")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, body)
	}))
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Accept-Encoding", "br, gzip;q=0.8")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK || response.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("status/encoding = %d/%q", response.Code, response.Header().Get("Content-Encoding"))
	}
	if got := response.Header().Get("ETag"); got != `W/"html-identity"` {
		t.Fatalf("compressed representation ETag = %q", got)
	}
	if got := response.Header().Get("Vary"); got != "Origin, Accept-Encoding" {
		t.Fatalf("Vary = %q", got)
	}
	if response.Header().Get("Content-Length") != "" {
		t.Fatalf("compressed response has stale Content-Length %q", response.Header().Get("Content-Length"))
	}
	reader, err := gzip.NewReader(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if string(decoded) != body {
		t.Fatal("gzip response did not preserve the original HTML")
	}
}

func TestCompressTextSkipsUnsupportedOrSmallRepresentations(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        string
		accept      string
		status      int
		rangeHeader string
	}{
		{name: "small", contentType: "text/plain", body: "small", accept: "gzip"},
		{name: "q-zero", contentType: "text/html", body: strings.Repeat("x", 600), accept: "gzip;q=0"},
		{name: "binary", contentType: "image/png", body: strings.Repeat("x", 600), accept: "gzip"},
		{name: "partial", contentType: "text/css", body: strings.Repeat("x", 600), accept: "gzip", status: http.StatusPartialContent, rangeHeader: "bytes=0-599"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := CompressText(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", test.contentType)
				if test.status != 0 {
					w.WriteHeader(test.status)
				}
				_, _ = io.WriteString(w, test.body)
			}))
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			request.Header.Set("Accept-Encoding", test.accept)
			request.Header.Set("Range", test.rangeHeader)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if got := response.Header().Get("Content-Encoding"); got != "" {
				t.Fatalf("unexpected encoding %q", got)
			}
			if got := response.Body.String(); got != test.body {
				t.Fatalf("body changed: got %q", got)
			}
		})
	}
}

func TestAcceptsGzipHonorsQualityAndWildcard(t *testing.T) {
	for _, test := range []struct {
		header string
		want   bool
	}{
		{header: "gzip", want: true},
		{header: "br, gzip;q=0.4", want: true},
		{header: "gzip;q=0, *;q=1", want: false},
		{header: "*;q=0.5", want: true},
		{header: "br", want: false},
	} {
		if got := acceptsGzip(test.header); got != test.want {
			t.Errorf("acceptsGzip(%q) = %v, want %v", test.header, got, test.want)
		}
	}
}

func TestCompressTextHEADAndNotModifiedHeaders(t *testing.T) {
	for _, test := range []struct {
		name              string
		method            string
		status            int
		contentLength     string
		wantEncoding      string
		wantWeakValidator bool
	}{
		{name: "head gzip metadata", method: http.MethodHead, status: http.StatusOK, contentLength: "1200", wantEncoding: "gzip", wantWeakValidator: true},
		{name: "not modified stays empty", method: http.MethodGet, status: http.StatusNotModified},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler := CompressText(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.Header().Set("ETag", `"page"`)
				if test.contentLength != "" {
					w.Header().Set("Content-Length", test.contentLength)
				}
				if test.status == http.StatusNotModified {
					w.WriteHeader(test.status)
					return
				}
				w.WriteHeader(test.status)
			}))
			request := httptest.NewRequest(test.method, "/", nil)
			request.Header.Set("Accept-Encoding", "gzip")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d", response.Code, test.status)
			}
			if got := response.Header().Get("Content-Encoding"); got != test.wantEncoding {
				t.Fatalf("Content-Encoding = %q, want %q", got, test.wantEncoding)
			}
			if test.wantWeakValidator && response.Header().Get("ETag") != `W/"page"` {
				t.Fatalf("compressed HEAD validator = %q", response.Header().Get("ETag"))
			}
			if !strings.Contains(response.Header().Get("Vary"), "Accept-Encoding") {
				t.Fatalf("Vary omits Accept-Encoding: %q", response.Header().Get("Vary"))
			}
			if response.Body.Len() != 0 {
				t.Fatalf("HEAD/304 wrote %d body bytes", response.Body.Len())
			}
		})
	}
}
