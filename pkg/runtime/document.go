package runtime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	htmltemplate "html/template"
	"io/fs"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
)

// Document separates route status and current content identity from its HTML.
type Document struct {
	HTML            []byte
	Status          int
	CacheKey        string
	SnapshotID      string
	ContentRevision int64
	Cacheable       bool
	SkipPrerender   bool
	ConnectOrigins  []string
}

type DocumentResolver func(context.Context, string, string) (Document, error)

// DocumentHandler retains static runtime assets while serving route-aware
// HTML. A worker can be omitted; the route status and metadata remain correct.
func DocumentHandler(mediaBase string, resolve DocumentResolver, worker *Prerenderer, assetCacheTTL time.Duration) http.Handler {
	assetCacheControl := runtimeAssetCacheControl(assetCacheTTL)
	sub, err := fs.Sub(embedded, "dist")
	if err != nil {
		panic(err)
	}
	assets := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		clean := path.Clean("/" + r.URL.Path)
		assetName := strings.TrimPrefix(clean, "/")
		if info, statErr := fs.Stat(sub, assetName); statErr == nil && !info.IsDir() {
			cacheControl := assetCacheControl
			if strings.HasPrefix(clean, "/assets/") {
				cacheControl = "public, max-age=31536000, immutable"
			}
			w.Header().Set("Cache-Control", cacheControl)
			assets.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(clean, "/assets/") {
			http.NotFound(w, r)
			return
		}
		resolverStartedAt := time.Now()
		document, err := resolve(r.Context(), clean, r.URL.RawQuery)
		if worker != nil {
			worker.logDocumentResolveDuration(r.Context(), time.Since(resolverStartedAt))
		}
		if err != nil {
			document = SystemDocument(http.StatusInternalServerError)
		}
		// Route matching takes precedence, including page URLs containing a
		// dot or a file-like suffix. If no route resolves, a request for a
		// common runtime asset still receives 404 even on an unconfigured site.
		if isRuntimeAssetPath(clean) && document.Status != http.StatusOK {
			http.NotFound(w, r)
			return
		}
		if worker != nil && document.SnapshotID != "" && !document.SkipPrerender && !worker.IsWorkerRequest(r) {
			html, renderErr := worker.Render(r.Context(), document, clean, r.URL.RawQuery)
			if renderErr == nil {
				document.HTML = html
				w.Header().Set("X-Engine-Prerender", "hit")
			} else {
				// The shell is only a temporary fallback: never let a failed
				// generation acquire validators or become cacheable at the edge.
				document.Cacheable = false
				w.Header().Set("X-Engine-Prerender", "unavailable")
			}
		}
		serveDocument(w, r, document, mediaBase)
	})
}

func runtimeAssetCacheControl(ttl time.Duration) string {
	if ttl < time.Second || ttl%time.Second != 0 {
		panic("runtime asset cache TTL must be a positive whole number of seconds")
	}
	return fmt.Sprintf("public, max-age=%d", int64(ttl/time.Second))
}

func mediaOrigin(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host
}

func isRuntimeAssetPath(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".js", ".mjs", ".css", ".map", ".woff", ".woff2":
		return true
	default:
		return false
	}
}

func serveDocument(w http.ResponseWriter, r *http.Request, document Document, mediaBase string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	connectSources := []string{"'self'"}
	seenOrigins := map[string]bool{}
	for _, origin := range document.ConnectOrigins {
		if safeConnectOrigin(origin) && !seenOrigins[origin] {
			seenOrigins[origin] = true
			connectSources = append(connectSources, origin)
		}
	}
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob: https: "+mediaOrigin(mediaBase)+"; media-src 'self' blob: https: "+mediaOrigin(mediaBase)+"; connect-src "+strings.Join(connectSources, " ")+"; font-src 'self' data:; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if document.Status == 0 {
		document.Status = http.StatusOK
	}
	if document.Cacheable && document.Status == http.StatusOK && document.CacheKey != "" {
		etag := documentETag(document.CacheKey, document.HTML)
		w.Header().Set("Cache-Control", "public, max-age=0, must-revalidate")
		w.Header().Set("ETag", etag)
		if (r.Method == http.MethodGet || r.Method == http.MethodHead) && ifNoneMatchMatches(r.Header.Values("If-None-Match"), etag) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	} else {
		w.Header().Set("Cache-Control", "no-store")
	}
	w.WriteHeader(document.Status)
	if r.Method != http.MethodHead {
		_, _ = w.Write(document.HTML)
	}
}

// safeConnectOrigin accepts only a bare HTTP(S) origin. Callers populate this
// allowlist from validated snapshot backend bindings; validating again here
// prevents a malformed document value from becoming a CSP directive.
func safeConnectOrigin(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
		return false
	}
	if strings.ContainsAny(u.Host, ";,\t\r\n '\"<>") || strings.HasSuffix(u.Host, ":") || u.Hostname() == "" {
		return false
	}
	if _, err := strconv.Atoi(u.Port()); u.Port() != "" && err != nil {
		return false
	}
	return raw == u.Scheme+"://"+u.Host
}

func documentETag(cacheKey string, representation []byte) string {
	hasher := sha256.New()
	_, _ = hasher.Write([]byte(cacheKey))
	_, _ = hasher.Write([]byte{0})
	_, _ = hasher.Write(representation)
	return `"` + hex.EncodeToString(hasher.Sum(nil)) + `"`
}

func ifNoneMatchMatches(headers []string, current string) bool {
	for _, header := range headers {
		for _, candidate := range strings.Split(header, ",") {
			candidate = strings.TrimSpace(candidate)
			if candidate == "*" {
				return true
			}
			if strings.HasPrefix(candidate, "W/") {
				candidate = strings.TrimPrefix(candidate, "W/")
			}
			if candidate == current {
				return true
			}
		}
	}
	return false
}

// SystemDocument is self-contained, readable without JavaScript and has no
// site branding. A failing user error feature cannot take away this fallback.
func SystemDocument(status int) Document {
	title, detail := "Сайт временно недоступен", "Попробуйте открыть страницу позже."
	switch status {
	case http.StatusNotFound:
		title, detail = "Страница не найдена", "Проверьте адрес или вернитесь на главную."
	case http.StatusForbidden:
		title, detail = "Доступ ограничен", "У вас нет доступа к этой странице."
	case http.StatusServiceUnavailable:
		title, detail = "Сайт ещё не настроен", "Для этой ветки пока не назначен снапшот сайта."
	}
	parsed, err := loadRuntimeTemplates()
	if err != nil {
		return Document{HTML: []byte("Service temporarily unavailable"), Status: status}
	}
	style, err := fs.ReadFile(styles, "styles/system.css")
	if err != nil || bytes.Contains(bytes.ToLower(style), []byte("</style")) {
		return Document{HTML: []byte("Service temporarily unavailable"), Status: status}
	}
	var rendered bytes.Buffer
	data := struct {
		Status int
		Title  string
		Detail string
		Style  htmltemplate.CSS
	}{Status: status, Title: title, Detail: detail, Style: htmltemplate.CSS(style)}
	if err := parsed.ExecuteTemplate(&rendered, "publicSystemDocument", data); err != nil {
		return Document{HTML: []byte("Service temporarily unavailable"), Status: status}
	}
	return Document{HTML: rendered.Bytes(), Status: status}
}
