package model

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestNormalizeHostCanonicalizesAndRejectsAmbiguousHosts(t *testing.T) {
	got, err := NormalizeHost("Store.Example.COM.")
	if err != nil || got != "store.example.com" {
		t.Fatalf("NormalizeHost() = %q, %v", got, err)
	}
	for _, value := range []string{"", " example.com", "*.example.com", "example.com:443", "user@example.com", "a..example.com", "example.com/path", "münich.example"} {
		if got, err := NormalizeHost(value); err == nil {
			t.Errorf("NormalizeHost(%q) = %q, want an error", value, got)
		}
	}
}

func TestNormalizeRequestHostValidatesPortAndCanonicalizesHost(t *testing.T) {
	for input, want := range map[string]string{
		"EXAMPLE.COM:443": "example.com",
		"example.com.":    "example.com",
	} {
		got, err := NormalizeRequestHost(input)
		if err != nil || got != want {
			t.Errorf("NormalizeRequestHost(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	for _, input := range []string{"example.com:0", "example.com:65536", "example.com:+80", "example.com:abc", "[::1]:80", "example.com:443:2"} {
		if got, err := NormalizeRequestHost(input); err == nil {
			t.Errorf("NormalizeRequestHost(%q) = %q, want an error", input, got)
		}
	}
}

func TestNormalizePathCanonicalizesSafeEscapesAndRejectsTraversal(t *testing.T) {
	for input, want := range map[string]string{
		"/assets/a%20b.pdf": "/assets/a%20b.pdf",
		"/café":             "/caf%C3%A9",
		"/a/%7euser":        "/a/~user",
	} {
		got, err := NormalizePath(input)
		if err != nil || got != want {
			t.Errorf("NormalizePath(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	for _, input := range []string{"", "relative", "/../secret", "/a/%2e%2e/secret", "/a%2fb", "/a%5Cb", "/a?download=1", "/a#fragment", "/a\\b", "/a%00b", "/%zz", "/a//b"} {
		if got, err := NormalizePath(input); err == nil {
			t.Errorf("NormalizePath(%q) = %q, want an error", input, got)
		}
	}
}

func TestPrefixCandidatesPreserveSlashBoundariesAndMatchWholeSegments(t *testing.T) {
	got, err := PathPrefixes("/assets/icons/logo.svg")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/", "/assets", "/assets/", "/assets/icons", "/assets/icons/", "/assets/icons/logo.svg"}
	if len(got) != len(want) {
		t.Fatalf("PathPrefixes() = %#v, want %#v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("PathPrefixes() = %#v, want %#v", got, want)
		}
	}
	if !PathMatches("/assets/", "/assets/icons/logo.svg", MatchPrefix) || PathMatches("/assets", "/assets-old/file", MatchPrefix) {
		t.Fatal("prefix matching must include slash suffixes and reject partial path segments")
	}
	if PathMatches("/assets", "/assets/", MatchExact) || !PathMatches("/assets/", "/assets/", MatchExact) {
		t.Fatal("exact matching must preserve trailing slash semantics")
	}
}

func TestPathSegmentLimitBoundsCoreSelectorValues(t *testing.T) {
	accepted := "/" + strings.TrimSuffix(strings.Repeat("x/", MaxPathSegments), "/")
	if _, err := NormalizePath(accepted); err != nil {
		t.Fatalf("path at %d segments rejected: %v", MaxPathSegments, err)
	}
	rejected := accepted + "/x"
	if _, err := NormalizePath(rejected); err == nil {
		t.Fatalf("path above %d segments was accepted", MaxPathSegments)
	}
	trailing := accepted + "/"
	prefixes, err := PathPrefixes(trailing)
	if err != nil || len(prefixes) > MaxRoutePathSelectorValues {
		t.Fatalf("trailing path candidates = %d, err=%v; limit=%d", len(prefixes), err, MaxRoutePathSelectorValues)
	}
}

func TestNewRouteRequiresExactCoreFilePathAndExplicitOperationConfig(t *testing.T) {
	now := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	coreFile := Target{Kind: TargetCoreFile, FileID: "01938f7a-68af-7c10-a51d-78b6ca125301"}
	if _, err := NewRoute("01938f7a-68af-7c10-a51d-78b6ca125302", "main", "EXAMPLE.COM", "/download.pdf", MatchPrefix, coreFile, now); err == nil {
		t.Fatal("core-file route accepted prefix matching")
	}
	if _, err := NewRoute("01938f7a-68af-7c10-a51d-78b6ca125303", "main", "example.com", "/forms", MatchPrefix,
		Target{Kind: TargetRegisteredOperation, OperationID: "public.form-session", OperationConfig: json.RawMessage(`{"enabled":true}`)}, now); err == nil {
		t.Fatal("registered operation accepted prefix matching")
	}
	if err := (Target{Kind: TargetCoreFile, FileID: coreFile.FileID, FallbackEnabled: true}).Validate(); err == nil {
		t.Fatal("core-file target accepted artifact fallback configuration")
	}
	if _, err := NewRoute("01938f7a-68af-7c10-a51d-78b6ca125302", "main", "EXAMPLE.COM", "/download.pdf", MatchExact, Target{Kind: TargetRegisteredOperation, OperationID: "public.form-upload"}, now); err == nil {
		t.Fatal("registered operation accepted empty config")
	}
	route, err := NewRoute("01938f7a-68af-7c10-a51d-78b6ca125302", "main", "EXAMPLE.COM", "/download.pdf", MatchExact, coreFile, now)
	if err != nil {
		t.Fatal(err)
	}
	if route.Host != "example.com" || route.Revision != 1 || route.State != StateActive || !route.CreatedAt.Equal(now) {
		t.Fatalf("unexpected normalized route: %+v", route)
	}
	operation, err := NewRoute("01938f7a-68af-7c10-a51d-78b6ca125303", "main", "example.com", "/upload", MatchExact,
		Target{Kind: TargetRegisteredOperation, OperationID: "public.form-upload", OperationConfig: json.RawMessage(`{"enabled":true}`)}, now)
	if err != nil || operation.Target.OperationID != "public.form-upload" {
		t.Fatalf("valid registered operation route rejected: route=%+v err=%v", operation, err)
	}
	artifactTarget, err := NewStaticArtifactTarget("en-US", []LocaleArtifactTarget{
		{Locale: "en-US", ArtifactManifestFileID: coreFile.FileID, Revision: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	artifactTarget.FallbackEnabled = true
	artifact, err := NewRoute("01938f7a-68af-7c10-a51d-78b6ca125304", "main", "example.com", "/site", MatchPrefix, artifactTarget, now)
	if err == nil || !errors.Is(err, ErrInvalidRoute) {
		t.Fatalf("static artifact route accepted explicit fallback: route=%+v err=%v", artifact, err)
	}
}

func TestNewRouteAssignsInitialRevisionToLocaleArtifactTargets(t *testing.T) {
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	target, err := NewStaticArtifactTarget("en-US", []LocaleArtifactTarget{
		{Locale: "en-US", ArtifactManifestFileID: "01938f7a-68af-7c10-a51d-78b6ca125302"},
	})
	if err != nil {
		t.Fatalf("NewStaticArtifactTarget(): %v", err)
	}
	route, err := NewRoute("01938f7a-68af-7c10-a51d-78b6ca125304", "main", "example.com", "/site", MatchPrefix, target, now)
	if err != nil {
		t.Fatalf("NewRoute(): %v", err)
	}
	if got := route.Target.LocaleTargets[0].Revision; got != 1 {
		t.Fatalf("initial locale target revision = %d, want 1", got)
	}
}
func TestCoreFileTargetDispositionIsOptionalAndStrictlyValidated(t *testing.T) {
	for _, disposition := range []string{"inline", "attachment"} {
		var target Target
		raw := `{"kind":"core-file","file_id":"01938f7a-68af-7c10-a51d-78b6ca125301","disposition":"` + disposition + `"}`
		if err := json.Unmarshal([]byte(raw), &target); err != nil {
			t.Fatalf("unmarshal %s target: %v", disposition, err)
		}
		if err := target.Validate(); err != nil {
			t.Errorf("core-file target rejected disposition %q: %v", disposition, err)
		}
	}

	var legacy Target
	if err := json.Unmarshal([]byte(`{"kind":"core-file","file_id":"01938f7a-68af-7c10-a51d-78b6ca125301"}`), &legacy); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Validate(); err != nil {
		t.Fatalf("legacy core-file target without disposition rejected: %v", err)
	}

	for name, raw := range map[string]string{
		"unknown value": `{"kind":"core-file","file_id":"01938f7a-68af-7c10-a51d-78b6ca125301","disposition":"download"}`,
		"artifact":      `{"kind":"static-site-artifact","default_locale":"en-US","locale_targets":[{"locale":"en-US","artifact_manifest_file_id":"01938f7a-68af-7c10-a51d-78b6ca125302"}],"disposition":"inline"}`,
	} {
		var target Target
		if err := json.Unmarshal([]byte(raw), &target); err != nil {
			t.Fatalf("%s: unmarshal: %v", name, err)
		}
		if err := target.Validate(); err == nil {
			t.Errorf("%s target accepted invalid disposition", name)
		}
	}
}
