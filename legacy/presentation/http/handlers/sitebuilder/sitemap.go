package sitebuilder

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"websiteengine/pkg/environment"
)

const (
	maxSitemapURLs = 50_000
	maxSitemapSize = 50 * 1024 * 1024
	sitemapStart   = `<?xml version="1.0" encoding="UTF-8"?>` + "\n" + `<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">`
	sitemapEnd     = `</urlset>`
)

type sitemapEntry struct {
	XMLName  xml.Name `xml:"url"`
	Location string   `xml:"loc"`
}

type sitemapIndex struct {
	XMLName   xml.Name     `xml:"sitemapindex"`
	Namespace string       `xml:"xmlns,attr"`
	Sitemaps  []sitemapURL `xml:"sitemap"`
}

func (h *SnapshotsHandler) Sitemap(indexable bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		index, err := h.service.PageIndex(r.Context(), environment.FromContext(r.Context()), indexable)
		if err != nil {
			h.writeSitemapError(w, r, err)
			return
		}
		documents, err := sitemapURLSets(index.URLs, maxSitemapURLs, maxSitemapSize)
		if err != nil {
			h.writeSitemapError(w, r, err)
			return
		}
		if len(documents) == 1 {
			writeSitemapResponse(w, r, documents[0], index.Indexable && index.CanonicalDomain != "")
			return
		}
		body, err := encodeSitemapIndex(index.CanonicalDomain, len(documents), maxSitemapURLs, maxSitemapSize)
		if err != nil {
			h.writeSitemapError(w, r, err)
			return
		}
		writeSitemapResponse(w, r, body, index.Indexable && index.CanonicalDomain != "")
	}
}

func (h *SnapshotsHandler) SitemapPart(indexable bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		partName := r.PathValue("part")
		if !strings.HasSuffix(partName, ".xml") {
			writeSitemapNotFound(w, r)
			return
		}
		partValue := strings.TrimSuffix(partName, ".xml")
		partNumber, err := strconv.Atoi(partValue)
		if err != nil || partNumber < 1 || strconv.Itoa(partNumber) != partValue {
			writeSitemapNotFound(w, r)
			return
		}
		index, err := h.service.PageIndex(r.Context(), environment.FromContext(r.Context()), indexable)
		if err != nil {
			h.writeSitemapError(w, r, err)
			return
		}
		documents, err := sitemapURLSets(index.URLs, maxSitemapURLs, maxSitemapSize)
		if err != nil {
			h.writeSitemapError(w, r, err)
			return
		}
		if len(documents) <= 1 || partNumber > len(documents) {
			writeSitemapNotFound(w, r)
			return
		}
		writeSitemapResponse(w, r, documents[partNumber-1], index.Indexable && index.CanonicalDomain != "")
	}
}

func (h *SnapshotsHandler) writeSitemapError(w http.ResponseWriter, r *http.Request, err error) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex")
	h.writePublicError(w, r, err)
}

func writeSitemapNotFound(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex")
	http.NotFound(w, r)
}

func sitemapURLSets(urls []string, maxURLs, maxBytes int) ([][]byte, error) {
	if maxURLs < 1 || maxBytes <= len(sitemapStart)+len(sitemapEnd) {
		return nil, fmt.Errorf("invalid sitemap limits")
	}
	documents := make([][]byte, 0, 1)
	current := []byte(sitemapStart)
	count := 0
	flush := func() {
		document := make([]byte, 0, len(current)+len(sitemapEnd))
		document = append(document, current...)
		document = append(document, sitemapEnd...)
		documents = append(documents, document)
		current = []byte(sitemapStart)
		count = 0
	}
	for _, location := range urls {
		entry, err := xml.Marshal(sitemapEntry{Location: location})
		if err != nil {
			return nil, fmt.Errorf("encode sitemap URL: %w", err)
		}
		if count == maxURLs || len(current)+len(entry)+len(sitemapEnd) > maxBytes {
			if count == 0 {
				return nil, fmt.Errorf("sitemap URL exceeds the configured document size")
			}
			flush()
		}
		if len(current)+len(entry)+len(sitemapEnd) > maxBytes {
			return nil, fmt.Errorf("sitemap URL exceeds the configured document size")
		}
		current = append(current, entry...)
		count++
	}
	if count > 0 || len(documents) == 0 {
		flush()
	}
	return documents, nil
}

func encodeSitemapIndex(canonicalDomain string, partCount, maxURLs, maxBytes int) ([]byte, error) {
	if canonicalDomain == "" || partCount < 2 || partCount > maxURLs {
		return nil, fmt.Errorf("invalid sitemap index parameters")
	}
	canonicalDomain = strings.TrimRight(canonicalDomain, "/")
	index := sitemapIndex{Namespace: "http://www.sitemaps.org/schemas/sitemap/0.9", Sitemaps: make([]sitemapURL, 0, partCount)}
	for part := 1; part <= partCount; part++ {
		index.Sitemaps = append(index.Sitemaps, sitemapURL{Location: fmt.Sprintf("%s/sitemaps/%d.xml", canonicalDomain, part)})
	}
	body, err := xml.Marshal(index)
	if err != nil {
		return nil, err
	}
	document := append([]byte(xml.Header), body...)
	if len(document) > maxBytes {
		return nil, fmt.Errorf("sitemap index exceeds the configured document size")
	}
	return document, nil
}

func writeSitemapResponse(w http.ResponseWriter, r *http.Request, body []byte, cacheable bool) {
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if !cacheable {
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(body)
		return
	}
	hash := sha256.Sum256(body)
	etag := `"` + hex.EncodeToString(hash[:]) + `"`
	w.Header().Set("Cache-Control", "public, max-age=0, must-revalidate")
	w.Header().Set("ETag", etag)
	if sitemapETagMatches(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	_, _ = w.Write(body)
}

func sitemapETagMatches(header, etag string) bool {
	start := 0
	quoted := false
	for index := 0; index < len(header); index++ {
		switch header[index] {
		case '"':
			quoted = !quoted
		case ',':
			if !quoted {
				if matchesSitemapETag(header[start:index], etag) {
					return true
				}
				start = index + 1
			}
		}
	}
	return matchesSitemapETag(header[start:], etag)
}

func matchesSitemapETag(candidate, etag string) bool {
	candidate = strings.TrimSpace(candidate)
	if candidate == "*" {
		return true
	}
	candidate = strings.TrimSpace(strings.TrimPrefix(candidate, "W/"))
	return candidate == etag
}
