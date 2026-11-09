package middleware

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"strconv"
	"strings"
)

const gzipThreshold = 512

// CompressText applies gzip to sufficiently large textual responses on a
// public branch. It buffers only until it can choose a representation, then
// streams the rest. Range responses and non-text media remain byte-for-byte
// unchanged.
func CompressText(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cw := &compressWriter{dst: w, request: r, header: w.Header()}
		next.ServeHTTP(cw, r)
		cw.finish()
	})
}

type compressWriter struct {
	dst       http.ResponseWriter
	request   *http.Request
	header    http.Header
	status    int
	buffer    bytes.Buffer
	gzip      *gzip.Writer
	decided   bool
	committed bool
}

func (w *compressWriter) Header() http.Header { return w.header }

func (w *compressWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	if status >= 100 && status < 200 {
		w.dst.WriteHeader(status)
		return
	}
	w.status = status
}

func (w *compressWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if w.decided {
		if w.gzip != nil {
			return w.gzip.Write(data)
		}
		w.commit()
		return w.dst.Write(data)
	}
	_, _ = w.buffer.Write(data)
	if w.buffer.Len() >= gzipThreshold {
		w.decide()
	}
	return len(data), nil
}

func (w *compressWriter) Flush() {
	if !w.decided {
		w.decide()
	}
	if w.gzip != nil {
		_ = w.gzip.Flush()
	}
	w.commit()
	if flusher, ok := w.dst.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *compressWriter) Unwrap() http.ResponseWriter { return w.dst }

func (w *compressWriter) finish() {
	if !w.decided {
		w.decide()
	}
	if w.gzip != nil {
		_ = w.gzip.Close()
	}
	w.commit()
}

func (w *compressWriter) decide() {
	if w.decided {
		return
	}
	w.decided = true
	status := w.status
	if status == 0 {
		status = http.StatusOK
	}
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(w.header.Get("Content-Type"), ";")[0]))
	if contentType == "" && w.buffer.Len() > 0 {
		contentType = strings.ToLower(http.DetectContentType(w.buffer.Bytes()))
		w.header.Set("Content-Type", contentType)
	}
	if compressibleContentType(contentType) {
		mergeVary(w.header, "Accept-Encoding")
	}
	shouldCompress := status >= 200 && status != http.StatusNoContent && status != http.StatusPartialContent &&
		status != http.StatusNotModified && w.request.Method != http.MethodHead && w.request.Header.Get("Range") == "" &&
		w.buffer.Len() >= gzipThreshold && compressibleContentType(contentType) && acceptsGzip(w.request.Header.Get("Accept-Encoding"))
	headCompressible := w.request.Method == http.MethodHead && w.request.Header.Get("Range") == "" &&
		status >= 200 && status != http.StatusNoContent && status != http.StatusPartialContent && status != http.StatusNotModified &&
		compressibleContentType(contentType) && acceptsGzip(w.request.Header.Get("Accept-Encoding")) &&
		contentLengthAtLeast(w.header.Get("Content-Length"), gzipThreshold)
	if shouldCompress || headCompressible {
		w.header.Set("Content-Encoding", "gzip")
		w.header.Del("Content-Length")
		weakenETag(w.header)
		if shouldCompress {
			w.gzip = gzip.NewWriter(w.dst)
		}
	}
	w.commit()
	if w.buffer.Len() > 0 {
		if w.gzip != nil {
			_, _ = w.gzip.Write(w.buffer.Bytes())
		} else {
			_, _ = w.dst.Write(w.buffer.Bytes())
		}
		w.buffer.Reset()
	}
}

func (w *compressWriter) commit() {
	if w.committed {
		return
	}
	status := w.status
	if status == 0 {
		status = http.StatusOK
	}
	w.dst.WriteHeader(status)
	w.committed = true
}

func acceptsGzip(header string) bool {
	wildcard := false
	for _, item := range strings.Split(header, ",") {
		parts := strings.Split(strings.TrimSpace(item), ";")
		coding := strings.ToLower(strings.TrimSpace(parts[0]))
		quality := 1.0
		for _, parameter := range parts[1:] {
			key, value, ok := strings.Cut(strings.TrimSpace(parameter), "=")
			if !ok || !strings.EqualFold(strings.TrimSpace(key), "q") {
				continue
			}
			parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
			if err != nil || parsed < 0 || parsed > 1 {
				quality = 0
			} else {
				quality = parsed
			}
		}
		switch coding {
		case "gzip":
			return quality > 0
		case "*":
			wildcard = quality > 0
		}
	}
	return wildcard
}

func compressibleContentType(contentType string) bool {
	if strings.HasPrefix(contentType, "text/") {
		return true
	}
	switch contentType {
	case "application/javascript", "application/json", "application/manifest+json", "application/ld+json", "application/xml", "application/xhtml+xml", "image/svg+xml":
		return true
	default:
		return false
	}
}

func contentLengthAtLeast(raw string, threshold int) bool {
	length, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	return err == nil && length >= int64(threshold)
}

func mergeVary(header http.Header, token string) {
	for _, value := range header.Values("Vary") {
		for _, existing := range strings.Split(value, ",") {
			if strings.TrimSpace(existing) == "*" || strings.EqualFold(strings.TrimSpace(existing), token) {
				return
			}
		}
	}
	values := header.Values("Vary")
	values = append(values, token)
	header.Set("Vary", strings.Join(values, ", "))
}

func weakenETag(header http.Header) {
	etag := strings.TrimSpace(header.Get("ETag"))
	if len(etag) >= 2 && etag[0] == '"' && etag[len(etag)-1] == '"' {
		header.Set("ETag", "W/"+etag)
	}
}

var _ io.Writer = (*compressWriter)(nil)
