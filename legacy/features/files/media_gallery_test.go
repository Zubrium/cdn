package files_test

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"testing"

	"websiteengine/features/media"
)

func makeGalleryPNG(t *testing.T) []byte {
	t.Helper()
	var out bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: 30, G: 90, B: 150, A: 255})
	if err := png.Encode(&out, img); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// The admin media gallery still reads the shared media_objects table through
// the legacy media repository. Service-owned files must not appear there,
// while files uploaded through the new admin file service remain visible and
// filterable by the gallery.
func TestAdminMediaGalleryListsAdminOwnedFilesAndAppliesFilters(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	admin := f.service.AdminOwner()
	imageBytes := makeGalleryPNG(t)

	publicImage, err := f.service.UploadStream(ctx, admin, "product-public.png", int64(len(imageBytes)), "public", bytes.NewReader(imageBytes))
	if err != nil {
		t.Fatal(err)
	}
	privateImage, err := f.service.UploadStream(ctx, admin, "product-private.png", int64(len(imageBytes)), "private", bytes.NewReader(imageBytes))
	if err != nil {
		t.Fatal(err)
	}
	privateNote, err := f.service.UploadStream(ctx, admin, "editor-notes.txt", int64(len("private note")), "private", bytes.NewBufferString("private note"))
	if err != nil {
		t.Fatal(err)
	}
	serviceImage, err := f.service.UploadStream(ctx, f.a, "service-only.png", int64(len(imageBytes)), "public", bytes.NewReader(imageBytes))
	if err != nil {
		t.Fatal(err)
	}

	gallery := media.NewServiceWithProcessor(media.NewRepository(f.pool), f.store, 1024, nil)
	all, _, err := gallery.List(ctx, media.ListFilters{Status: "ready", Search: "product", Type: "image", Sort: "name"}, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("product image filter returned %d items, want 2: %+v", len(all), all)
	}
	seen := map[string]media.Media{}
	for _, item := range all {
		seen[item.ID.String()] = item
		if item.OwnerID != admin.ID {
			t.Fatalf("gallery returned non-admin owner %s for %s", item.OwnerID, item.ID)
		}
		if item.Status != "ready" || item.ContentType != "image/png" {
			t.Fatalf("gallery returned malformed file metadata: %+v", item)
		}
	}
	if _, ok := seen[publicImage.ID.String()]; !ok {
		t.Fatalf("admin public file missing from gallery: %s", publicImage.ID)
	}
	if _, ok := seen[privateImage.ID.String()]; !ok {
		t.Fatalf("admin private file missing from management gallery: %s", privateImage.ID)
	}
	if _, ok := seen[serviceImage.ID.String()]; ok {
		t.Fatalf("service-owned file leaked into admin gallery: %s", serviceImage.ID)
	}

	private, _, err := gallery.List(ctx, media.ListFilters{Status: "ready", Search: "editor-notes", Type: "pdf"}, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(private) != 0 {
		t.Fatalf("non-image type filter returned a text file: %+v", private)
	}
	byName, _, err := gallery.List(ctx, media.ListFilters{Status: "ready", Search: "editor-notes", Sort: "name"}, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(byName) != 1 || byName[0].ID != privateNote.ID {
		t.Fatalf("name search did not return the expected admin file: %+v", byName)
	}
	if byName[0].Visibility != "private" {
		t.Fatalf("private visibility was lost in the gallery response: %+v", byName[0])
	}
}
