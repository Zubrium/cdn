package media_test

import (
	"encoding/json"
	"strings"
	"testing"

	mediaprofile "github.com/Zubrium/cdn/pkg/contracts/media"
)

func TestCanonicalMediaProfileIsStrictAndStable(t *testing.T) {
	raw := json.RawMessage(`{"accept":"raster-image","renditions":[{"name":"card","width":640,"height":480,"fit":"cover"},{"name":"full","width":1600,"height":1200,"fit":"contain"}]}`)
	profile, err := mediaprofile.ValidateProfile(raw)
	if err != nil {
		t.Fatalf("validate profile: %v", err)
	}
	if got := mediaprofile.Outputs(profile); strings.Join(got, ",") != "card,full" {
		t.Fatalf("unexpected outputs: %v", got)
	}
	first, err := mediaprofile.Digest(profile, "ffmpeg-7.1")
	if err != nil {
		t.Fatalf("digest: %v", err)
	}
	reordered := json.RawMessage(`{"renditions":[{"fit":"cover","height":480,"width":640,"name":"card"},{"fit":"contain","height":1200,"width":1600,"name":"full"}],"accept":"raster-image"}`)
	secondProfile, err := mediaprofile.ValidateProfile(reordered)
	if err != nil {
		t.Fatalf("validate reordered profile: %v", err)
	}
	second, err := mediaprofile.Digest(secondProfile, "ffmpeg-7.1")
	if err != nil || first != second {
		t.Fatalf("canonical digest is unstable: %q vs %q (%v)", first, second, err)
	}
	changed, err := mediaprofile.Digest(profile, "ffmpeg-8.0")
	if err != nil || changed == first {
		t.Fatalf("processor version must change digest: %q vs %q (%v)", first, changed, err)
	}
}

func TestCanonicalMediaProfileRejectsUnsafeAndIncompatibleDeclarations(t *testing.T) {
	cases := map[string]string{
		"unknown field":        `{"accept":"raster-image","renditions":[{"name":"card","width":1,"height":1,"fit":"cover"}],"filters":"sharpen"}`,
		"command":              `{"accept":"raster-image","command":"ffmpeg"}`,
		"bad fit":              `{"accept":"raster-image","renditions":[{"name":"card","width":1,"height":1,"fit":"stretch"}]}`,
		"duplicate name":       `{"accept":"raster-image","renditions":[{"name":"card","width":1,"height":1,"fit":"cover"},{"name":"card","width":2,"height":2,"fit":"cover"}]}`,
		"image renditions":     `{"accept":"image","renditions":[{"name":"card","width":1,"height":1,"fit":"cover"}]}`,
		"video without poster": `{"accept":"video","video":{"maxWidth":1,"maxHeight":1,"poster":"thumb"}}`,
		"file transform":       `{"accept":"file","renditions":[{"name":"card","width":1,"height":1,"fit":"cover"}]}`,
		"zero size":            `{"accept":"raster-image","renditions":[{"name":"card","width":0,"height":1,"fit":"cover"}]}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := mediaprofile.ValidateProfile(json.RawMessage(raw)); err == nil {
				t.Fatal("expected profile rejection")
			}
		})
	}
}
