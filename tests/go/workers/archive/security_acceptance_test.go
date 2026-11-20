package archive

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"testing"
)

func TestInspectRejectsArchiveWithDamagedContent(t *testing.T) {
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	file, err := writer.CreateHeader(&zip.FileHeader{Name: "asset.txt", Method: zip.Store})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(file, "original bytes"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	data := buf.Bytes()
	index, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	offset, err := index.File[0].DataOffset()
	if err != nil {
		t.Fatal(err)
	}
	data[offset] ^= 1
	limits := Limits{MaxArchiveBytes: 4096, MaxEntries: 10, MaxEntryBytes: 100, MaxExpandedBytes: 1000, MaxCompressionRatio: 100}
	if files, err := Inspect(context.Background(), bytes.NewReader(data), int64(len(data)), limits); err == nil {
		t.Fatalf("corrupted ZIP returned files: %+v", files)
	}
}

func TestInspectReturnsDigestOfUncompressedBytes(t *testing.T) {
	data := zipFixture(t, "asset.txt")
	limits := Limits{MaxArchiveBytes: 4096, MaxEntries: 10, MaxEntryBytes: 100, MaxExpandedBytes: 1000, MaxCompressionRatio: 100}
	files, err := Inspect(context.Background(), bytes.NewReader(data), int64(len(data)), limits)
	if err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256([]byte("content"))
	if len(files) != 1 || files[0].SHA256 != hex.EncodeToString(want[:]) {
		t.Fatalf("unexpected verified file digest: %+v", files)
	}
}

func TestInspectRejectsPortablePathCollisions(t *testing.T) {
	for _, entries := range [][]string{
		{"assets", "assets/app.js"},
		{"assets/app.js", "assets"},
		{"assets/App.js", "assets/app.js"},
		{"A/file.js", "a/other.js"},
	} {
		t.Run(entries[0]+"_and_"+entries[1], func(t *testing.T) {
			data := zipFixture(t, entries...)
			limits := Limits{MaxArchiveBytes: 4096, MaxEntries: 10, MaxEntryBytes: 100, MaxExpandedBytes: 1000, MaxCompressionRatio: 100}
			if files, err := Inspect(context.Background(), bytes.NewReader(data), int64(len(data)), limits); err == nil {
				t.Fatalf("ambiguous archive paths returned files: %+v", files)
			}
		})
	}
}

func TestRatioExceededUsesExactRatio(t *testing.T) {
	for _, tc := range []struct {
		uncompressed uint64
		compressed   uint64
		maximum      uint64
		want         bool
	}{
		{0, 0, 1, false},
		{2, 2, 1, false},
		{3, 2, 1, true},
		{2, 1, 2, false},
		{3, 1, 2, true},
		{1, 0, 100, true},
	} {
		if got := ratioExceeded(tc.uncompressed, tc.compressed, tc.maximum); got != tc.want {
			t.Errorf("ratioExceeded(%d, %d, %d)=%v, want %v", tc.uncompressed, tc.compressed, tc.maximum, got, tc.want)
		}
	}
}

func TestInspectRejectsDirectoryOnlyArchive(t *testing.T) {
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	header := &zip.FileHeader{Name: "assets/"}
	header.SetMode(fs.ModeDir | 0o755)
	if _, err := writer.CreateHeader(header); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	data := buf.Bytes()
	limits := Limits{MaxArchiveBytes: 4096, MaxEntries: 10, MaxEntryBytes: 100, MaxExpandedBytes: 1000, MaxCompressionRatio: 100}
	if files, err := Inspect(context.Background(), bytes.NewReader(data), int64(len(data)), limits); err == nil {
		t.Fatalf("directory-only archive accepted: %+v", files)
	}
}

func TestInspectRejectsExcessiveCompressionRatio(t *testing.T) {
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	entry, err := writer.Create("asset.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write(bytes.Repeat([]byte("a"), 8192)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	data := buf.Bytes()
	limits := Limits{MaxArchiveBytes: 4096, MaxEntries: 10, MaxEntryBytes: 16384, MaxExpandedBytes: 16384, MaxCompressionRatio: 2}
	if files, err := Inspect(context.Background(), bytes.NewReader(data), int64(len(data)), limits); err == nil {
		t.Fatalf("highly compressed ZIP returned files: %+v", files)
	}
}

func TestInspectPortablePathInputs(t *testing.T) {
	for _, tc := range []struct {
		name  string
		path  string
		valid bool
	}{
		{"root file", "manifest.json", true},
		{"nested file", "assets/css/main.css", true},
		{"unicode file", "assets/иконка.svg", true},
		{"root traversal", "../secret", false},
		{"nested traversal", "assets/../../secret", false},
		{"absolute path", "/etc/passwd", false},
		{"backslash separator", `assets\file.js`, false},
		{"dot segment", "assets/./file.js", false},
		{"double slash", "assets//file.js", false},
		{"windows drive path", "C:/secret.txt", false},
		{"windows drive relative path", "C:secret.txt", false},
		{"lowercase windows drive path", "c:/secret.txt", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := zipFixture(t, tc.path)
			limits := Limits{MaxArchiveBytes: 4096, MaxEntries: 10, MaxEntryBytes: 100, MaxExpandedBytes: 1000, MaxCompressionRatio: 100}
			files, err := Inspect(context.Background(), bytes.NewReader(data), int64(len(data)), limits)
			if tc.valid && (err != nil || len(files) != 1 || files[0].Path != tc.path) {
				t.Fatalf("expected file %q, got %+v: %v", tc.path, files, err)
			}
			if !tc.valid && err == nil {
				t.Fatalf("unsafe path %q was accepted", tc.path)
			}
		})
	}
}

func TestInspectRejectsEntryCountAndPerFileLimit(t *testing.T) {
	data := zipFixture(t, "manifest.json", "assets/app.js")
	for _, limits := range []Limits{
		{MaxArchiveBytes: 4096, MaxEntries: 1, MaxEntryBytes: 100, MaxExpandedBytes: 1000, MaxCompressionRatio: 100},
		{MaxArchiveBytes: 4096, MaxEntries: 10, MaxEntryBytes: 6, MaxExpandedBytes: 1000, MaxCompressionRatio: 100},
	} {
		if files, err := Inspect(context.Background(), bytes.NewReader(data), int64(len(data)), limits); err == nil {
			t.Fatalf("ZIP passed configured limit %+v: %+v", limits, files)
		}
	}
}
