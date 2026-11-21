package runtime_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/Zubrium/cdn/engines/website/features/runtime"
)

func TestArtifactValidationRequiresCompleteDeterministicManifest(t *testing.T) {
	valid := artifactBytes(t, nil)
	manifest, err := runtime.ValidateArtifact(valid, 8, 1<<20)
	if err != nil || manifest.Format != "static-page-artifact/v1" || manifest.Document.FileID == "" || len(manifest.Assets) != 1 {
		t.Fatalf("valid artifact rejected: manifest=%+v err=%v", manifest, err)
	}

	cases := map[string]func(map[string]any){
		"missing document": func(p map[string]any) { delete(p, "document") },
		"invalid document type": func(p map[string]any) {
			p["document"].(map[string]any)["content_type"] = "text/plain; charset=utf-8"
		},
		"missing asset map": func(p map[string]any) { p["assets"] = nil },
		"illegal asset path": func(p map[string]any) {
			assets := p["assets"].(map[string]any)
			assets["../outside.js"] = assets["assets/app.js"]
			delete(assets, "assets/app.js")
		},
		"MIME mismatch": func(p map[string]any) {
			p["assets"].(map[string]any)["assets/app.js"].(map[string]any)["content_type"] = "text/html; charset=utf-8"
		},
		"unknown format": func(p map[string]any) { p["format"] = "static-page-artifact/v2" },
		"unknown cache class": func(p map[string]any) {
			p["assets"].(map[string]any)["assets/app.js"].(map[string]any)["cache_class"] = "forever"
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := runtime.ValidateArtifact(artifactBytes(t, mutate), 8, 1<<20); !errors.Is(err, runtime.ErrArtifact) {
				t.Fatalf("err=%v, want ErrArtifact", err)
			}
		})
	}

	tampered := strings.Replace(string(valid), strings.Repeat("a", 64), strings.Repeat("c", 64), 1)
	if _, err := runtime.ValidateArtifact([]byte(tampered), 8, 1<<20); !errors.Is(err, runtime.ErrArtifact) {
		t.Fatalf("tampered digest: err=%v", err)
	}
}

func TestArtifactFileMetadataIsVerifiedAgainstTrustedCoreState(t *testing.T) {
	manifest, err := runtime.ValidateArtifact(artifactBytes(t, nil), 8, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	observed := map[string]runtime.CoreFileMetadata{
		manifest.Document.FileID:               {ContentType: "text/html; charset=utf-8", SHA256: strings.Repeat("a", 64), Size: 120},
		"22222222-2222-4222-8222-222222222222": {ContentType: "text/javascript; charset=utf-8", SHA256: strings.Repeat("b", 64), Size: 64},
	}
	if err := runtime.VerifyArtifactFiles(manifest, observed); err != nil {
		t.Fatal(err)
	}
	observed["22222222-2222-4222-8222-222222222222"] = runtime.CoreFileMetadata{ContentType: "text/javascript; charset=utf-8", SHA256: strings.Repeat("d", 64), Size: 64}
	if err := runtime.VerifyArtifactFiles(manifest, observed); !errors.Is(err, runtime.ErrFileMeta) {
		t.Fatalf("checksum mismatch: err=%v", err)
	}
}

func web014ArtifactValidationIsFailClosed(t *testing.T) {
	TestArtifactValidationRequiresCompleteDeterministicManifest(t)
	TestArtifactFileMetadataIsVerifiedAgainstTrustedCoreState(t)
}

func web026ArtifactValidationIsFailClosed(t *testing.T) {
	TestArtifactValidationRequiresCompleteDeterministicManifest(t)
	TestArtifactFileMetadataIsVerifiedAgainstTrustedCoreState(t)
}
