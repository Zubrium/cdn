package sitebuilder

import (
	"strings"
	"testing"
)

func TestPublicRobotsRulesKeepPublicRuntimeCrawlableWithoutIndexingItsJSON(t *testing.T) {
	rules := publicRobotsRules(true, "https://site.example")
	for _, want := range []string{
		"Disallow: /admin/",
		"Disallow: /api/",
		"Allow: /api/v2/runtime",
		"Allow: /api/v2/content/*/records",
		"Allow: /api/v2/site-themes/snapshots/",
		"Allow: /api/v2/system-assets/",
		"Sitemap: https://site.example/sitemap.xml",
	} {
		if !strings.Contains(rules, want) {
			t.Errorf("robots rules do not contain %q:\n%s", want, rules)
		}
	}
}

func TestNonIndexableBranchCanExposeNoindexToCrawlers(t *testing.T) {
	rules := publicRobotsRules(false, "https://preview.example")
	if strings.Contains(rules, "Disallow: /\n") {
		t.Fatalf("non-indexable branch blocks its page-level noindex directive:\n%s", rules)
	}
	if strings.Contains(rules, "Sitemap:") {
		t.Fatalf("non-indexable branch advertises a sitemap:\n%s", rules)
	}
	if !strings.Contains(rules, "Allow: /\n") {
		t.Fatalf("non-indexable public branch must allow crawler to see noindex:\n%s", rules)
	}
}
