package media

import "testing"

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
