// Package adminui serves the built Admin SPA without owning its API or state.
package adminui

import (
	"bytes"
	"errors"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"regexp"
	"strings"
	"time"
)

const mountPath = "/admin"

// Vite content hashes are 8 or more URL-safe characters between the last dash
// and the file extension. Only such immutable filenames get a shared TTL.
var hashedAssetName = regexp.MustCompile(`-[A-Za-z0-9_-]{8,}\.[A-Za-z0-9]+$`)

type Handler struct{ assets fs.FS }

func NewHandler(assets fs.FS) (*Handler, error) {
	if assets == nil {
		return nil, errors.New("Admin UI assets are required")
	}
	if _, err := fs.ReadFile(assets, "index.html"); err != nil {
		return nil, errors.New("Admin UI index.html is missing")
	}
	return &Handler{assets: assets}, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Path == mountPath {
		http.Redirect(w, r, mountPath+"/", http.StatusPermanentRedirect)
		return
	}
	if r.URL.Path != mountPath+"/" && !strings.HasPrefix(r.URL.Path, mountPath+"/") {
		http.NotFound(w, r)
		return
	}
	if strings.ContainsAny(r.URL.Path, "\\\x00") || strings.Contains(strings.ToLower(r.URL.EscapedPath()), "%2f") || strings.Contains(strings.ToLower(r.URL.EscapedPath()), "%5c") {
		http.NotFound(w, r)
		return
	}
	relative := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, mountPath), "/")
	if relative == "" {
		relative = "index.html"
	}
	clean := path.Clean(relative)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || !fs.ValidPath(clean) {
		http.NotFound(w, r)
		return
	}
	content, err := fs.ReadFile(h.assets, clean)
	if err != nil {
		if isNavigation(r) && path.Ext(clean) == "" {
			content, err = fs.ReadFile(h.assets, "index.html")
			clean = "index.html"
		}
		if err != nil {
			http.NotFound(w, r)
			return
		}
	}
	contentType := mime.TypeByExtension(path.Ext(clean))
	if contentType == "" {
		contentType = http.DetectContentType(content)
	}
	if strings.HasPrefix(contentType, "text/") && !strings.Contains(contentType, "charset=") {
		contentType += "; charset=utf-8"
	}
	w.Header().Set("Content-Type", contentType)
	if hashedAssetName.MatchString(path.Base(clean)) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	http.ServeContent(w, r, path.Base(clean), zeroModTime, bytes.NewReader(content))
}

func isNavigation(r *http.Request) bool {
	if r.Header.Get("Sec-Fetch-Dest") == "document" {
		return true
	}
	for _, value := range r.Header.Values("Accept") {
		for _, part := range strings.Split(value, ",") {
			if strings.TrimSpace(strings.SplitN(part, ";", 2)[0]) == "text/html" {
				return true
			}
		}
	}
	return false
}

// A fixed zero timestamp prevents build-machine metadata from leaking into
// headers while allowing the standard library to handle HEAD and Range.
var zeroModTime time.Time
