package archive

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"io/fs"
	"testing"
)

func zipFixture(t *testing.T, entries ...string) []byte {
	t.Helper()
	var output bytes.Buffer
	w := zip.NewWriter(&output)
	for _, name := range entries {
		file, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(file, "content"); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func TestInspectRejectsSymlinks(t *testing.T) {
	var data bytes.Buffer
	w := zip.NewWriter(&data)
	header := &zip.FileHeader{Name: "assets/link"}
	header.SetMode(fs.ModeSymlink | 0o777)
	entry, err := w.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(entry, "../../secret"); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = Inspect(context.Background(), bytes.NewReader(data.Bytes()), int64(data.Len()), Limits{MaxArchiveBytes: 4096, MaxEntries: 10, MaxEntryBytes: 100, MaxExpandedBytes: 1000, MaxCompressionRatio: 1000})
	if err == nil {
		t.Fatal("symbolic link was accepted")
	}
}

func TestInspectRejectsUnsafeAndDuplicatePaths(t *testing.T) {
	for _, entries := range [][]string{{"../secret"}, {"/absolute"}, {"a\\b"}, {"a", "a"}, {"a/./b"}} {
		t.Run(entries[0], func(t *testing.T) {
			data := zipFixture(t, entries...)
			_, err := Inspect(context.Background(), bytes.NewReader(data), int64(len(data)), Limits{MaxArchiveBytes: 4096, MaxEntries: 10, MaxEntryBytes: 100, MaxExpandedBytes: 1000, MaxCompressionRatio: 1000})
			if err == nil {
				t.Fatalf("unsafe entries accepted: %v", entries)
			}
		})
	}
}

func TestInspectReportsVerifiedFilesAndEnforcesLimits(t *testing.T) {
	data := zipFixture(t, "manifest.json", "assets/app.js")
	limits := Limits{MaxArchiveBytes: 4096, MaxEntries: 2, MaxEntryBytes: 20, MaxExpandedBytes: 20, MaxCompressionRatio: 1000}
	files, err := Inspect(context.Background(), bytes.NewReader(data), int64(len(data)), limits)
	if err != nil || len(files) != 2 || files[1].Path != "assets/app.js" || files[1].Size != 7 || len(files[1].SHA256) != 64 {
		t.Fatalf("files=%+v err=%v", files, err)
	}
	limits.MaxExpandedBytes = 10
	if _, err := Inspect(context.Background(), bytes.NewReader(data), int64(len(data)), limits); err == nil {
		t.Fatal("expanded size limit ignored")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	limits.MaxExpandedBytes = 20
	if _, err := Inspect(ctx, bytes.NewReader(data), int64(len(data)), limits); err == nil {
		t.Fatal("cancelled operation succeeded")
	}
}
