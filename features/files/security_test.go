package files

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/png"
	"testing"
	"unicode/utf8"
)

func TestValidateOriginalRasterRejectsOversizedDimensions(t *testing.T) {
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	data := encoded.Bytes()
	binary.BigEndian.PutUint32(data[16:20], 40_000_000)
	binary.BigEndian.PutUint32(data[20:24], 2)
	binary.BigEndian.PutUint32(data[29:33], crc32.ChecksumIEEE(data[12:29]))
	if err := validateOriginalRaster("image/png", data); err == nil {
		t.Fatal("oversized public raster was accepted")
	}
}

func TestSanitizeNameRemovesHeaderControlsAndPathSegments(t *testing.T) {
	got := sanitizeName("folder\\subfolder/unsafe\r\n\u0085name\u007f.sh")
	if got != "unsafename.sh" {
		t.Fatalf("sanitizeName() = %q, want control-free basename %q", got, "unsafename.sh")
	}
	if !utf8.ValidString(got) {
		t.Fatalf("sanitizeName() returned invalid UTF-8: %q", got)
	}

	const internationalName = "résumé-страница.pdf"
	if got := sanitizeName(internationalName); got != internationalName {
		t.Fatalf("sanitizeName() changed a valid international filename: %q", got)
	}
}

func TestReferencesRevisionIsStableAcrossOrderingAndChangesWithReference(t *testing.T) {
	first := []Reference{
		{Kind: "content", ID: "entry-2", Description: "second"},
		{Kind: "snapshot", ID: "snapshot-1", Description: "homepage image"},
	}
	second := []Reference{first[1], first[0]}
	firstRevision := ReferencesRevision(first)
	if secondRevision := ReferencesRevision(second); firstRevision != secondRevision {
		t.Fatalf("revision depends on input order: %q != %q", firstRevision, secondRevision)
	}
	if first[0].ID != "entry-2" {
		t.Fatalf("revision calculation reordered caller-owned references: %+v", first)
	}

	second[0].Description = "different image"
	if changedRevision := ReferencesRevision(second); firstRevision == changedRevision {
		t.Fatalf("revision did not change when a reference changed: %q", firstRevision)
	}
}
