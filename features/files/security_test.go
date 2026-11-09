package files

import (
	"testing"
	"unicode/utf8"
)

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
