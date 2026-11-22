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
