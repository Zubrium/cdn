package middleware

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRecoverPropagatesAbortedStreamingResponses(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := Recover(logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("partial"))
		panic(http.ErrAbortHandler)
	}))
	recorder := httptest.NewRecorder()
	defer func() {
		if got := recover(); got != http.ErrAbortHandler {
			t.Fatalf("abort must reach net/http, got %v", got)
		}
		if recorder.Body.String() != "partial" {
			t.Fatalf("unexpected bytes after abort: %q", recorder.Body.String())
		}
	}()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
}

func TestRecoverHandlesUnexpectedPanic(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := Recover(logger)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("unexpected") }))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d", recorder.Code)
	}
}
