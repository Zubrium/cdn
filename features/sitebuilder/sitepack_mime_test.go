package sitebuilder

import (
	"archive/zip"
	"bytes"
	"testing"
)

func packTestEntry(t *testing.T, name string, data []byte) *zip.File {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	f, err := w.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	return r.File[0]
}
func TestSitepackDoesNotTrustAdvertisedInlineMIME(t *testing.T) {
	executable := packTestEntry(t, "test.bin", []byte("<script>alert('x')</script>"))
	if err := validatePackBlobMIME(executable, "photo.png", "image/png", 1024); err == nil {
		t.Fatal("script accepted as image")
	}
	if err := validatePackBlobMIME(executable, "script.js", "application/octet-stream", 1024); err != nil {
		t.Fatal(err)
	}
	svg := packTestEntry(t, "test.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`))
	if err := validatePackBlobMIME(svg, "test.svg", "image/svg+xml", 1024); err == nil {
		t.Fatal("active SVG accepted")
	}
	safe := packTestEntry(t, "safe.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1 1"><path d="M0 0"/></svg>`))
	if err := validatePackBlobMIME(safe, "safe.svg", "image/svg+xml", 1024); err != nil {
		t.Fatalf("safe SVG: %v", err)
	}
	if err := validatePackBlobMIME(safe, "safe.svg", "image/svg+xml", 4); err == nil {
		t.Fatal("SVG inspection limit ignored")
	}
}
