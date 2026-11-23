package observability

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCounterOnlySeriesAppearsInMetricsSnapshot(t *testing.T) {
	store := newMetricStore()
	store.inc("zubrium_http_server_requests_total", []Attribute{String("http.route", "/healthz")}, 1)

	snapshot := store.render()
	if !strings.Contains(snapshot, "zubrium_http_server_requests_total{http.route=\"/healthz\"} 1") {
		t.Fatalf("counter missing from /metrics snapshot: %q", snapshot)
	}
}

func TestSignalFileRotationBoundsIndicesByMaxFiles(t *testing.T) {
	const maxFiles = 3
	directory := t.TempDir()
	writer, err := newSignalFile(directory, "logs", 48, maxFiles)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 12; index++ {
		if err := writer.write(map[string]any{"index": index, "padding": "abcdefgh"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.close(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	// SPEC-observability §2.1: active file plus rotated indices 1..max_files.
	if len(entries) > maxFiles+1 {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Fatalf("rotation created %d files (max_files=%d): %v", len(entries), maxFiles, names)
	}
	if _, err := os.Stat(filepath.Join(directory, "logs.jsonl.1")); err != nil {
		t.Fatalf("expected active rotated file .1: %v", err)
	}
	if _, err := os.Stat(filepath.Join(directory, "logs.jsonl.4")); !os.IsNotExist(err) {
		t.Fatalf("rotated index above max_files must be removed, stat error = %v", err)
	}
}
