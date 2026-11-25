package runtime_test

import (
	"fmt"
	"testing"

	featureruntime "github.com/Zubrium/cdn/engines/website/features/runtime"
)

func web019Manifest(dataInputs string) []byte {
	return []byte(fmt.Sprintf(`{
		"formatVersion":2,"key":"hero","name":"Hero","version":"1.2.3",
		"engineApi":"v1","slots":["main"],"arguments":{},
		"dataInputs":%s,
		"runtime":{"abi":"static-lifecycle","entry":"assets/app.js","styles":[]}
	}`, dataInputs))
}

func TestResolveMediaProfileUsesOnlyPinnedManifestDeclaration(t *testing.T) {
	manifest := web019Manifest(`[{"key":"hero","kind":"record","fields":[{"path":"image","type":"media","profile":{"x-media":{"accept":"raster-image","renditions":[{"name":"card","width":640,"height":480,"fit":"cover"}]}}}]}]`)
	profile, err := featureruntime.ResolveMediaProfile(manifest, map[string]struct{}{"assets/app.js": {}}, "/hero/image")
	if err != nil {
		t.Fatalf("resolve pinned profile: %v", err)
	}
	if profile.Accept != "raster-image" || len(profile.Renditions) != 1 || profile.Renditions[0].Name != "card" {
		t.Fatalf("unexpected resolved profile: %+v", profile)
	}
	if _, err := featureruntime.ResolveMediaProfile(manifest, map[string]struct{}{"assets/app.js": {}}, "/hero/missing"); err == nil {
		t.Fatal("field without declaration must be rejected")
	}
}

func TestManifestValidationRejectsInvalidMediaDeclarations(t *testing.T) {
	cases := map[string]string{
		"x-media on non-media field": `[{"key":"hero","kind":"record","fields":[{"path":"image","type":"text","profile":{"x-media":{"accept":"file"}}}]}]`,
		"unknown profile property":   `[{"key":"hero","kind":"record","fields":[{"path":"image","type":"media","profile":{"x-media":{"accept":"file","preset":"huge"}}}]}]`,
		"processor override":         `[{"key":"hero","kind":"record","fields":[{"path":"image","type":"media","profile":{"x-media":{"accept":"file"},"processor":"custom"}}]}]`,
	}
	for name, inputs := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := featureruntime.ValidateManifest(web019Manifest(inputs), map[string]struct{}{"assets/app.js": {}}); err == nil {
				t.Fatal("expected manifest rejection")
			}
		})
	}
}

func TestManifestRejectsInvalidArgumentMediaProfile(t *testing.T) {
	manifest := []byte(`{
		"formatVersion":2,"key":"hero","name":"Hero","version":"1.2.3",
		"engineApi":"v1","slots":["main"],
		"arguments":{"type":"object","properties":{"image":{"type":"string","format":"media","x-media":{"accept":"raster-image","preset":"untrusted"}}}},
		"dataInputs":[],"runtime":{"abi":"static-lifecycle","entry":"assets/app.js","styles":[]}
	}`)
	if _, err := featureruntime.ValidateManifest(manifest, map[string]struct{}{"assets/app.js": {}}); err == nil {
		t.Fatal("argument media profile with an undeclared processing option must be rejected")
	}
}

func TestManifestValidatesNestedArgumentMediaProfiles(t *testing.T) {
	cases := []struct {
		name      string
		arguments string
		wantError bool
	}{
		{"valid nested profile", `{"type":"object","properties":{"items":{"type":"array","items":{"type":"object","properties":{"image":{"type":"string","format":"media","x-media":{"accept":"image"}}}}}}}`, false},
		{"profile on non-media", `{"type":"object","properties":{"name":{"type":"string","x-media":{"accept":"file"}}}}`, true},
		{"invalid nested profile", `{"type":"object","properties":{"image":{"type":"string","format":"media","x-media":{"accept":"image","processor":"external"}}}}`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			manifest := []byte(fmt.Sprintf(`{"formatVersion":2,"key":"hero","name":"Hero","version":"1.2.3","engineApi":"v1","slots":["main"],"arguments":%s,"dataInputs":[],"runtime":{"abi":"static-lifecycle","entry":"assets/app.js","styles":[]}}`, tc.arguments))
			_, err := featureruntime.ValidateManifest(manifest, map[string]struct{}{"assets/app.js": {}})
			if (err != nil) != tc.wantError {
				t.Fatalf("ValidateManifest error = %v, wantError %v", err, tc.wantError)
			}
		})
	}
}
