package sitebuilder

import (
	"encoding/xml"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSitemapURLSetsSplitByURLCountAndStayValidXML(t *testing.T) {
	urls := []string{"https://site.example/a", "https://site.example/b?x=1&y=2", "https://site.example/c", "https://site.example/d", "https://site.example/e"}
	documents, err := sitemapURLSets(urls, 2, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if len(documents) != 3 {
		t.Fatalf("got %d sitemap parts, want 3", len(documents))
	}
	var total int
	for index, document := range documents {
		if len(document) > 4096 {
			t.Fatalf("part %d exceeds byte limit: %d", index+1, len(document))
		}
		var decoded struct {
			URLs []struct {
				Location string `xml:"loc"`
			} `xml:"url"`
		}
		if err := xml.Unmarshal(document, &decoded); err != nil {
			t.Fatalf("part %d is not valid XML: %v\n%s", index+1, err, document)
		}
		if len(decoded.URLs) > 2 {
			t.Fatalf("part %d has %d URLs, limit is 2", index+1, len(decoded.URLs))
		}
		total += len(decoded.URLs)
	}
	if total != len(urls) {
		t.Fatalf("encoded %d of %d URLs", total, len(urls))
	}
}

func TestSitemapURLSetsSplitByEncodedByteSize(t *testing.T) {
	urls := []string{"https://site.example/" + strings.Repeat("a", 70), "https://site.example/" + strings.Repeat("b", 70)}
	documents, err := sitemapURLSets(urls, 10, 250)
	if err != nil {
		t.Fatal(err)
	}
	if len(documents) != 2 {
		t.Fatalf("got %d sitemap parts, want 2", len(documents))
	}
	for _, document := range documents {
		if len(document) > 250 {
			t.Fatalf("document exceeds byte limit: %d", len(document))
		}
	}
}

func TestSitemapURLSetsSplitAtProtocolURLLimit(t *testing.T) {
	urls := make([]string, maxSitemapURLs+1)
	for index := range urls {
		urls[index] = fmt.Sprintf("https://site.example/items/%d", index)
	}
	documents, err := sitemapURLSets(urls, maxSitemapURLs, maxSitemapSize)
	if err != nil {
		t.Fatal(err)
	}
	if len(documents) != 2 {
		t.Fatalf("got %d sitemap parts, want 2 at the protocol limit", len(documents))
	}
	var counts []int
	for _, document := range documents {
		var decoded struct {
			URLs []struct{} `xml:"url"`
		}
		if err := xml.Unmarshal(document, &decoded); err != nil {
			t.Fatal(err)
		}
		counts = append(counts, len(decoded.URLs))
	}
	if counts[0] != maxSitemapURLs || counts[1] != 1 {
		t.Fatalf("sitemap URL split = %v, want [%d 1]", counts, maxSitemapURLs)
	}
}

func TestSitemapIndexReferencesCanonicalPartURLs(t *testing.T) {
	body, err := encodeSitemapIndex("https://site.example/", 2, 50_000, 4096)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, part := range []string{"https://site.example/sitemaps/1.xml", "https://site.example/sitemaps/2.xml"} {
		if !strings.Contains(text, part) {
			t.Errorf("sitemap index lacks %s: %s", part, text)
		}
	}
}

func TestSitemapConditionalResponseUsesETagAndRevalidation(t *testing.T) {
	body := []byte("<?xml version=\"1.0\"?><urlset/>")
	first := httptest.NewRecorder()
	writeSitemapResponse(first, httptest.NewRequest(http.MethodGet, "/sitemap.xml", nil), body, true)
	if first.Code != http.StatusOK || first.Header().Get("ETag") == "" || first.Header().Get("Cache-Control") != "public, max-age=0, must-revalidate" {
		t.Fatalf("unexpected sitemap response: status=%d headers=%v", first.Code, first.Header())
	}
	second := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/sitemap.xml", nil)
	request.Header.Set("If-None-Match", `"stale", W/`+first.Header().Get("ETag"))
	writeSitemapResponse(second, request, body, true)
	if second.Code != http.StatusNotModified || second.Body.Len() != 0 {
		t.Fatalf("conditional request status/body = %d/%q", second.Code, second.Body.String())
	}
	noIndex := httptest.NewRecorder()
	writeSitemapResponse(noIndex, httptest.NewRequest(http.MethodGet, "/sitemap.xml", nil), body, false)
	if noIndex.Header().Get("Cache-Control") != "no-store" || noIndex.Header().Get("ETag") != "" {
		t.Fatalf("non-indexable sitemap should not be shared cached: %v", noIndex.Header())
	}
}

func TestSitemapETagMatchesWeakTagsListsAndWildcard(t *testing.T) {
	for _, header := range []string{`"tag"`, `W/"tag"`, `"other", W/"tag"`, `*`} {
		if !sitemapETagMatches(header, `"tag"`) {
			t.Errorf("expected %q to match", header)
		}
	}
	if sitemapETagMatches(`"other"`, `"tag"`) {
		t.Fatal("unrelated validator matched")
	}
}

func TestMissingSitemapPartIsNoindexAndUncacheable(t *testing.T) {
	recorder := httptest.NewRecorder()
	writeSitemapNotFound(recorder, httptest.NewRequest(http.MethodGet, "/sitemaps/999.xml", nil))
	if recorder.Code != http.StatusNotFound || recorder.Header().Get("Cache-Control") != "no-store" || recorder.Header().Get("X-Robots-Tag") != "noindex" {
		t.Fatalf("invalid sitemap part response = %d %v", recorder.Code, recorder.Header())
	}
}
