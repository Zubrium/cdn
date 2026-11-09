package files_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"websiteengine/features/files"
)

func TestSafeMediaTypeClassification(t *testing.T) {
	safe := []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="8" height="8"><rect width="8" height="8" fill="#fff"/></svg>`)
	unsafe := []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)
	for _, tc := range []struct {
		name     string
		data     []byte
		allowSVG bool
		expected string
	}{
		{"logo.svg", safe, true, "image/svg+xml"},
		{"unsafe.svg", unsafe, true, "text/plain; charset=utf-8"},
		{"renamed.svg", []byte("arbitrary text"), true, "text/plain; charset=utf-8"},
		{"large.svg", safe, false, "text/plain; charset=utf-8"},
		{"font.woff2", []byte("wOF2fake"), false, "font/woff2"},
		{"font.woff2", []byte("not a font"), false, "text/plain; charset=utf-8"},
	} {
		var reader *bytes.Reader
		if tc.allowSVG {
			reader = bytes.NewReader(tc.data)
		}
		var result string
		if reader != nil {
			result = files.ClassifyContentType(tc.name, tc.data, reader)
		} else {
			result = files.ClassifyContentType(tc.name, tc.data, nil)
		}
		if result != tc.expected {
			t.Fatalf("%s: got %q want %q", tc.name, result, tc.expected)
		}
	}
}
func TestRealVitasilSVGAndFontClassification(t *testing.T) {
	root := os.Getenv("TEST_VITASIL_ASSETS_DIR")
	if root == "" {
		t.Skip("TEST_VITASIL_ASSETS_DIR not set")
	}
	for _, tc := range []struct{ path, expected string }{{"issit_logo.svg", "image/svg+xml"}, {"fonts/alumni-sans-sc-latin-wght-normal.woff2", "font/woff2"}} {
		path := filepath.Join(root, tc.path)
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		head := make([]byte, 512)
		n, err := f.Read(head)
		if err != nil {
			_ = f.Close()
			t.Fatal(err)
		}
		if _, err = f.Seek(0, 0); err != nil {
			_ = f.Close()
			t.Fatal(err)
		}
		result := files.ClassifyContentType(tc.path, head[:n], f)
		_ = f.Close()
		if result != tc.expected {
			t.Fatalf("%s: got %s want %s", tc.path, result, tc.expected)
		}
	}
}
