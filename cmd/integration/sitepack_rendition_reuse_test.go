package integration

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"image/color"
	"testing"

	featuremedia "websiteengine/features/media"
	"websiteengine/features/sitebuilder"
	"websiteengine/pkg/principal"
)

func TestSitepackReusedFileRestoresPackagedRenditions(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	actor := testActor(t, e)

	profile := featuremedia.FieldProfile{Accept: "raster-image", Renditions: []featuremedia.Rendition{{Name: "card", Width: 96, Height: 72, Fit: "cover"}}}
	manifest := testFeatureManifestFor("sitepack-rendition-reuse", "1.0.0")
	manifest["arguments"] = map[string]any{
		"type": "object",
		"properties": map[string]any{"image": map[string]any{
			"type": "string", "format": "media", "x-media": profile,
		}},
		"additionalProperties": false,
	}
	uploadFeatureBuild(t, e, manifest, "export default function Feature() { return null; }")

	original, err := e.media.Upload(ctx, principal.Principal{TokenID: actor}, "product.png", makeFileMediaPNG(t, color.RGBA{R: 20, G: 90, B: 140, A: 255}))
	if err != nil {
		t.Fatalf("upload synthetic public image: %v", err)
	}
	profileHash, err := featuremedia.ProfileHash(profile)
	if err != nil {
		t.Fatal(err)
	}
	renditionKey := "media/" + original.ID.String() + "/renditions/" + profileHash + "/card.webp"
	renditionBytes := sitepackCrossInstallWebP("card")
	e.store.objects[renditionKey] = renditionBytes
	e.store.types[renditionKey] = "image/webp"
	encodedProfile, _ := json.Marshal(profile)
	encodedOutputs, _ := json.Marshal(map[string]featuremedia.StoredOutput{
		"card": {ObjectKey: renditionKey, MIMEType: "image/webp", Width: 96, Height: 72},
	})
	if _, err := e.pool.Exec(ctx, `INSERT INTO media_renditions(media_id,profile_hash,profile,status,outputs) VALUES($1,$2,$3::jsonb,'ready',$4::jsonb)`, original.ID, profileHash, string(encodedProfile), string(encodedOutputs)); err != nil {
		t.Fatalf("seed ready media rendition: %v", err)
	}

	configMap := singlePageConfig("sitepack-rendition-reuse", "1.0.0")
	placement := configMap["pages"].([]any)[0].(map[string]any)["placements"].([]any)[0].(map[string]any)
	placement["arguments"] = map[string]any{"image": map[string]any{"source": "literal", "value": original.ID.String()}}
	rawConfig, _ := json.Marshal(configMap)
	config, err := sitebuilder.ParseSiteConfig(rawConfig)
	if err != nil {
		t.Fatalf("parse media feature snapshot config: %v", err)
	}
	snapshot, err := e.snapshots.CreateSnapshot(ctx, actor, sitebuilder.CreateSnapshotInput{Name: "Rendition export", Config: &config})
	if err != nil {
		t.Fatalf("create source snapshot: %v", err)
	}
	options := sitebuilder.SitepackOptions{TempDir: t.TempDir(), MaxBytes: 16 << 20, MaxExpandedBytes: 32 << 20, MaxEntries: 100, MaxIndexBytes: 1 << 20, MaxSVGBytes: 1 << 20, MaxBrowserBlobBytes: 1 << 20}
	var archive bytes.Buffer
	if err := e.snapshots.ExportSitepack(ctx, snapshot.ID, &archive, options); err != nil {
		t.Fatalf("export snapshot with image rendition: %v", err)
	}
	zipReader, err := zip.NewReader(bytes.NewReader(archive.Bytes()), int64(archive.Len()))
	if err != nil {
		t.Fatalf("open exported sitepack: %v", err)
	}
	if len(zipReader.File) < 1 {
		t.Fatal("sitepack export has no entries")
	}

	// Model the common cross-install case: target already has the same original
	// file, but has never processed this manifest's rendition profile.
	if _, err := e.pool.Exec(ctx, `DELETE FROM media_renditions WHERE media_id=$1`, original.ID); err != nil {
		t.Fatalf("remove disposable target rendition state: %v", err)
	}
	delete(e.store.objects, renditionKey)
	delete(e.store.types, renditionKey)
	imported, err := e.snapshots.ImportSitepack(ctx, actor, bytes.NewReader(archive.Bytes()), options)
	if err != nil {
		t.Fatalf("import sitepack with reused original: %v", err)
	}
	if imported.NewFiles != 0 || imported.ReusedFiles != 1 {
		t.Fatalf("sitepack should reuse original media: new=%d reused=%d", imported.NewFiles, imported.ReusedFiles)
	}

	var status string
	var outputsRaw []byte
	if err := e.pool.QueryRow(ctx, `SELECT status,outputs FROM media_renditions WHERE media_id=$1 AND profile_hash=$2`, original.ID, profileHash).Scan(&status, &outputsRaw); err != nil {
		t.Fatalf("read restored rendition row: %v", err)
	}
	var outputs map[string]featuremedia.StoredOutput
	if err := json.Unmarshal(outputsRaw, &outputs); err != nil {
		t.Fatal(err)
	}
	output, ok := outputs["card"]
	if status != "ready" || !ok || output.MIMEType != "image/webp" || output.Width != 96 || output.Height != 72 {
		t.Fatalf("sitepack did not restore ready rendition metadata: status=%q outputs=%+v", status, outputs)
	}
	if _, ok := e.store.objects[output.ObjectKey]; !ok {
		t.Fatalf("restored rendition object %q is missing from storage", output.ObjectKey)
	}
	if got := e.store.cacheControls[output.ObjectKey]; got != "public, max-age=31536000, immutable" {
		t.Fatalf("public imported rendition cache policy=%q", got)
	}
	var profileCount int
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM media_renditions WHERE media_id=$1`, original.ID).Scan(&profileCount); err != nil || profileCount != 1 {
		t.Fatalf("target should contain exactly the restored rendition profile: count=%d err=%v", profileCount, err)
	}
}
