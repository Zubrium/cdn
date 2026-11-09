package integration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"

	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"websiteengine/features/files"

	"websiteengine/pkg/config"

	s3store "websiteengine/pkg/storage/s3"
	httptransport "websiteengine/presentation/http"
	hfiles "websiteengine/presentation/http/handlers/files"
	hmedia "websiteengine/presentation/http/handlers/media"
)

// requireIsolatedFilesMediaDatabase protects newTestEnv's destructive TRUNCATE
// from accidentally reaching any database other than this disposable test DB.
func requireIsolatedFilesMediaDatabase(t *testing.T) {
	t.Helper()
	rawURL := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if rawURL == "" {
		t.Skip("TEST_DATABASE_URL is not set; file/media API acceptance requires a disposable database")
	}
	parsed, err := pgx.ParseConfig(rawURL)
	if err != nil {
		t.Fatalf("TEST_DATABASE_URL must identify a disposable local database: %v", err)
	}
	host := parsed.Host
	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		t.Fatalf("refusing non-local test database host %q", host)
	}
	if !strings.HasPrefix(parsed.Database, "website_engine_files_media_qa_") || strings.HasSuffix(parsed.Database, "website_engine_files_media_qa_") {
		t.Skipf("file/media acceptance requires its own unique website_engine_files_media_qa_* disposable database; current database is %q", parsed.Database)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, rawURL)
	if err != nil {
		t.Fatalf("connect to disposable test database: %v", err)
	}
	defer conn.Close(context.Background())
	var current string
	if err := conn.QueryRow(ctx, `SELECT current_database()`).Scan(&current); err != nil {
		t.Fatal(err)
	}
	if current != parsed.Database {
		t.Fatalf("database URL names %q but server connected to %q", parsed.Database, current)
	}
	var otherSessions int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid()`).Scan(&otherSessions); err != nil {
		t.Fatal(err)
	}
	if otherSessions != 0 {
		t.Fatalf("refusing database reset with %d other sessions", otherSessions)
	}
}

func requireDisposableFilesMediaStore(t *testing.T) {
	t.Helper()
	endpoint := strings.TrimSpace(os.Getenv("TEST_FILES_S3_ENDPOINT"))
	if endpoint == "" {
		t.Skip("TEST_FILES_S3_ENDPOINT is not set; file/media API acceptance requires disposable MinIO")
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "http" || parsed.Hostname() == "" {
		t.Fatalf("TEST_FILES_S3_ENDPOINT must point to a loopback disposable MinIO service: %v", err)
	}
	host := parsed.Hostname()
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		t.Fatalf("refusing non-local object storage endpoint %q", host)
	}
	if !strings.HasPrefix(strings.TrimSpace(os.Getenv("TEST_FILES_S3_BUCKET")), "files-media-qa-") {
		t.Fatal("TEST_FILES_S3_BUCKET must be a uniquely named files-media-qa-* bucket")
	}
	if os.Getenv("TEST_FILES_S3_ACCESS_KEY") == "" || os.Getenv("TEST_FILES_S3_SECRET_KEY") == "" {
		t.Fatal("disposable MinIO credentials are required")
	}
}

func fileMediaRawRequest(t *testing.T, server *httptest.Server, method, path, token string, body []byte, digest string) (int, []byte, http.Header) {
	t.Helper()
	request, err := http.NewRequest(method, server.URL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/octet-stream")
	request.Header.Set("X-Content-SHA256", digest)
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read %s %s response: %v", method, path, err)
	}
	return response.StatusCode, data, response.Header.Clone()
}

func TestFilesHTTPResumesPrivateUploadAfterAPIRestartWithS3(t *testing.T) {
	requireIsolatedFilesMediaDatabase(t)
	requireDisposableFilesMediaStore(t)
	env := newTestEnv(t)
	h := newFileMediaHarness(t, env)
	partSize := int(h.service.Config().PartBytes)
	data := make([]byte, partSize+137)
	for i := range data {
		data[i] = byte((i*31 + 17) % 251)
	}
	digestBytes := sha256.Sum256(data)
	digest := hex.EncodeToString(digestBytes[:])
	status, createdRaw, _ := fileMediaRequest(t, h.server, http.MethodPost, "/api/v2/admin/files/uploads", env.adminToken, map[string]any{
		"name": "process-restart-private.bin", "size": len(data), "sha256": digest, "visibility": "private",
	})
	created := decodeJSONMap(t, createdRaw)
	if status != http.StatusCreated {
		t.Fatalf("create resumable private upload: status=%d body=%s", status, createdRaw)
	}
	uploadID := created["id"].(string)
	fileID := created["fileId"].(string)
	partPath := "/api/v2/admin/files/uploads/" + uploadID + "/parts/1"
	status, _, _ = fileMediaRawRequest(t, h.server, http.MethodPut, partPath, env.adminToken, data[:partSize], hex.EncodeToString(sha256Bytes(data[:partSize])))
	if status != http.StatusOK {
		t.Fatalf("store first part before restart: status=%d", status)
	}
	serverAddress := h.server.Listener.Addr().String()
	h.server.Close()
	http.DefaultClient.CloseIdleConnections()

	fileConfig := h.service.Config()
	store, err := s3store.New(context.Background(), config.S3Config{
		Endpoint: strings.TrimRight(os.Getenv("TEST_FILES_S3_ENDPOINT"), "/"), Region: "us-east-1",
		Bucket: os.Getenv("TEST_FILES_S3_BUCKET"), AccessKey: os.Getenv("TEST_FILES_S3_ACCESS_KEY"),
		SecretKey: os.Getenv("TEST_FILES_S3_SECRET_KEY"), PublicBaseURL: fileConfig.PublicBaseURL, ForcePathStyle: true,
	})
	if err != nil {
		t.Fatalf("reinitialize S3 client after process restart: %v", err)
	}
	if err := store.RequirePrivate(context.Background()); err != nil {
		t.Fatalf("restarted process lost the private-bucket policy: %v", err)
	}
	restarted, err := files.NewService(env.pool, store, fileConfig)
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.InitOwners(context.Background()); err != nil {
		t.Fatal(err)
	}
	restarted.SetReferenceManager(env.snapshots)
	t.Cleanup(func() { _ = restarted.Cleanup(context.Background()) })
	fileHandler := hfiles.NewHandler(restarted, env.routerDeps.Log, 1<<20)
	mediaHandler := hmedia.NewHandler(env.media, env.routerDeps.Log, 20<<20)
	if err := mediaHandler.SetRedirectCacheTTL(time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := mediaHandler.SetPagination(20, 50); err != nil {
		t.Fatal(err)
	}
	mediaHandler.SetProfileProvider(env.snapshots)
	mediaHandler.SetFileHandler(fileHandler)
	deps := env.routerDeps
	deps.Files = fileHandler
	deps.Media = mediaHandler
	listener, err := net.Listen("tcp", serverAddress)
	if err != nil {
		t.Fatalf("rebind API address after restart: %v", err)
	}
	server := httptest.NewUnstartedServer(httptransport.NewRouter(deps))
	server.Listener = listener
	server.Start()
	defer server.Close()

	status, statusRaw, _ := fileMediaRequest(t, server, http.MethodGet, "/api/v2/admin/files/uploads/"+uploadID, env.adminToken, nil)
	resumeState := decodeJSONMap(t, statusRaw)
	if status != http.StatusOK || resumeState["state"] != "uploading" {
		t.Fatalf("upload session did not survive restart: status=%d body=%v", status, resumeState)
	}
	status, _, _ = fileMediaRawRequest(t, server, http.MethodPut, "/api/v2/admin/files/uploads/"+uploadID+"/parts/2", env.adminToken, data[partSize:], hex.EncodeToString(sha256Bytes(data[partSize:])))
	if status != http.StatusOK {
		t.Fatalf("upload final part after restart: status=%d", status)
	}
	status, committedRaw, _ := fileMediaRequest(t, server, http.MethodPost, "/api/v2/admin/files/uploads/"+uploadID+"/commit", env.adminToken, nil)
	committed := decodeJSONMap(t, committedRaw)
	if status != http.StatusOK || committed["id"] != fileID || committed["sha256"] != digest {
		t.Fatalf("commit after restart mismatch: status=%d body=%v", status, committed)
	}
	status, fetched, headers := fileMediaRequest(t, server, http.MethodGet, "/api/v2/admin/files/objects/"+fileID+"/content", env.adminToken, nil)
	if status != http.StatusOK || !bytes.Equal(fetched, data) || headers.Get("Cache-Control") != "private, no-store" {
		t.Fatalf("private file is not readable through authenticated API after restart: status=%d bytes=%d cache=%q", status, len(fetched), headers.Get("Cache-Control"))
	}
	status, _, _ = fileMediaRequest(t, server, http.MethodGet, "/media/"+fileID, "", nil)
	if status != http.StatusNotFound {
		t.Fatalf("private file became publicly accessible after restart: status=%d", status)
	}
	status, linkRaw, _ := fileMediaRequest(t, server, http.MethodPost, "/api/v2/admin/files/objects/"+fileID+"/links", env.adminToken, map[string]any{"ttlSeconds": 60})
	link := decodeJSONMap(t, linkRaw)
	if status != http.StatusCreated {
		t.Fatalf("issue limited link after restart: status=%d body=%v", status, link)
	}
	linkResponse, err := http.Get(link["url"].(string))
	if err != nil {
		t.Fatal(err)
	}
	linkBytes, err := io.ReadAll(linkResponse.Body)
	linkResponse.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if linkResponse.StatusCode != http.StatusOK || !bytes.Equal(linkBytes, data) || linkResponse.Header.Get("Cache-Control") != "private, no-store" {
		t.Fatalf("signed private URL failed after restart: status=%d bytes=%d cache=%q", linkResponse.StatusCode, len(linkBytes), linkResponse.Header.Get("Cache-Control"))
	}
}

func sha256Bytes(data []byte) []byte {
	sum := sha256.Sum256(data)
	return sum[:]
}

// Deletion must invalidate both branches after a reference was inherited by
// an independent child, and a confirmation made before the fork is stale.
