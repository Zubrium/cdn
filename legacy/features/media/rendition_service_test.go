package media

import (
	"testing"

	"github.com/google/uuid"
)

func TestResponsiveOutputsSortAndDeduplicateActualDimensions(t *testing.T) {
	profile := FieldProfile{Renditions: []Rendition{
		{Name: "large", Width: 1200, Height: 900},
		{Name: "small", Width: 480, Height: 360},
		{Name: "duplicate-small", Width: 800, Height: 600},
	}}
	outputs := map[string]Output{
		"large":           {URL: "/large", Width: 480, Height: 360, MIMEType: "image/webp"},
		"small":           {URL: "/small", Width: 320, Height: 240, MIMEType: "image/webp"},
		"duplicate-small": {URL: "/duplicate-small", Width: 320, Height: 300, MIMEType: "image/webp"},
	}

	got := responsiveOutputs(profile, outputs)
	if len(got) != 2 {
		t.Fatalf("responsive outputs = %+v, want two unique intrinsic widths", got)
	}
	if got[0].Width != 320 || got[0].Height != 300 || got[0].URL != "/duplicate-small" {
		t.Fatalf("first candidate = %+v, want deterministic largest height at 320w", got[0])
	}
	if got[1].Width != 480 || got[1].URL != "/large" {
		t.Fatalf("second candidate = %+v, want largest available width", got[1])
	}
}

func TestRasterImageFallsBackToOriginalWhileRenditionIsUnavailable(t *testing.T) {
	id := uuid.MustParse("c9244f88-34df-448b-9fe0-fda7bca06cc3")
	profile := FieldProfile{
		Accept:     "raster-image",
		Renditions: []Rendition{{Name: "card", Width: 800, Height: 800, Fit: "cover"}},
	}
	wantURL := "/media/" + id.String()

	for name, result := range map[string]struct {
		record RenditionRecord
		err    error
	}{
		"missing":    {err: ErrNotFound},
		"pending":    {record: RenditionRecord{Status: "pending"}},
		"processing": {record: RenditionRecord{Status: "processing"}},
		"failed":     {record: RenditionRecord{Status: "failed"}},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := hydrateRasterMediaSource(id, profile, result.record, result.err)
			if err != nil {
				t.Fatal(err)
			}
			if got.ID != id.String() || got.Kind != "image" || got.OriginalURL != wantURL || got.Src != wantURL || got.SrcSet != "" || got.Variants != nil {
				t.Fatalf("fallback source = %+v, want original media source %s", got, wantURL)
			}
		})
	}
}

func TestRasterImageUsesReadyRenditionVariants(t *testing.T) {
	id := uuid.MustParse("29f3a478-a8d8-42d7-9fbb-80d1f445a730")
	profile := FieldProfile{
		Accept:     "raster-image",
		Renditions: []Rendition{{Name: "card", Width: 800, Height: 800, Fit: "cover"}},
	}
	record := RenditionRecord{
		MediaID: id, ProfileHash: "profile-hash", Status: "ready",
		Outputs: map[string]StoredOutput{
			"card": {MIMEType: "image/webp", Width: 800, Height: 800},
		},
	}

	got, err := hydrateRasterMediaSource(id, profile, record, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Src == got.OriginalURL || got.SrcSet == "" || got.Variants["card"].Width != 800 {
		t.Fatalf("ready rendition was not used: %+v", got)
	}
}
