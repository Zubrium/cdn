package files_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"websiteengine/features/files"
	"websiteengine/pkg/config"
	"websiteengine/pkg/principal"
	"websiteengine/pkg/storage"
	s3store "websiteengine/pkg/storage/s3"
	hfiles "websiteengine/presentation/http/handlers/files"
)

type memoryStore struct {
	mu        sync.Mutex
	objects   map[string][]byte
	failFinal bool
}

func (m *memoryStore) Put(_ context.Context, key string, r io.Reader, _ int64, _ storage.PutOptions) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failFinal && strings.HasPrefix(key, "media/") {
		m.failFinal = false
		return errors.New("storage unavailable")
	}
	b, e := io.ReadAll(r)
	if e == nil {
		m.objects[key] = b
	}
	return e
}
func (m *memoryStore) Get(_ context.Context, key string) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.objects[key]
	if !ok {
		return nil, errors.New("not found")
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}
func (m *memoryStore) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.objects, key)
	return nil
}
func (m *memoryStore) PublicURL(string) (string, error) {
	return "", errors.New("public S3 URLs prohibited")
}
func (m *memoryStore) Ready(context.Context) error { return nil }
func checksum(b []byte) string                     { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

type fixture struct {
	service *files.Service
	pool    *pgxpool.Pool
	store   *memoryStore
	cfg     config.FilesConfig
	a, b    files.Owner
}

func setup(t *testing.T) *fixture {
	t.Helper()
	raw := os.Getenv("TEST_FILES_DATABASE_URL")
	if raw == "" {
		t.Skip("TEST_FILES_DATABASE_URL is not set")
	}
	ctx := context.Background()
	c, e := pgxpool.ParseConfig(raw)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.HasPrefix(c.ConnConfig.Database, "website_engine_files_media_qa_") {
		t.Fatal("file integration tests require a uniquely named disposable website_engine_files_media_qa_* database")
	}
	if endpoint := strings.TrimSpace(os.Getenv("TEST_FILES_S3_ENDPOINT")); endpoint != "" {
		parsed, err := url.Parse(endpoint)
		if err != nil || parsed.Hostname() == "" {
			t.Fatalf("invalid TEST_FILES_S3_ENDPOINT %q", endpoint)
		}
		host := parsed.Hostname()
		ip := net.ParseIP(host)
		if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			t.Fatalf("file integration tests require a loopback disposable S3 endpoint, got %q", host)
		}
		if !strings.HasPrefix(strings.TrimSpace(os.Getenv("TEST_FILES_S3_BUCKET")), "files-media-qa-") {
			t.Fatal("file integration tests require a uniquely named disposable files-media-qa-* bucket")
		}
	}
	admin, e := pgxpool.NewWithConfig(ctx, c)
	if e != nil {
		t.Fatal(e)
	}
	defer admin.Close()
	schema := "file_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, e = admin.Exec(ctx, `CREATE SCHEMA `+schema); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		pool, e := pgxpool.New(ctx, raw)
		if e == nil {
			defer pool.Close()
			_, _ = pool.Exec(context.Background(), `DROP SCHEMA `+schema+` CASCADE`)
		}
	})
	c.ConnConfig.RuntimeParams["search_path"] = schema
	pool, e := pgxpool.NewWithConfig(ctx, c)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(pool.Close)
	_, e = pool.Exec(ctx, `CREATE TABLE media_objects(id uuid PRIMARY KEY,object_key text UNIQUE NOT NULL,original_filename text NOT NULL,content_type text NOT NULL,size_bytes bigint NOT NULL,status text NOT NULL,variants jsonb NOT NULL DEFAULT '{}',uploaded_by uuid,created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now());CREATE TABLE media_renditions(media_id uuid REFERENCES media_objects(id) ON DELETE CASCADE,profile_hash text,status text,outputs jsonb);`)
	if e != nil {
		t.Fatal(e)
	}
	migration, e := os.ReadFile(filepath.Join("testdata", "file-schema.sql"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, string(migration)); e != nil {
		t.Fatal(e)
	}
	t.Setenv("FILES_TEST_KEY_A", "files-test-key-a-at-least-24-chars")
	t.Setenv("FILES_TEST_KEY_B", "files-test-key-b-at-least-24-chars")
	cfg := config.FilesConfig{MaxBytes: 1024, PartBytes: 4, UploadTTL: time.Minute, CleanupInterval: time.Second, ProcessingTimeout: time.Minute, OwnerQuotaBytes: 4096, MaxSessions: 5, ListLimit: 2, RateLimit: 1200, RateMaxClients: 100, RateWindow: time.Minute, LinkMaxTTL: time.Minute, PublicBaseURL: "http://example.test", TempDir: t.TempDir(), SigningKey: bytes.Repeat([]byte("s"), 32), Services: []config.FileServiceConfig{{Name: "service-a", Permissions: []string{"upload", "read", "list", "delete", "link"}, Keys: []config.FileSecretRef{{Env: "FILES_TEST_KEY_A"}}}, {Name: "service-b", Permissions: []string{"read", "list"}, Keys: []config.FileSecretRef{{Env: "FILES_TEST_KEY_B"}}}}}
	store := &memoryStore{objects: map[string][]byte{}}
	s, e := files.NewService(pool, store, cfg)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.InitOwners(ctx); e != nil {
		t.Fatal(e)
	}
	a, e := s.Authenticate(os.Getenv("FILES_TEST_KEY_A"), "upload")
	if e != nil {
		t.Fatal(e)
	}
	b, e := s.Authenticate(os.Getenv("FILES_TEST_KEY_B"), "read")
	if e != nil {
		t.Fatal(e)
	}
	return &fixture{s, pool, store, cfg, a, b}
}
func upload(t *testing.T, f *fixture, data []byte, visibility string) files.Upload {
	t.Helper()
	u, e := f.service.CreateUpload(context.Background(), f.a, files.UploadInput{Name: "example.sh", Size: int64(len(data)), SHA256: checksum(data), Visibility: visibility})
	if e != nil {
		t.Fatal(e)
	}
	for offset := int64(0); offset < int64(len(data)); offset += u.PartSize {
		end := min(int64(len(data)), offset+u.PartSize)
		part := data[offset:end]
		_, e = f.service.PutPart(context.Background(), f.a, u.ID, int(offset/u.PartSize)+1, int64(len(part)), checksum(part), bytes.NewReader(part))
		if e != nil {
			t.Fatal(e)
		}
	}
	return u
}
func TestResumableIsolationCommitRetryRestartAndQuota(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	data := []byte("hello world!")
	u := upload(t, f, data, "private")
	if _, e := f.service.UploadStatus(ctx, f.b, u.ID); !errors.Is(e, files.ErrNotFound) {
		t.Fatalf("foreign session: %v", e)
	}
	if _, e := f.service.PutPart(ctx, f.a, u.ID, 1, 4, checksum([]byte("xxxx")), strings.NewReader("xxxx")); !errors.Is(e, files.ErrConflict) {
		t.Fatalf("changed part accepted: %v", e)
	}
	if _, e := f.service.PutPart(ctx, f.a, u.ID, 1, 4, checksum(data[:4]), bytes.NewReader(data[:4])); e != nil {
		t.Fatalf("identical retry: %v", e)
	}
	f.store.failFinal = true
	if _, e := f.service.Commit(ctx, f.a, u.ID); e == nil {
		t.Fatal("storage failure accepted")
	}
	if e := f.service.Cleanup(ctx); e != nil {
		t.Fatal(e)
	}
	restarted, e := files.NewService(f.pool, f.store, f.cfg)
	if e != nil {
		t.Fatal(e)
	}
	if e = restarted.InitOwners(ctx); e != nil {
		t.Fatal(e)
	}
	owner, e := restarted.Authenticate(os.Getenv("FILES_TEST_KEY_A"), "read")
	if e != nil || owner.ID != f.a.ID {
		t.Fatalf("identity lost: %v", e)
	}
	statuses := make(chan error, 2)
	ids := make(chan uuid.UUID, 2)
	for i := 0; i < 2; i++ {
		go func() { file, e := restarted.Commit(ctx, owner, u.ID); statuses <- e; ids <- file.ID }()
	}
	for i := 0; i < 2; i++ {
		if e := <-statuses; e != nil {
			t.Fatal(e)
		}
		if id := <-ids; id != u.FileID {
			t.Fatal("idempotent commit changed identity")
		}
	}
	if _, e := restarted.Get(ctx, f.b, u.FileID); !errors.Is(e, files.ErrNotFound) {
		t.Fatalf("foreign metadata read: %v", e)
	}
	file, e := restarted.Get(ctx, owner, u.FileID)
	if e != nil || file.SHA256 != checksum(data) {
		t.Fatalf("metadata: %+v %v", file, e)
	}
	read, e := f.store.Get(ctx, file.ObjectKey)
	if e != nil {
		t.Fatal(e)
	}
	got, _ := io.ReadAll(read)
	_ = read.Close()
	if !bytes.Equal(got, data) {
		t.Fatal("bytes corrupted")
	}
	if _, e := restarted.CreateUpload(ctx, owner, files.UploadInput{Name: "too-big.zip", Size: 1025, SHA256: checksum(data), Visibility: "public"}); !errors.Is(e, files.ErrInvalid) {
		t.Fatal("oversize accepted")
	}
	if _, e := restarted.Authenticate(os.Getenv("FILES_TEST_KEY_B"), "upload"); !errors.Is(e, files.ErrForbidden) {
		t.Fatal("permission escalation")
	}
	renamed, e := restarted.RenameOwner(ctx, "service-a", "renamed")
	if e != nil || renamed.ID != owner.ID {
		t.Fatalf("rename changed ID: %v", e)
	}
	cfg := f.cfg
	cfg.Services[0].Name = "renamed"
	cfg.Services[0].Keys = []config.FileSecretRef{{Env: "FILES_TEST_ROTATED"}}
	t.Setenv("FILES_TEST_ROTATED", "rotated-files-service-key-over-24-chars")
	rotated, e := files.NewService(f.pool, f.store, cfg)
	if e != nil {
		t.Fatal(e)
	}
	if e = rotated.InitOwners(ctx); e != nil {
		t.Fatal(e)
	}
	if _, e = rotated.Authenticate(os.Getenv("FILES_TEST_KEY_A"), "read"); !errors.Is(e, files.ErrForbidden) {
		t.Fatal("revoked key accepted")
	}
	newOwner, e := rotated.Authenticate(os.Getenv("FILES_TEST_ROTATED"), "read")
	if e != nil || newOwner.ID != owner.ID {
		t.Fatal("rotation/rename lost identity")
	}
}
func TestUploadChecksumIncompleteAbortTTLAndCleanup(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	u, e := f.service.CreateUpload(ctx, f.a, files.UploadInput{Name: "file.zip", Size: 8, SHA256: checksum([]byte("abcdefgh")), Visibility: "public"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.service.Commit(ctx, f.a, u.ID); !errors.Is(e, files.ErrConflict) {
		t.Fatalf("incomplete: %v", e)
	}
	if _, e = f.service.PutPart(ctx, f.a, u.ID, 1, 4, checksum([]byte("abcd")), strings.NewReader("bad!")); !errors.Is(e, files.ErrInvalid) {
		t.Fatalf("bad hash: %v", e)
	}
	if _, e = f.service.PutPart(ctx, f.a, u.ID, 1, 4, checksum([]byte("abcd")), strings.NewReader("abcd")); e != nil {
		t.Fatal(e)
	}
	if _, e = f.pool.Exec(ctx, `UPDATE file_upload_sessions SET expires_at=now()-interval '1 second' WHERE id=$1`, u.ID); e != nil {
		t.Fatal(e)
	}
	if e = f.service.Cleanup(ctx); e != nil {
		t.Fatal(e)
	}
	status, e := f.service.UploadStatus(ctx, f.a, u.ID)
	if e != nil || status.State != "cancelled" {
		t.Fatalf("TTL: %v %v", status, e)
	}
	f.store.mu.Lock()
	count := len(f.store.objects)
	f.store.mu.Unlock()
	if count != 0 {
		t.Fatalf("orphan chunks: %d", count)
	}
	second := upload(t, f, []byte("abcdefgh"), "public")
	if e = f.service.Abort(ctx, f.a, second.ID); e != nil {
		t.Fatal(e)
	}
	if e = f.service.Abort(ctx, f.a, second.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = f.service.Commit(ctx, f.a, second.ID); !errors.Is(e, files.ErrConflict) {
		t.Fatal("aborted commit accepted")
	}
}
func TestFileHTTPPrivateSignedRangesAndDeletion(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	u := upload(t, f, []byte("abcdefgh"), "private")
	file, e := f.service.Commit(ctx, f.a, u.ID)
	if e != nil {
		t.Fatal(e)
	}
	mux := http.NewServeMux()
	h := hfiles.NewHandler(f.service, slog.New(slog.NewTextHandler(io.Discard, nil)), 1<<20)
	h.RegisterServiceRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()
	path := "/api/v2/files/objects/" + file.ID.String() + "/content"
	request := func(method, path, token, rangeHeader string) *http.Response {
		t.Helper()
		r, _ := http.NewRequest(method, server.URL+path, nil)
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		r.Header.Set("Range", rangeHeader)
		resp, e := http.DefaultClient.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		return resp
	}
	resp := request("GET", path, "", "")
	_ = resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatal("private anonymously readable")
	}
	resp = request("GET", path, os.Getenv("FILES_TEST_KEY_B"), "")
	_ = resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatal("foreign key reads bytes")
	}
	link, _, e := f.service.Link(ctx, f.a, file.ID, time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	signed := strings.TrimPrefix(link, f.cfg.PublicBaseURL)
	resp = request("GET", signed, "", "bytes=2-4")
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != 206 || string(body) != "cde" || resp.Header.Get("Content-Range") != "bytes 2-4/8" || resp.Header.Get("Cache-Control") != "private, no-store" {
		t.Fatalf("range: %d %q %v", resp.StatusCode, body, resp.Header)
	}
	if !strings.HasPrefix(resp.Header.Get("Content-Disposition"), "attachment") {
		t.Fatal("shell/text script rendered inline")
	}
	resp = request("HEAD", signed, "", "")
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != 200 || len(body) != 0 || resp.ContentLength != 8 {
		t.Fatal("HEAD incorrect")
	}
	resp = request("GET", signed, "", "bytes=100-")
	_ = resp.Body.Close()
	if resp.StatusCode != 416 {
		t.Fatal("invalid range accepted")
	}
	if _, _, e = f.service.Link(ctx, f.a, file.ID, time.Minute+time.Second); !errors.Is(e, files.ErrInvalid) {
		t.Fatal("TTL max bypass")
	}
	if e = f.service.Delete(ctx, f.a, file.ID, false); e != nil {
		t.Fatal(e)
	}
	resp = request("GET", signed, "", "")
	_ = resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatal("deleted link still works")
	}
	if e = f.service.Cleanup(ctx); e != nil {
		t.Fatal(e)
	}
}
func TestParseRangeAndDisposition(t *testing.T) {
	for _, tc := range []struct {
		raw        string
		start, end int64
	}{{"bytes=0-0", 0, 0}, {"bytes=3-", 3, 9}, {"bytes=-3", 7, 9}, {"bytes=4-999", 4, 9}} {
		a, b, p, e := files.ParseRange(tc.raw, 10)
		if e != nil || !p || a != tc.start || b != tc.end {
			t.Fatalf("%s: %d %d %v", tc.raw, a, b, e)
		}
	}
	for _, raw := range []string{"bytes=-0", "bytes=10-", "bytes=2-1", "bytes=0-1,3-4", "items=0-1"} {
		if _, _, _, e := files.ParseRange(raw, 10); e == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	for _, ct := range []string{"text/html", "text/javascript", "application/javascript", "image/svg+xml", "application/octet-stream", "text/x-shellscript"} {
		if files.InlineSafe(ct) {
			t.Fatalf("unsafe inline %s", ct)
		}
	}
}

func TestConditionalMatchParsesEntityTagList(t *testing.T) {
	r, err := http.NewRequest(http.MethodGet, "/", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, header, etag string
		want               bool
	}{
		{name: "single", header: `"tag"`, etag: `"tag"`, want: true},
		{name: "weak", header: `W/"tag"`, etag: `"tag"`, want: true},
		{name: "list after comma tag", header: `"first", W/"tag"`, etag: `"tag"`, want: true},
		{name: "opaque comma", header: `"tag,with,commas", "other"`, etag: `"tag,with,commas"`, want: true},
		{name: "wildcard", header: `*`, etag: `"tag"`, want: true},
		{name: "no match", header: `"other"`, etag: `"tag"`, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r.Header.Set("If-None-Match", tc.header)
			if got := files.ConditionalMatch(r, tc.etag); got != tc.want {
				t.Fatalf("ConditionalMatch(%q, %q) = %v, want %v", tc.header, tc.etag, got, tc.want)
			}
		})
	}
}

func TestHTTPConditionalRequestsRespectVisibilityAndCachePolicy(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	mux := http.NewServeMux()
	h := hfiles.NewHandler(f.service, slog.New(slog.NewTextHandler(io.Discard, nil)), 1<<20)
	h.RegisterServiceRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()

	request := func(method, path, token, match, rangeHeader, ifRange string) *http.Response {
		t.Helper()
		req, err := http.NewRequest(method, server.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if match != "" {
			req.Header.Set("If-None-Match", match)
		}
		if rangeHeader != "" {
			req.Header.Set("Range", rangeHeader)
		}
		if ifRange != "" {
			req.Header.Set("If-Range", ifRange)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	readBody := func(resp *http.Response) []byte {
		t.Helper()
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		return body
	}

	publicUpload := upload(t, f, []byte("public body"), "public")
	publicFile, err := f.service.Commit(ctx, f.a, publicUpload.ID)
	if err != nil {
		t.Fatal(err)
	}
	publicPath := "/api/v2/files/objects/" + publicFile.ID.String() + "/content"
	resp := request(http.MethodGet, publicPath, "", "", "", "")
	publicBody := readBody(resp)
	publicETag := resp.Header.Get("ETag")
	if resp.StatusCode != http.StatusOK || string(publicBody) != "public body" || publicETag != `"`+publicFile.SHA256+`"` || resp.Header.Get("Cache-Control") != "public, no-cache" {
		t.Fatalf("public response: status=%d body=%q etag=%q cache=%q", resp.StatusCode, publicBody, publicETag, resp.Header.Get("Cache-Control"))
	}

	resp = request(http.MethodGet, publicPath, "", `"unrelated", W/`+publicETag, "", "")
	body := readBody(resp)
	if resp.StatusCode != http.StatusNotModified || len(body) != 0 || resp.Header.Get("ETag") != publicETag || resp.Header.Get("Cache-Control") != "public, no-cache" {
		t.Fatalf("public conditional response: status=%d body=%q etag=%q cache=%q", resp.StatusCode, body, resp.Header.Get("ETag"), resp.Header.Get("Cache-Control"))
	}
	resp = request(http.MethodGet, publicPath, "", "*", "", "")
	if body = readBody(resp); resp.StatusCode != http.StatusNotModified || len(body) != 0 {
		t.Fatalf("wildcard conditional response: status=%d body=%q", resp.StatusCode, body)
	}
	resp = request(http.MethodGet, publicPath, "", "", "bytes=0-2", `"stale"`)
	if body = readBody(resp); resp.StatusCode != http.StatusOK || string(body) != "public body" || resp.Header.Get("Content-Range") != "" {
		t.Fatalf("stale If-Range must return the full body: status=%d body=%q range=%q", resp.StatusCode, body, resp.Header.Get("Content-Range"))
	}
	resp = request(http.MethodGet, publicPath, "", "", "bytes=0-2", publicETag)
	if body = readBody(resp); resp.StatusCode != http.StatusPartialContent || string(body) != "pub" || resp.Header.Get("Content-Range") != "bytes 0-2/11" {
		t.Fatalf("matching If-Range response: status=%d body=%q range=%q", resp.StatusCode, body, resp.Header.Get("Content-Range"))
	}

	privateUpload := upload(t, f, []byte("private body"), "private")
	privateFile, err := f.service.Commit(ctx, f.a, privateUpload.ID)
	if err != nil {
		t.Fatal(err)
	}
	privatePath := "/api/v2/files/objects/" + privateFile.ID.String() + "/content"
	resp = request(http.MethodGet, privatePath, os.Getenv("FILES_TEST_KEY_A"), "", "", "")
	_ = readBody(resp)
	privateETag := resp.Header.Get("ETag")
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Cache-Control") != "private, no-store" {
		t.Fatalf("private owner response: status=%d cache=%q", resp.StatusCode, resp.Header.Get("Cache-Control"))
	}
	resp = request(http.MethodGet, privatePath, "", privateETag, "", "")
	if body = readBody(resp); resp.StatusCode != http.StatusNotFound || len(body) == 0 || resp.Header.Get("ETag") != "" {
		t.Fatalf("private validator leaked object state: status=%d body=%q etag=%q", resp.StatusCode, body, resp.Header.Get("ETag"))
	}
	resp = request(http.MethodGet, privatePath, os.Getenv("FILES_TEST_KEY_A"), `W/`+privateETag, "", "")
	if body = readBody(resp); resp.StatusCode != http.StatusNotModified || len(body) != 0 || resp.Header.Get("Cache-Control") != "private, no-store" {
		t.Fatalf("authorized private conditional response: status=%d body=%q cache=%q", resp.StatusCode, body, resp.Header.Get("Cache-Control"))
	}

	if err := f.service.Delete(ctx, f.a, publicFile.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := f.service.Cleanup(ctx); err != nil {
		t.Fatal(err)
	}
	resp = request(http.MethodGet, publicPath, "", publicETag, "", "")
	if body = readBody(resp); resp.StatusCode != http.StatusNotFound || len(body) == 0 {
		t.Fatalf("deleted public file was conditionally served: status=%d body=%q", resp.StatusCode, body)
	}
}

func TestActualS3PrivateRangeAndObjectLifecycle(t *testing.T) {
	endpoint := os.Getenv("TEST_FILES_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("TEST_FILES_S3_ENDPOINT is not set")
	}
	ctx := context.Background()
	bucket := "engine-files-qa-" + strings.ToLower(uuid.NewString())
	cfg := config.S3Config{Endpoint: endpoint, Region: "us-east-1", Bucket: bucket, AccessKey: "filesqaadmin", SecretKey: "disposable-qa-only", ForcePathStyle: true}
	store, e := s3store.New(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	if e = store.EnsureBucket(ctx); e != nil {
		t.Fatal(e)
	}
	if e = store.RequirePrivate(ctx); e != nil {
		t.Fatal(e)
	}
	key := "object.sh"
	if e = store.Put(ctx, key, strings.NewReader("abcdefgh"), 8, storage.PutOptions{ContentType: "application/octet-stream"}); e != nil {
		t.Fatal(e)
	}
	defer func() { _ = store.Delete(ctx, key) }()
	reader, e := store.GetRange(ctx, key, 2, 4)
	if e != nil {
		t.Fatal(e)
	}
	body, _ := io.ReadAll(reader)
	_ = reader.Close()
	if string(body) != "cde" {
		t.Fatal("S3 range incorrect")
	}
	info, e := store.Stat(ctx, key)
	if e != nil || info.Size != 8 {
		t.Fatal("S3 stat incorrect")
	}
	resp, e := http.Get(fmt.Sprintf("%s/%s/%s", endpoint, bucket, key))
	if e != nil {
		t.Fatal(e)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatalf("anonymous bucket bypass: %d", resp.StatusCode)
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }
func zeroDigest(size int64) string {
	h := sha256.New()
	_, _ = io.Copy(h, io.LimitReader(zeroReader{}, size))
	return hex.EncodeToString(h.Sum(nil))
}

type virtualStore struct {
	mu           sync.Mutex
	sizes        map[string]int64
	bytesWritten int64
}

func (v *virtualStore) Put(_ context.Context, key string, r io.Reader, size int64, _ storage.PutOptions) error {
	n, e := io.Copy(io.Discard, r)
	if e != nil {
		return e
	}
	if n != size {
		return fmt.Errorf("bad size")
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.sizes[key] = n
	v.bytesWritten += n
	return nil
}
func (v *virtualStore) Get(_ context.Context, key string) (io.ReadCloser, error) {
	v.mu.Lock()
	n, ok := v.sizes[key]
	v.mu.Unlock()
	if !ok {
		return nil, errors.New("missing")
	}
	return io.NopCloser(io.LimitReader(zeroReader{}, n)), nil
}
func (v *virtualStore) Delete(_ context.Context, key string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	delete(v.sizes, key)
	return nil
}
func (v *virtualStore) PublicURL(string) (string, error) { return "", errors.New("private") }
func (v *virtualStore) Ready(context.Context) error      { return nil }
func (v *virtualStore) Compose(_ context.Context, key string, parts []string, _ storage.PutOptions) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	var total int64
	for _, part := range parts {
		n, ok := v.sizes[part]
		if !ok {
			return errors.New("missing part")
		}
		total += n
	}
	v.sizes[key] = total
	return nil
}
func (v *virtualStore) CleanupMultipart(context.Context, time.Time) error { return nil }

// This test exercises all five GiB through the real service checksum/part/
// commit paths while the store generates/discards bytes. It intentionally
// does not claim a five-GiB S3 storage or HTTP throughput measurement.
func TestFullFiveGiBStreamWithBoundedStore(t *testing.T) {
	if os.Getenv("TEST_FILES_LARGE") != "1" {
		t.Skip("TEST_FILES_LARGE=1 enables full five-GiB synthetic stream")
	}
	f := setup(t)
	cfg := f.cfg
	cfg.MaxBytes = 5 << 30
	cfg.PartBytes = 64 << 20
	cfg.OwnerQuotaBytes = 10 << 30
	store := &virtualStore{sizes: map[string]int64{}}
	s, e := files.NewService(f.pool, store, cfg)
	if e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	if e = s.InitOwners(ctx); e != nil {
		t.Fatal(e)
	}
	digest := zeroDigest(cfg.MaxBytes)
	partDigest := zeroDigest(cfg.PartBytes)
	u, e := s.CreateUpload(ctx, f.a, files.UploadInput{Name: "five-gib.bin", Size: cfg.MaxBytes, SHA256: digest, Visibility: "private"})
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = s.Abort(ctx, f.a, u.ID); _ = s.Delete(ctx, f.a, u.FileID, false); _ = s.Cleanup(ctx) }()
	for number := 1; int64(number) <= cfg.MaxBytes/cfg.PartBytes; number++ {
		_, e = s.PutPart(ctx, f.a, u.ID, number, cfg.PartBytes, partDigest, io.LimitReader(zeroReader{}, cfg.PartBytes))
		if e != nil {
			t.Fatal(e)
		}
	}
	result, e := s.Commit(ctx, f.a, u.ID)
	if e != nil {
		t.Fatal(e)
	}
	if result.Size != 5<<30 || result.SHA256 != digest {
		t.Fatal("five-GiB metadata mismatch")
	}
	if store.bytesWritten != 5<<30 {
		t.Fatalf("streamed %d bytes", store.bytesWritten)
	}
	if _, e = s.CreateUpload(ctx, f.a, files.UploadInput{Name: "over-limit.bin", Size: (5 << 30) + 1, SHA256: digest, Visibility: "private"}); !errors.Is(e, files.ErrInvalid) {
		t.Fatal("five-GiB limit bypass")
	}
	t.Logf("streamed %d bytes in %d parts through service; virtual store and at most one part temp file", result.Size, cfg.MaxBytes/cfg.PartBytes)
}
func TestActualS3ResumableCompositionAndTemporaryLinks(t *testing.T) {
	endpoint := os.Getenv("TEST_FILES_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("TEST_FILES_S3_ENDPOINT is not set")
	}
	f := setup(t)
	ctx := context.Background()
	bucket := "engine-files-qa-" + uuid.NewString()
	store, e := s3store.New(ctx, config.S3Config{Endpoint: endpoint, Region: "us-east-1", Bucket: bucket, AccessKey: "filesqaadmin", SecretKey: "disposable-qa-only", ForcePathStyle: true})
	if e != nil {
		t.Fatal(e)
	}
	if e = store.EnsureBucket(ctx); e != nil {
		t.Fatal(e)
	}
	if e = store.RequirePrivate(ctx); e != nil {
		t.Fatal(e)
	}
	cfg := f.cfg
	cfg.MaxBytes = 64 << 20
	cfg.OwnerQuotaBytes = 128 << 20
	cfg.PartBytes = 8 << 20
	s, e := files.NewService(f.pool, store, cfg)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.InitOwners(ctx); e != nil {
		t.Fatal(e)
	}
	size := int64(32 << 20)
	digest := zeroDigest(size)
	partDigest := zeroDigest(cfg.PartBytes)
	u, e := s.CreateUpload(ctx, f.a, files.UploadInput{Name: "video.bin", Size: size, SHA256: digest, Visibility: "private"})
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = s.Abort(ctx, f.a, u.ID); _ = s.Delete(ctx, f.a, u.FileID, false); _ = s.Cleanup(ctx) }()
	for number := 1; number <= 4; number++ {
		_, e = s.PutPart(ctx, f.a, u.ID, number, cfg.PartBytes, partDigest, io.LimitReader(zeroReader{}, cfg.PartBytes))
		if e != nil {
			t.Fatal(e)
		}
	}
	file, e := s.Commit(ctx, f.a, u.ID)
	if e != nil {
		t.Fatal(e)
	}
	info, e := store.Stat(ctx, file.ObjectKey)
	if e != nil || info.Size != size {
		t.Fatalf("composed object: %+v %v", info, e)
	}
	reader, e := store.GetRange(ctx, file.ObjectKey, size-1024, size-1)
	if e != nil {
		t.Fatal(e)
	}
	n, e := io.Copy(io.Discard, reader)
	_ = reader.Close()
	if e != nil || n != 1024 {
		t.Fatal("last-byte range incorrect")
	}
	if _, e = s.Commit(ctx, f.a, u.ID); e != nil {
		t.Fatal("composed commit retry failed", e)
	}
	if e = s.Cleanup(ctx); e != nil {
		t.Fatal(e)
	}
}

func TestActualS3ServiceHTTPAPIRoundTrip(t *testing.T) {
	endpoint := os.Getenv("TEST_FILES_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("TEST_FILES_S3_ENDPOINT is not set")
	}
	f := setup(t)
	ctx := context.Background()
	bucket := "engine-files-http-qa-" + uuid.NewString()
	store, err := s3store.New(ctx, config.S3Config{Endpoint: endpoint, Region: "us-east-1", Bucket: bucket, AccessKey: "filesqaadmin", SecretKey: "disposable-qa-only", ForcePathStyle: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureBucket(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.RequirePrivate(ctx); err != nil {
		t.Fatal(err)
	}
	cfg := f.cfg
	// The HTTP path must accept the whole file as the final part; the generic
	// fixture uses four-byte chunks for unit cases, which are too small here.
	cfg.PartBytes = 64
	s, err := files.NewService(f.pool, store, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.InitOwners(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Cleanup(context.Background()) })

	mux := http.NewServeMux()
	h := hfiles.NewHandler(s, slog.New(slog.NewTextHandler(io.Discard, nil)), 1<<20)
	h.RegisterServiceRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()
	request := func(method, path, token, rangeHeader, digest string, body []byte) *http.Response {
		t.Helper()
		var reader io.Reader
		if body != nil {
			reader = bytes.NewReader(body)
		}
		req, err := http.NewRequest(method, server.URL+path, reader)
		if err != nil {
			t.Fatal(err)
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if rangeHeader != "" {
			req.Header.Set("Range", rangeHeader)
		}
		if digest != "" {
			req.Header.Set("X-Content-SHA256", digest)
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	decode := func(resp *http.Response, destination any) {
		t.Helper()
		defer resp.Body.Close()
		if err := json.NewDecoder(resp.Body).Decode(destination); err != nil {
			t.Fatal(err)
		}
	}
	assertStatus := func(resp *http.Response, want int) {
		t.Helper()
		defer resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("status=%d want=%d", resp.StatusCode, want)
		}
	}

	data := []byte("#!/bin/sh\necho qa\n")
	uploadInput, err := json.Marshal(files.UploadInput{Name: "probe.sh", Size: int64(len(data)), SHA256: checksum(data), Visibility: "private"})
	if err != nil {
		t.Fatal(err)
	}
	resp := request(http.MethodPost, "/api/v2/files/uploads", os.Getenv("FILES_TEST_KEY_A"), "", "", uploadInput)
	if resp.StatusCode != http.StatusCreated {
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("create upload: status=%d body=%s", resp.StatusCode, body)
	}
	var upload files.Upload
	decode(resp, &upload)
	if upload.ID == uuid.Nil || upload.FileID == uuid.Nil || upload.PartSize != cfg.PartBytes {
		t.Fatalf("unexpected upload response: %+v", upload)
	}

	resp = request(http.MethodPut, fmt.Sprintf("/api/v2/files/uploads/%s/parts/1", upload.ID), os.Getenv("FILES_TEST_KEY_A"), "", checksum(data), data)
	assertStatus(resp, http.StatusOK)
	resp = request(http.MethodPost, "/api/v2/files/uploads/"+upload.ID.String()+"/commit", os.Getenv("FILES_TEST_KEY_A"), "", "", nil)
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("commit upload: status=%d body=%s", resp.StatusCode, body)
	}
	var uploaded files.File
	decode(resp, &uploaded)
	if uploaded.ID != upload.FileID || uploaded.Visibility != "private" || uploaded.SHA256 != checksum(data) {
		t.Fatalf("unexpected committed file: %+v", uploaded)
	}

	objectPath := "/api/v2/files/objects/" + uploaded.ID.String()
	resp = request(http.MethodGet, objectPath, os.Getenv("FILES_TEST_KEY_A"), "", "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("metadata read: %d", resp.StatusCode)
	}
	decode(resp, &uploaded)
	contentPath := objectPath + "/content"
	for _, tc := range []struct {
		name, token string
		want        int
	}{{"anonymous", "", http.StatusNotFound}, {"foreign owner", os.Getenv("FILES_TEST_KEY_B"), http.StatusNotFound}} {
		resp = request(http.MethodGet, contentPath, tc.token, "", "", nil)
		assertStatus(resp, tc.want)
	}
	resp = request(http.MethodGet, contentPath, os.Getenv("FILES_TEST_KEY_A"), "bytes=2-6", "", nil)
	body, err := io.ReadAll(resp.Body)
	ownerETag := resp.Header.Get("ETag")
	_ = resp.Body.Close()
	if err != nil || resp.StatusCode != http.StatusPartialContent || string(body) != string(data[2:7]) || resp.Header.Get("Content-Range") != fmt.Sprintf("bytes 2-6/%d", len(data)) || ownerETag != `"`+uploaded.SHA256+`"` || resp.Header.Get("Cache-Control") != "private, no-store" {
		t.Fatalf("owner range response: status=%d body=%q range=%q err=%v", resp.StatusCode, body, resp.Header.Get("Content-Range"), err)
	}
	conditionalRequest := func(path, token, match string) *http.Response {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, server.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		req.Header.Set("If-None-Match", match)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	resp = conditionalRequest(contentPath, os.Getenv("FILES_TEST_KEY_A"), `W/`+ownerETag)
	body, err = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil || resp.StatusCode != http.StatusNotModified || len(body) != 0 || resp.Header.Get("Cache-Control") != "private, no-store" {
		t.Fatalf("owner conditional response: status=%d body=%q cache=%q err=%v", resp.StatusCode, body, resp.Header.Get("Cache-Control"), err)
	}
	resp = conditionalRequest(contentPath, "", ownerETag)
	body, err = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil || resp.StatusCode != http.StatusNotFound || len(body) == 0 || resp.Header.Get("ETag") != "" {
		t.Fatalf("anonymous conditional request leaked private file: status=%d body=%q etag=%q err=%v", resp.StatusCode, body, resp.Header.Get("ETag"), err)
	}

	linkBody, err := json.Marshal(map[string]int64{"ttlSeconds": int64(f.cfg.LinkMaxTTL / time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	resp = request(http.MethodPost, objectPath+"/links", os.Getenv("FILES_TEST_KEY_A"), "", "", linkBody)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("issue signed link: %d", resp.StatusCode)
	}
	var link struct {
		URL string `json:"url"`
	}
	decode(resp, &link)
	signedPath := strings.TrimPrefix(link.URL, f.cfg.PublicBaseURL)
	if signedPath == link.URL || !strings.HasPrefix(signedPath, contentPath+"?") {
		t.Fatalf("unexpected signed URL path: %q", signedPath)
	}
	resp = request(http.MethodGet, signedPath, "", "", "", nil)
	body, err = io.ReadAll(resp.Body)
	signedETag := resp.Header.Get("ETag")
	_ = resp.Body.Close()
	if err != nil || resp.StatusCode != http.StatusOK || !bytes.Equal(body, data) {
		t.Fatalf("signed download: status=%d body=%q err=%v", resp.StatusCode, body, err)
	}
	resp = conditionalRequest(signedPath, "", signedETag)
	body, err = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil || resp.StatusCode != http.StatusNotModified || len(body) != 0 || resp.Header.Get("Cache-Control") != "private, no-store" {
		t.Fatalf("signed conditional response: status=%d body=%q cache=%q err=%v", resp.StatusCode, body, resp.Header.Get("Cache-Control"), err)
	}

	resp = request(http.MethodDelete, objectPath, os.Getenv("FILES_TEST_KEY_B"), "", "", nil)
	assertStatus(resp, http.StatusForbidden)
	resp = request(http.MethodDelete, objectPath+"?confirm=true", os.Getenv("FILES_TEST_KEY_A"), "", "", nil)
	assertStatus(resp, http.StatusNoContent)
	resp = request(http.MethodGet, signedPath, "", "", "", nil)
	assertStatus(resp, http.StatusNotFound)
	if err := s.Cleanup(ctx); err != nil {
		t.Fatal(err)
	}
}

// This opt-in test transfers the full 5 GiB maximum through the HTTP service
// and a disposable local S3-compatible store. It streams zero-filled parts,
// keeps at most one configured part in memory/on temporary disk, verifies the
// composed object and Range response, then removes the object and upload parts.
func TestActualS3HTTPFiveGiBTransfer(t *testing.T) {
	if os.Getenv("TEST_FILES_5GIB") != "1" {
		t.Skip("TEST_FILES_5GIB=1 enables the physical five-GiB HTTP/S3 transfer")
	}
	endpoint := strings.TrimRight(os.Getenv("TEST_FILES_S3_ENDPOINT"), "/")
	if endpoint != "http://127.0.0.1:39000" && endpoint != "http://localhost:39000" {
		t.Fatal("five-GiB test requires the dedicated disposable MinIO endpoint on port 39000")
	}
	f := setup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	bucket := "engine-files-five-gib-qa-" + uuid.NewString()
	store, err := s3store.New(ctx, config.S3Config{Endpoint: endpoint, Region: "us-east-1", Bucket: bucket, AccessKey: "filesqaadmin", SecretKey: "disposable-qa-only", ForcePathStyle: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureBucket(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.RequirePrivate(ctx); err != nil {
		t.Fatal(err)
	}
	cfg := f.cfg
	cfg.MaxBytes = 5 << 30
	cfg.OwnerQuotaBytes = 10 << 30
	cfg.PartBytes = 64 << 20
	s, err := files.NewService(f.pool, store, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.InitOwners(ctx); err != nil {
		t.Fatal(err)
	}
	var upload files.Upload
	defer func() {
		if upload.ID != uuid.Nil {
			if err := s.Abort(context.Background(), f.a, upload.ID); err != nil && !errors.Is(err, files.ErrConflict) && !errors.Is(err, files.ErrNotFound) {
				t.Errorf("abort leftover upload: %v", err)
			}
		}
		if upload.FileID != uuid.Nil {
			if err := s.Delete(context.Background(), f.a, upload.FileID, false); err != nil && !errors.Is(err, files.ErrNotFound) {
				t.Errorf("delete leftover test file: %v", err)
			}
		}
		if err := s.Cleanup(context.Background()); err != nil {
			t.Errorf("clean leftover test objects: %v", err)
		}
	}()

	mux := http.NewServeMux()
	h := hfiles.NewHandler(s, slog.New(slog.NewTextHandler(io.Discard, nil)), 1<<20)
	h.RegisterServiceRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()
	client := &http.Client{Timeout: 15 * time.Minute}
	newRequest := func(method, path string, body io.Reader, contentLength int64, digest, rangeHeader string) *http.Request {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, method, server.URL+path, body)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+os.Getenv("FILES_TEST_KEY_A"))
		if body != nil {
			req.ContentLength = contentLength
			req.Header.Set("Content-Type", "application/octet-stream")
		}
		if digest != "" {
			req.Header.Set("X-Content-SHA256", digest)
		}
		if rangeHeader != "" {
			req.Header.Set("Range", rangeHeader)
		}
		return req
	}
	const size int64 = 5 << 30
	digest := zeroDigest(size)
	createBody, err := json.Marshal(files.UploadInput{Name: "five-gib.bin", Size: size, SHA256: digest, Visibility: "private"})
	if err != nil {
		t.Fatal(err)
	}
	createRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/api/v2/files/uploads", bytes.NewReader(createBody))
	if err != nil {
		t.Fatal(err)
	}
	createRequest.Header.Set("Authorization", "Bearer "+os.Getenv("FILES_TEST_KEY_A"))
	createRequest.Header.Set("Content-Type", "application/json")
	createResponse, err := client.Do(createRequest)
	if err != nil {
		t.Fatal(err)
	}
	if createResponse.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(createResponse.Body)
		_ = createResponse.Body.Close()
		t.Fatalf("create five-GiB upload: status=%d body=%s", createResponse.StatusCode, body)
	}
	if err := json.NewDecoder(createResponse.Body).Decode(&upload); err != nil {
		_ = createResponse.Body.Close()
		t.Fatal(err)
	}
	_ = createResponse.Body.Close()
	if upload.Size != size || upload.PartSize != cfg.PartBytes || upload.ID == uuid.Nil || upload.FileID == uuid.Nil {
		t.Fatalf("unexpected upload limits: %+v", upload)
	}

	partDigest := zeroDigest(cfg.PartBytes)
	parts := int(size / cfg.PartBytes)
	started := time.Now()
	for number := 1; number <= parts; number++ {
		partRequest := newRequest(http.MethodPut, fmt.Sprintf("/api/v2/files/uploads/%s/parts/%d", upload.ID, number), io.LimitReader(zeroReader{}, cfg.PartBytes), cfg.PartBytes, partDigest, "")
		partResponse, err := client.Do(partRequest)
		if err != nil {
			t.Fatalf("upload part %d/%d: %v", number, parts, err)
		}
		if partResponse.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(partResponse.Body)
			_ = partResponse.Body.Close()
			t.Fatalf("upload part %d/%d: status=%d body=%s", number, parts, partResponse.StatusCode, body)
		}
		_, _ = io.Copy(io.Discard, partResponse.Body)
		_ = partResponse.Body.Close()
		if number%10 == 0 {
			t.Logf("uploaded %d/%d parts (%d MiB)", number, parts, int64(number)*cfg.PartBytes/(1<<20))
		}
	}

	commitRequest := newRequest(http.MethodPost, "/api/v2/files/uploads/"+upload.ID.String()+"/commit", nil, 0, "", "")
	commitResponse, err := client.Do(commitRequest)
	if err != nil {
		t.Fatal(err)
	}
	var uploaded files.File
	if commitResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(commitResponse.Body)
		_ = commitResponse.Body.Close()
		t.Fatalf("commit five-GiB upload: status=%d body=%s", commitResponse.StatusCode, body)
	}
	if err := json.NewDecoder(commitResponse.Body).Decode(&uploaded); err != nil {
		_ = commitResponse.Body.Close()
		t.Fatal(err)
	}
	_ = commitResponse.Body.Close()
	if uploaded.ID != upload.FileID || uploaded.Size != size || uploaded.SHA256 != digest || uploaded.Visibility != "private" {
		t.Fatalf("committed file metadata mismatch: %+v", uploaded)
	}
	objectKey := "media/" + uploaded.ID.String() + "/original"
	info, err := store.Stat(ctx, objectKey)
	if err != nil || info.Size != size {
		t.Fatalf("composed object size: %+v %v", info, err)
	}

	contentPath := "/api/v2/files/objects/" + uploaded.ID.String() + "/content"
	const rangeSize int64 = 32
	rangeRequest := newRequest(http.MethodGet, contentPath, nil, 0, "", fmt.Sprintf("bytes=%d-%d", size-rangeSize, size-1))
	rangeResponse, err := client.Do(rangeRequest)
	if err != nil {
		t.Fatal(err)
	}
	rangeBody, err := io.ReadAll(rangeResponse.Body)
	_ = rangeResponse.Body.Close()
	if err != nil || rangeResponse.StatusCode != http.StatusPartialContent || int64(len(rangeBody)) != rangeSize || !bytes.Equal(rangeBody, make([]byte, rangeSize)) {
		t.Fatalf("tail Range response: status=%d bytes=%d err=%v", rangeResponse.StatusCode, len(rangeBody), err)
	}

	downloadResponse, err := client.Do(newRequest(http.MethodGet, contentPath, nil, 0, "", ""))
	if err != nil {
		t.Fatal(err)
	}
	if downloadResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(downloadResponse.Body, 4096))
		_ = downloadResponse.Body.Close()
		t.Fatalf("download five-GiB object: status=%d body=%s", downloadResponse.StatusCode, body)
	}
	downloadHash := sha256.New()
	downloadedBytes, copyErr := io.Copy(downloadHash, downloadResponse.Body)
	closeErr := downloadResponse.Body.Close()
	if copyErr != nil || closeErr != nil || downloadedBytes != size || hex.EncodeToString(downloadHash.Sum(nil)) != digest {
		t.Fatalf("streamed five-GiB download mismatch: status=%d bytes=%d copyErr=%v closeErr=%v sha256=%s", downloadResponse.StatusCode, downloadedBytes, copyErr, closeErr, hex.EncodeToString(downloadHash.Sum(nil)))
	}

	referenceRequest := newRequest(http.MethodGet, "/api/v2/files/objects/"+uploaded.ID.String()+"/references", nil, 0, "", "")
	referenceResponse, err := client.Do(referenceRequest)
	if err != nil {
		t.Fatal(err)
	}
	var references struct {
		Items    []files.Reference `json:"items"`
		Revision string            `json:"revision"`
	}
	if referenceResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(referenceResponse.Body)
		_ = referenceResponse.Body.Close()
		t.Fatalf("read file references: status=%d body=%s", referenceResponse.StatusCode, body)
	}
	if err := json.NewDecoder(referenceResponse.Body).Decode(&references); err != nil {
		_ = referenceResponse.Body.Close()
		t.Fatal(err)
	}
	_ = referenceResponse.Body.Close()
	if len(references.Items) != 0 {
		t.Fatalf("isolated test object unexpectedly has references: %+v", references.Items)
	}
	deleteRequest := newRequest(http.MethodDelete, "/api/v2/files/objects/"+uploaded.ID.String()+"?confirm=true&referencesRevision="+references.Revision, nil, 0, "", "")
	deleteResponse, err := client.Do(deleteRequest)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, deleteResponse.Body)
	_ = deleteResponse.Body.Close()
	if deleteResponse.StatusCode != http.StatusNoContent {
		t.Fatalf("delete test object: status=%d", deleteResponse.StatusCode)
	}
	if err := s.Cleanup(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Stat(ctx, objectKey); err == nil {
		t.Fatal("5-GiB S3 object survived confirmed test cleanup")
	}
	for number := 1; number <= parts; number++ {
		partKey := fmt.Sprintf("uploads/%s/%d", upload.ID, number)
		if _, err := store.Stat(ctx, partKey); err == nil {
			t.Fatalf("temporary S3 part %d survived cleanup", number)
		}
	}
	t.Logf("transferred and verified 5 GiB over local HTTP/S3, then cleaned up in %s", time.Since(started).Round(time.Second))
}

func TestUploadedRealVitasilSVGAndWOFF2HaveUsableType(t *testing.T) {
	root := os.Getenv("TEST_VITASIL_ASSETS_DIR")
	if root == "" {
		t.Skip("TEST_VITASIL_ASSETS_DIR is not set")
	}
	f := setup(t)
	cfg := f.cfg
	cfg.MaxBytes = 2 << 20
	cfg.PartBytes = 2 << 20
	cfg.OwnerQuotaBytes = 10 << 20
	service, err := files.NewService(f.pool, f.store, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = service.InitOwners(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ path, expected string }{{"issit_logo.svg", "image/svg+xml"}, {"fonts/alumni-sans-sc-latin-wght-normal.woff2", "font/woff2"}} {
		data, err := os.ReadFile(filepath.Join(root, tc.path))
		if err != nil {
			t.Fatal(err)
		}
		obj, err := service.UploadStream(context.Background(), service.AdminOwner(), filepath.Base(tc.path), int64(len(data)), "public", bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		if obj.ContentType != tc.expected || obj.SHA256 != checksum(data) {
			t.Fatalf("%s type/hash %q/%s", tc.path, obj.ContentType, obj.SHA256)
		}
	}
}

func TestAdminFileDeleteRespectsRole(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	file, err := f.service.UploadStream(ctx, f.service.AdminOwner(), "file.txt", 8, "public", strings.NewReader("abcdefgh"))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	handler := hfiles.NewHandler(f.service, slog.New(slog.NewTextHandler(io.Discard, nil)), 1<<20)
	wrap := func(fn http.HandlerFunc) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			role := principal.Role(r.Header.Get("X-Test-Role"))
			fn(w, r.WithContext(principal.WithPrincipal(r.Context(), principal.Principal{Role: role})))
		})
	}
	handler.RegisterAdminRoutes(mux, wrap)
	request := func(role string) int {
		r := httptest.NewRequest(http.MethodDelete, "/api/v2/admin/files/objects/"+file.ID.String(), nil)
		r.Header.Set("X-Test-Role", role)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w.Code
	}
	if code := request("user"); code != http.StatusForbidden {
		t.Fatalf("user deleted admin-owned file: %d", code)
	}
	if code := request("admin"); code != http.StatusNoContent {
		t.Fatalf("admin delete failed: %d", code)
	}
}

func TestConcurrentDeletionEnforcesOwnership(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	upload := upload(t, f, []byte("parallel file"), "private")
	file, err := f.service.Commit(ctx, f.a, upload.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.service.Delete(ctx, f.b, file.ID, false); !errors.Is(err, files.ErrNotFound) {
		t.Fatalf("foreign owner deleted file: %v", err)
	}
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { results <- f.service.Delete(ctx, f.a, file.ID, false) }()
	}
	successes, missing := 0, 0
	for i := 0; i < 2; i++ {
		err := <-results
		switch {
		case err == nil:
			successes++
		case errors.Is(err, files.ErrNotFound):
			missing++
		default:
			t.Fatalf("unexpected deletion result: %v", err)
		}
	}
	if successes != 1 || missing != 1 {
		t.Fatalf("concurrent deletion: %d successes, %d missing", successes, missing)
	}
}

func TestLoadFilesRequiresOneServicesArray(t *testing.T) {
	for name, value := range map[string]string{
		"FILES_MAX_BYTES":         "1024",
		"FILES_PART_BYTES":        "256",
		"FILES_OWNER_QUOTA_BYTES": "2048",
		"FILES_MAX_SESSIONS":      "10",
		"FILES_LIST_LIMIT":        "10",
		"FILES_RATE_LIMIT":        "100",
		"FILES_RATE_MAX_CLIENTS":  "100",
		"FILES_UPLOAD_TTL":        "1h",
		"FILES_CLEANUP_INTERVAL":  "1m",
		"FILES_LINK_MAX_TTL":      "15m",
		"FILES_PROCESS_TIMEOUT":   "1m",
		"FILES_RATE_WINDOW":       "1m",
		"FILES_PUBLIC_BASE_URL":   "http://files.example.test",
		"FILES_TEMP_DIR":          t.TempDir(),
		"FILES_SIGNING_KEY_ENV":   "FILES_TEST_SIGNING_KEY",
		"FILES_TEST_SIGNING_KEY":  strings.Repeat("s", 32),
	} {
		t.Setenv(name, value)
	}

	for _, value := range []string{"null", "{}", "[] {}", "[] trailing"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("FILE_SERVICES", value)
			if _, err := config.LoadFiles(); err == nil {
				t.Fatalf("accepted FILE_SERVICES=%q", value)
			}
		})
	}

	t.Setenv("FILE_SERVICES", "[]")
	if _, err := config.LoadFiles(); err != nil {
		t.Fatalf("valid empty service list rejected: %v", err)
	}
}
