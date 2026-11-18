// Package s3mock provides an isolated S3-compatible HTTP endpoint for Core
// integration tests. It is imported only by test code.
package s3mock

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
)

const namespace = "http://s3.amazonaws.com/doc/2006-03-01/"

type Server struct {
	http *httptest.Server
	mu   sync.RWMutex
	data map[string][]byte
}

func New() *Server {
	s := &Server{data: make(map[string][]byte)}
	s.http = httptest.NewServer(s)
	return s
}

func (s *Server) URL() string { return s.http.URL }

func (s *Server) Close() { s.http.Close() }

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/xml")
	switch {
	case r.Method == http.MethodGet && r.URL.Query().Has("publicAccessBlock"):
		_, _ = fmt.Fprintf(w, `<PublicAccessBlockConfiguration xmlns=%q><BlockPublicAcls>true</BlockPublicAcls><IgnorePublicAcls>true</IgnorePublicAcls><BlockPublicPolicy>true</BlockPublicPolicy><RestrictPublicBuckets>true</RestrictPublicBuckets></PublicAccessBlockConfiguration>`, namespace)
		return
	case r.Method == http.MethodGet && r.URL.Query().Has("versioning"):
		_, _ = fmt.Fprintf(w, `<VersioningConfiguration xmlns=%q></VersioningConfiguration>`, namespace)
		return
	case r.Method == http.MethodGet && r.URL.Query().Has("acl"):
		_, _ = fmt.Fprintf(w, `<AccessControlPolicy xmlns=%q><Owner><ID>test-owner</ID></Owner><AccessControlList><Grant><Grantee><ID>test-owner</ID></Grantee><Permission>FULL_CONTROL</Permission></Grant></AccessControlList></AccessControlPolicy>`, namespace)
		return
	case r.Method == http.MethodGet && r.URL.Query().Has("policyStatus"):
		_, _ = fmt.Fprintf(w, `<PolicyStatus xmlns=%q><IsPublic>false</IsPublic></PolicyStatus>`, namespace)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/")
	separator := strings.IndexByte(path, '/')
	if separator < 1 || separator == len(path)-1 {
		http.Error(w, `<Error><Code>InvalidRequest</Code></Error>`, http.StatusBadRequest)
		return
	}
	key := path[separator+1:]
	switch r.Method {
	case http.MethodPut:
		body, err := io.ReadAll(io.LimitReader(r.Body, 32<<20+1))
		if err != nil {
			http.Error(w, `<Error><Code>InternalError</Code></Error>`, http.StatusInternalServerError)
			return
		}
		if len(body) > 32<<20 {
			http.Error(w, `<Error><Code>EntityTooLarge</Code></Error>`, http.StatusRequestEntityTooLarge)
			return
		}
		s.mu.Lock()
		s.data[key] = body
		s.mu.Unlock()
		digest := md5.Sum(body)
		w.Header().Set("ETag", `"`+hex.EncodeToString(digest[:])+`"`)
		w.WriteHeader(http.StatusOK)
	case http.MethodGet, http.MethodHead:
		s.mu.RLock()
		body, exists := s.data[key]
		body = append([]byte(nil), body...)
		s.mu.RUnlock()
		if !exists {
			http.Error(w, `<Error><Code>NoSuchKey</Code></Error>`, http.StatusNotFound)
			return
		}
		status := http.StatusOK
		if value := r.Header.Get("Range"); value != "" {
			start, end, ok := parseRange(value, int64(len(body)))
			if !ok {
				http.Error(w, `<Error><Code>InvalidRange</Code></Error>`, http.StatusRequestedRangeNotSatisfiable)
				return
			}
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(body)))
			body = body[start : end+1]
			status = http.StatusPartialContent
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		if status != http.StatusOK {
			w.WriteHeader(status)
		}
		if r.Method == http.MethodGet {
			_, _ = w.Write(body)
		}
	case http.MethodDelete:
		s.mu.Lock()
		delete(s.data, key)
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, `<Error><Code>NotImplemented</Code></Error>`, http.StatusNotImplemented)
	}
}

func parseRange(value string, size int64) (int, int, bool) {
	if size <= 0 || !strings.HasPrefix(value, "bytes=") {
		return 0, 0, false
	}
	parts := strings.Split(strings.TrimPrefix(value, "bytes="), "-")
	if len(parts) != 2 {
		return 0, 0, false
	}
	start, errStart := strconv.ParseInt(parts[0], 10, 64)
	end, errEnd := strconv.ParseInt(parts[1], 10, 64)
	if errStart != nil || errEnd != nil || start < 0 || end < start || end >= size {
		return 0, 0, false
	}
	return int(start), int(end), true
}
