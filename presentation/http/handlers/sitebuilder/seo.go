package sitebuilder

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"websiteengine/pkg/environment"
	publicruntime "websiteengine/pkg/runtime"
)

func (h *SnapshotsHandler) PageDocument(ctx context.Context, env environment.Environment, requestPath, rawQuery string, indexable bool) (publicruntime.Document, error) {
	query, err := url.ParseQuery(rawQuery)
	if err != nil {
		return publicruntime.SystemDocument(http.StatusBadRequest), nil
	}
	return h.service.PageDocument(ctx, env, requestPath, query, indexable)
}

func (h *SnapshotsHandler) Robots(indexable bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		index, err := h.service.PageIndex(r.Context(), environment.FromContext(r.Context()), indexable)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		if err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(publicRobotsRules(false, "")))
			return
		}
		_, _ = w.Write([]byte(publicRobotsRules(index.Indexable, index.CanonicalDomain)))
	}
}

func publicRobotsRules(indexable bool, canonicalDomain string) string {
	rules := "User-agent: *\nAllow: /\nDisallow: /admin/\nDisallow: /api/\n" +
		"Allow: /api/v2/runtime\n" +
		"Allow: /api/v2/content/*/records\n" +
		"Allow: /api/v2/site-themes/snapshots/\n" +
		"Allow: /api/v2/system-assets/\n"
	if indexable && canonicalDomain != "" {
		rules += "Sitemap: " + strings.TrimRight(canonicalDomain, "/") + "/sitemap.xml\n"
	}
	return rules
}

type sitemapURL struct {
	Location string `xml:"loc"`
}
