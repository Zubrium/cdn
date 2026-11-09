package sitebuilder

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestStageSitepackBlobUsesSeekableFileAndEnforcesSize(t *testing.T) {
	const contents = "bounded staged sitepack media"
	archive, entry := sitepackStagingZIPEntry(t, []byte(contents))
	_ = archive
	tempDir := t.TempDir()

	staged, err := stageSitepackBlob(tempDir, entry, int64(len(contents)))
	if err != nil {
		t.Fatalf("stage valid sitepack media: %v", err)
	}
	if _, ok := any(staged).(io.ReadSeeker); !ok {
		t.Fatal("staged media reader must be seekable for signed S3 requests")
	}
	position, err := staged.Seek(0, io.SeekCurrent)
	if err != nil || position != 0 {
		t.Fatalf("staged media must be rewound: position=%d err=%v", position, err)
	}
	got, err := io.ReadAll(staged)
	if err != nil || string(got) != contents {
		t.Fatalf("staged contents = %q, err=%v", got, err)
	}
	stagedPath := staged.Name()
	if err := staged.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(stagedPath); err != nil {
		t.Fatalf("remove staged media file: %v", err)
	}

	for _, test := range []struct {
		name string
		size int64
	}{
		{name: "smaller_than_entry", size: int64(len(contents) - 1)},
		{name: "larger_than_entry", size: int64(len(contents) + 1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			altered := *entry
			altered.UncompressedSize64 = uint64(test.size)
			altered.UncompressedSize = uint32(test.size)
			if staged, err := stageSitepackBlob(tempDir, &altered, test.size); err == nil {
				name := staged.Name()
				_ = staged.Close()
				_ = os.Remove(name)
				t.Fatal("stage accepted a size different from the uncompressed entry")
			}
			files, err := os.ReadDir(tempDir)
			if err != nil {
				t.Fatal(err)
			}
			if len(files) != 0 {
				t.Fatalf("failed staging left temporary files: %v", pathsInDirectory(files, tempDir))
			}
		})
	}
}

func sitepackStagingZIPEntry(t *testing.T, contents []byte) (*zip.Reader, *zip.File) {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	entry, err := writer.Create("media.bin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write(contents); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	reader, err := zip.NewReader(bytes.NewReader(buffer.Bytes()), int64(buffer.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if len(reader.File) != 1 {
		t.Fatalf("test ZIP has %d entries, want 1", len(reader.File))
	}
	return reader, reader.File[0]
}

func pathsInDirectory(files []os.DirEntry, dir string) []string {
	paths := make([]string, 0, len(files))
	for _, file := range files {
		paths = append(paths, filepath.Join(dir, file.Name()))
	}
	return paths
}
