package sitebuilder

import "testing"

func TestSiteMetadataForPageUsesGlobalDefaultsAndOverrides(t *testing.T) {
	name := "Catalog"
	description := "Products"
	metadata := SiteMetadata{
		FormatVersion:  1,
		SiteName:       "VitaSil",
		Description:    "Quality feed",
		OGImageMediaID: "global-image",
		Pages: map[string]SiteMetadataOverride{
			"catalog": {SiteName: &name, Description: &description},
		},
	}

	global := metadata.ForPage("home")
	if global.Title != "VitaSil" || global.Description != "Quality feed" || global.OGTitle != "VitaSil" || global.OGImageURL != "/media/global-image" {
		t.Fatalf("unexpected global metadata: %#v", global)
	}
	page := metadata.ForPage("catalog")
	if page.Title != "Catalog" || page.Description != "Products" || page.OGTitle != "Catalog" || page.OGDescription != "Products" || page.OGImageURL != "/media/global-image" {
		t.Fatalf("unexpected page metadata: %#v", page)
	}
	metadata.OGTitle = "Explicit shared title"
	if metadata.ForPage("catalog").OGTitle != "Explicit shared title" {
		t.Fatal("explicit global OG title should be inherited")
	}
}
