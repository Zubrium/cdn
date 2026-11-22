package media_test

import (
	"encoding/json"
	"strings"
	"testing"

	mediaprofile "github.com/Zubrium/cdn/pkg/contracts/media"
)

func web019WorkRequest(t *testing.T) mediaprofile.WorkRequest {
	t.Helper()
	return mediaprofile.WorkRequest{
		Version:     mediaprofile.WorkContractVersion,
		JobID:       "00000000-0000-4000-8000-000000000001",
		SnapshotID:  "00000000-0000-4000-8000-000000000002",
		PlacementID: "hero-placement", FieldPath: "/hero/image",
		SourceFileID: "00000000-0000-4000-8000-000000000003",
		SourceSHA256: strings.Repeat("a", 64), ProcessorVersion: "ffmpeg-7.1",
		OutputName: "original", IdempotencyKey: "media-job-original-key-0001",
		InputDigest: strings.Repeat("c", 64),
		Profile:     mediaprofile.Profile{Accept: "file"},
	}
}

func TestWorkRequestDigestIsBoundToCanonicalProfile(t *testing.T) {
	request := web019WorkRequest(t)
	digest, err := mediaprofile.Digest(request.Profile, request.ProcessorVersion)
	if err != nil {
		t.Fatal(err)
	}
	request.ProfileDigest = digest
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := mediaprofile.ValidateWorkRequest(raw)
	if err != nil || decoded.OutputName != "original" {
		t.Fatalf("validate request: %+v, %v", decoded, err)
	}
	request.ProcessorVersion = "ffmpeg-8.0"
	raw, _ = json.Marshal(request)
	if _, err := mediaprofile.ValidateWorkRequest(raw); err == nil {
		t.Fatal("stale profile digest must be rejected")
	}
}

func TestWorkRequestRejectsUnknownAndUnsafeFields(t *testing.T) {
	base := web019WorkRequest(t)
	digest, _ := mediaprofile.Digest(base.Profile, base.ProcessorVersion)
	base.ProfileDigest = digest
	cases := map[string]func(map[string]any){
		"client filter": func(m map[string]any) { m["filter"] = "sharpen" },
		"processor":     func(m map[string]any) { m["processor_version"] = "custom" },
		"bad output":    func(m map[string]any) { m["output_name"] = "../escape" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			raw, _ := json.Marshal(base)
			var value map[string]any
			if err := json.Unmarshal(raw, &value); err != nil {
				t.Fatal(err)
			}
			mutate(value)
			payload, _ := json.Marshal(value)
			if _, err := mediaprofile.ValidateWorkRequest(payload); err == nil {
				t.Fatal("expected request rejection")
			}
		})
	}
}
