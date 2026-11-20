package media

import (
	"errors"
	"testing"
)

func testRegistry(t *testing.T) *Registry {
	t.Helper()
	registry, err := NewRegistry([]Preset{{Name: "image-web", Version: "2.1.0", InputContentTypes: []string{"image/jpeg", "image/png"}, OutputContentType: "image/webp", MaxInputBytes: 1024, MaxOutputBytes: 2048}})
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func TestPresetLookupIsExactAndDefensivelyCopied(t *testing.T) {
	inputTypes := []string{"image/jpeg"}
	registry, err := NewRegistry([]Preset{{Name: "image-web", Version: "2.1.0", InputContentTypes: inputTypes, OutputContentType: "image/webp", MaxInputBytes: 10, MaxOutputBytes: 20}})
	if err != nil {
		t.Fatal(err)
	}
	inputTypes[0] = "text/html"
	preset, ok := registry.Lookup("image-web", "2.1.0")
	if !ok || preset.InputContentTypes[0] != "image/jpeg" {
		t.Fatal("registry retained mutable caller data")
	}
	preset.InputContentTypes[0] = "text/html"
	again, _ := registry.Lookup("image-web", "2.1.0")
	if again.InputContentTypes[0] != "image/jpeg" {
		t.Fatal("lookup exposed registry storage")
	}
	if _, ok := registry.Lookup("image-web", "2.1.1"); ok {
		t.Fatal("non-configured version accepted")
	}
}

func TestRegistryRejectsAmbiguousOrUnsafePresets(t *testing.T) {
	cases := [][]Preset{
		{{Name: "image", Version: "1", InputContentTypes: []string{"image/*"}, OutputContentType: "image/webp", MaxInputBytes: 1, MaxOutputBytes: 1}},
		{{Name: "image", Version: "1", InputContentTypes: []string{"image/png"}, OutputContentType: "image/webp", MaxInputBytes: 1, MaxOutputBytes: 1}, {Name: "image", Version: "1", InputContentTypes: []string{"image/jpeg"}, OutputContentType: "image/webp", MaxInputBytes: 1, MaxOutputBytes: 1}},
		{{Name: "../bin", Version: "1", InputContentTypes: []string{"image/png"}, OutputContentType: "image/webp", MaxInputBytes: 1, MaxOutputBytes: 1}},
		{{Name: "image", Version: "1", InputContentTypes: []string{"image/"}, OutputContentType: "image/webp", MaxInputBytes: 1, MaxOutputBytes: 1}},
		{{Name: "image", Version: "1", InputContentTypes: []string{"text/html; charset=utf-8"}, OutputContentType: "image/webp", MaxInputBytes: 1, MaxOutputBytes: 1}},
		{{Name: "image", Version: "1", InputContentTypes: []string{"Image/PNG"}, OutputContentType: "image/webp", MaxInputBytes: 1, MaxOutputBytes: 1}},
	}
	for i, presets := range cases {
		if _, err := NewRegistry(presets); !errors.Is(err, ErrInvalidPreset) {
			t.Errorf("case %d: got %v", i, err)
		}
	}
}

func TestRequestAndSourceAllowlist(t *testing.T) {
	registry := testRegistry(t)
	request := Request{TaskID: "task-1", SourceFileID: "file-1", Profile: "image-web", ProfileVersion: "2.1.0", IdempotencyKey: "request-1"}
	preset, err := registry.ValidateRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateSource(preset, "image/jpeg", 1024); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		typ  string
		size int64
	}{{"text/html", 1}, {"image/png", 1025}, {"image/jpeg", -1}} {
		if err := ValidateSource(preset, tc.typ, tc.size); !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("(%q,%d): got %v", tc.typ, tc.size, err)
		}
	}
	request.ProfileVersion = "2.1.1"
	if _, err := registry.ValidateRequest(request); !errors.Is(err, ErrPresetUnavailable) {
		t.Fatalf("unconfigured version: %v", err)
	}
	request.ProfileVersion = "2.1.0"
	request.IdempotencyKey = "../bad"
	if _, err := registry.ValidateRequest(request); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("unsafe key: %v", err)
	}
}

func TestFingerprintDependsOnImmutableSourceAndExactPreset(t *testing.T) {
	checksum := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	req := Request{SourceFileID: "file-1", Profile: "image-web", ProfileVersion: "2.1.0"}
	first, err := Fingerprint(checksum, req)
	if err != nil {
		t.Fatal(err)
	}
	repeat, _ := Fingerprint(checksum, req)
	if first != repeat {
		t.Fatal("fingerprint is not deterministic")
	}
	req.ProfileVersion = "2.1.1"
	changed, _ := Fingerprint(checksum, req)
	if first == changed {
		t.Fatal("fingerprint ignored preset version")
	}
	if _, err := Fingerprint("invalid", req); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("invalid checksum: %v", err)
	}
}

func TestMediaPresetRejectsActiveContentAndMismatchedOutputFormat(t *testing.T) {
	for _, preset := range []Preset{
		{Name: "svg", Version: "1", InputContentTypes: []string{"image/svg+xml"}, OutputContentType: "image/webp", MaxInputBytes: 10, MaxOutputBytes: 10},
		{Name: "bad-output", Version: "1", InputContentTypes: []string{"image/png"}, OutputContentType: "text/html", MaxInputBytes: 10, MaxOutputBytes: 10},
		{Name: "bad-format", Version: "1", InputContentTypes: []string{"image/png"}, OutputContentType: "image/jpeg", OutputFormat: "webp", MaxInputBytes: 10, MaxOutputBytes: 10},
	} {
		if _, err := NewRegistry([]Preset{preset}); !errors.Is(err, ErrInvalidPreset) {
			t.Errorf("unsafe preset accepted (%s): %v", preset.Name, err)
		}
	}
}

func TestMediaFingerprintBindsSourceFileID(t *testing.T) {
	checksum := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	left := Request{SourceFileID: "file-a", Profile: "image-web", ProfileVersion: "2.1.0"}
	right := left
	right.SourceFileID = "file-b"
	first, err := Fingerprint(checksum, left)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Fingerprint(checksum, right)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("fingerprint ignored source file ID")
	}
}
func TestOutputMetadataIsBoundedAndMatchesPreset(t *testing.T) {
	preset, _ := testRegistry(t).Lookup("image-web", "2.1.0")
	result := Result{OutputFileID: "out-1", Checksum: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", ContentType: "image/webp", Size: 2048}
	if err := ValidateOutput(preset, result); err != nil {
		t.Fatal(err)
	}
	result.ContentType = "text/html"
	if err := ValidateOutput(preset, result); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("mismatched MIME accepted: %v", err)
	}
	result.ContentType, result.Size = "image/webp", 2049
	if err := ValidateOutput(preset, result); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("oversize output accepted: %v", err)
	}
	result.Size, result.OutputFileID = 2048, "../outside"
	if err := ValidateOutput(preset, result); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("invalid output file ID accepted: %v", err)
	}
}
