package integration

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"websiteengine/features/files"
	"websiteengine/features/sitebuilder"
	"websiteengine/pkg/config"
	"websiteengine/pkg/storage/s3"
	httptransport "websiteengine/presentation/http"
	hfiles "websiteengine/presentation/http/handlers/files"
	hmedia "websiteengine/presentation/http/handlers/media"
)

type fileMediaHarness struct {
	service *files.Service
	server  *httptest.Server
	store   *s3.Store
}

func requireUniqueFileMediaEnvironment(t *testing.T) {
	t.Helper()
	rawURL := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	parsed, err := url.Parse(rawURL)
	if rawURL == "" || err != nil || !strings.HasPrefix(strings.TrimPrefix(parsed.Path, "/"), "website_engine_files_media_qa_") {
		t.Skip("set TEST_DATABASE_URL to a uniquely named website_engine_files_media_qa_* database; integration setup truncates its database")
	}
	bucket := strings.TrimSpace(os.Getenv("TEST_FILES_S3_BUCKET"))
	if !strings.HasPrefix(bucket, "files-media-qa-") {
		t.Skip("set TEST_FILES_S3_BUCKET to a unique files-media-qa-* bucket in a disposable MinIO instance")
	}
}

func newFileMediaHarness(t *testing.T, env *testEnv) *fileMediaHarness {
	t.Helper()
	endpoint := strings.TrimRight(os.Getenv("TEST_FILES_S3_ENDPOINT"), "/")
	bucket := strings.TrimSpace(os.Getenv("TEST_FILES_S3_BUCKET"))
	accessKey := os.Getenv("TEST_FILES_S3_ACCESS_KEY")
	secretKey := os.Getenv("TEST_FILES_S3_SECRET_KEY")
	if endpoint == "" || bucket == "" || accessKey == "" || secretKey == "" {
		t.Skip("set TEST_FILES_S3_ENDPOINT, TEST_FILES_S3_BUCKET, TEST_FILES_S3_ACCESS_KEY, and TEST_FILES_S3_SECRET_KEY for isolated file-media acceptance")
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve isolated file API port: %v", err)
	}
	baseURL := "http://" + listener.Addr().String()
	t.Setenv("FILE_MEDIA_SERVICE_A_KEY", "file-media-service-a-test-key-2026")
	t.Setenv("FILE_MEDIA_SERVICE_B_KEY", "file-media-service-b-test-key-2026")
	store, err := s3.New(context.Background(), config.S3Config{
		Endpoint: endpoint, Region: "us-east-1", Bucket: bucket,
		AccessKey: accessKey, SecretKey: secretKey, PublicBaseURL: endpoint, ForcePathStyle: true,
	})
	if err != nil {
		_ = listener.Close()
		t.Fatalf("create isolated S3 client: %v", err)
	}
	ctx := context.Background()
	if err := store.EnsureBucket(ctx); err != nil {
		_ = listener.Close()
		t.Fatalf("ensure isolated S3 bucket: %v", err)
	}
	if err := store.RequirePrivate(ctx); err != nil {
		_ = listener.Close()
		t.Fatalf("isolated S3 bucket is not private: %v", err)
	}
	filesConfig := config.FilesConfig{
		MaxBytes: 16 << 20, PartBytes: 1 << 20,
		UploadTTL: 2 * time.Minute, CleanupInterval: time.Minute, ProcessingTimeout: time.Minute,
		OwnerQuotaBytes: 64 << 20, MaxSessions: 8, ListLimit: 20,
		RateLimit: 1200, RateMaxClients: 100, RateWindow: time.Minute,
		LinkMaxTTL: 15 * time.Minute, PublicBaseURL: baseURL, TempDir: t.TempDir(),
		SigningKey: bytes.Repeat([]byte("file-media-qa-signing-key-"), 2),
		Services: []config.FileServiceConfig{
			{Name: "file-media-service-a", Permissions: []string{"upload", "read", "list", "delete", "link"}, Keys: []config.FileSecretRef{{Env: "FILE_MEDIA_SERVICE_A_KEY"}}},
			{Name: "file-media-service-b", Permissions: []string{"read", "list"}, Keys: []config.FileSecretRef{{Env: "FILE_MEDIA_SERVICE_B_KEY"}}},
		},
	}
	service, err := files.NewService(env.pool, store, filesConfig)
	if err != nil {
		_ = listener.Close()
		t.Fatalf("create isolated file service: %v", err)
	}
	if err := service.InitOwners(ctx); err != nil {
		_ = listener.Close()
		t.Fatalf("initialize file owners: %v", err)
	}
	service.SetReferenceManager(env.snapshots)
	fileHandler := hfiles.NewHandler(service, env.routerDeps.Log, 1<<20)
	mediaHandler := hmedia.NewHandler(env.media, env.routerDeps.Log, 20<<20)
	if err := mediaHandler.SetRedirectCacheTTL(time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := mediaHandler.SetPagination(20, 50); err != nil {
		_ = listener.Close()
		t.Fatalf("configure media pagination: %v", err)
	}
	mediaHandler.SetProfileProvider(env.snapshots)
	mediaHandler.SetFileHandler(fileHandler)
	deps := env.routerDeps
	deps.Files = fileHandler
	deps.Media = mediaHandler
	server := httptest.NewUnstartedServer(httptransport.NewRouter(deps))
	server.Listener = listener
	server.Start()
	t.Cleanup(server.Close)
	t.Cleanup(func() { _ = service.Cleanup(context.Background()) })
	return &fileMediaHarness{service: service, server: server, store: store}
}

func makeFileMediaPNG(t *testing.T, fill color.Color) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			img.Set(x, y, fill)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func uploadFileMedia(t *testing.T, h *fileMediaHarness, owner files.Owner, name, visibility string, data []byte) files.File {
	t.Helper()
	file, err := h.service.UploadStream(context.Background(), owner, name, int64(len(data)), visibility, bytes.NewReader(data))
	if err != nil {
		t.Fatalf("upload %s: %v", name, err)
	}
	return file
}

func fileMediaRequest(t *testing.T, server *httptest.Server, method, path, token string, body any) (int, []byte, http.Header) {
	t.Helper()
	var requestBody io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		requestBody = bytes.NewReader(encoded)
	}
	req, err := http.NewRequest(method, server.URL+path, requestBody)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s %s response: %v", method, path, err)
	}
	return resp.StatusCode, data, resp.Header.Clone()
}

func createFileMediaSnapshot(t *testing.T, env *testEnv, imageID string) sitebuilder.SnapshotRecord {
	t.Helper()
	configRaw := []byte(`{"formatVersion":3,"pages":[{"id":"home","name":"Home","kind":"page","placements":[]}],"routeTree":{"id":"site","type":"site","children":[{"id":"home-route","type":"path","path":"/","pageId":"home","children":[]}]}}`)
	cfg, err := sitebuilder.ParseSiteConfig(configRaw)
	if err != nil {
		t.Fatalf("parse test site config: %v", err)
	}
	metadata := sitebuilder.SiteMetadata{FormatVersion: 1, SiteName: "File media QA", OGImageMediaID: imageID}
	snapshot, err := env.snapshots.CreateSnapshot(context.Background(), testActor(t, env), sitebuilder.CreateSnapshotInput{
		Name: "File media acceptance", Config: &cfg, Metadata: &metadata,
	})
	if err != nil {
		t.Fatalf("create snapshot with file reference: %v", err)
	}
	return snapshot
}

func decodeJSONMap(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("decode JSON response: %v; body=%s", err, raw)
	}
	return value
}

func TestAdminMediaGallerySharesAdminFileOwnerAndServiceKeysStayScoped(t *testing.T) {
	requireUniqueFileMediaEnvironment(t)
	env := newTestEnv(t)
	h := newFileMediaHarness(t, env)
	ctx := context.Background()
	admin := h.service.AdminOwner()
	serviceA, err := h.service.Authenticate(os.Getenv("FILE_MEDIA_SERVICE_A_KEY"), "upload")
	if err != nil {
		t.Fatalf("authenticate service A: %v", err)
	}
	blue := makeFileMediaPNG(t, color.RGBA{R: 20, G: 80, B: 160, A: 255})
	red := makeFileMediaPNG(t, color.RGBA{R: 180, G: 40, B: 20, A: 255})
	publicImage := uploadFileMedia(t, h, admin, "product-public.png", "public", blue)
	privateImage := uploadFileMedia(t, h, admin, "product-private.png", "private", blue)
	serviceImage := uploadFileMedia(t, h, serviceA, "service-only.png", "public", blue)

	firstVersion := uploadFileMedia(t, h, admin, "replacement.png", "public", blue)
	secondVersion := uploadFileMedia(t, h, admin, "replacement.png", "public", red)
	if firstVersion.ID == secondVersion.ID || firstVersion.SHA256 == secondVersion.SHA256 {
		t.Fatalf("replacing file bytes must create a new immutable object: first=%+v second=%+v", firstVersion, secondVersion)
	}
	if _, err := h.service.Get(ctx, admin, firstVersion.ID); err != nil {
		t.Fatalf("replacement removed the previous object: %v", err)
	}

	status, body, _ := fileMediaRequest(t, h.server, http.MethodGet, "/api/v1/admin/media?type=image&status=ready&q=product", env.adminToken, nil)
	gallery := decodeJSONMap(t, body)
	items, ok := gallery["items"].([]any)
	if status != http.StatusOK || !ok || len(items) != 2 {
		t.Fatalf("admin gallery response mismatch: status=%d body=%v", status, gallery)
	}
	seen := map[string]map[string]any{}
	for _, raw := range items {
		item := raw.(map[string]any)
		seen[item["id"].(string)] = item
		if item["ownerId"] != admin.ID.String() {
			t.Fatalf("gallery leaked service-owned file: %v", item)
		}
	}
	if _, ok := seen[publicImage.ID.String()]; !ok {
		t.Fatalf("public admin file absent from gallery: %s", publicImage.ID)
	}
	if item, ok := seen[privateImage.ID.String()]; !ok || item["visibility"] != "private" {
		t.Fatalf("gallery must show private admin files for management: item=%v present=%v", item, ok)
	}
	if _, ok := seen[serviceImage.ID.String()]; ok {
		t.Fatalf("service-owned file leaked into admin gallery: %s", serviceImage.ID)
	}

	status, serviceListRaw, _ := fileMediaRequest(t, h.server, http.MethodGet, "/api/v2/files?q=service", os.Getenv("FILE_MEDIA_SERVICE_A_KEY"), nil)
	serviceList := decodeJSONMap(t, serviceListRaw)
	serviceItems, ok := serviceList["items"].([]any)
	if status != http.StatusOK || !ok || len(serviceItems) != 1 || serviceItems[0].(map[string]any)["id"] != serviceImage.ID.String() {
		t.Fatalf("service A list must contain only its object: status=%d response=%v", status, serviceList)
	}
	status, serviceBListRaw, _ := fileMediaRequest(t, h.server, http.MethodGet, "/api/v2/files", os.Getenv("FILE_MEDIA_SERVICE_B_KEY"), nil)
	serviceBList := decodeJSONMap(t, serviceBListRaw)
	serviceBItems, ok := serviceBList["items"].([]any)
	if status != http.StatusOK || !ok || len(serviceBItems) != 0 {
		t.Fatalf("service B must not see service A files: status=%d response=%v", status, serviceBList)
	}
	status, _, _ = fileMediaRequest(t, h.server, http.MethodGet, "/api/v2/files/objects/"+serviceImage.ID.String(), os.Getenv("FILE_MEDIA_SERVICE_B_KEY"), nil)
	if status != http.StatusNotFound {
		t.Fatalf("service B metadata access to service A file must be hidden, got %d", status)
	}
	status, _, _ = fileMediaRequest(t, h.server, http.MethodGet, "/api/v2/files/objects/"+publicImage.ID.String(), os.Getenv("FILE_MEDIA_SERVICE_A_KEY"), nil)
	if status != http.StatusNotFound {
		t.Fatalf("service A must not read admin-owned file, got %d", status)
	}
	status, _, _ = fileMediaRequest(t, h.server, http.MethodDelete, "/api/v2/files/objects/"+serviceImage.ID.String(), os.Getenv("FILE_MEDIA_SERVICE_B_KEY"), nil)
	if status != http.StatusForbidden {
		t.Fatalf("read-only service key must not delete files, got %d", status)
	}
	status, _, _ = fileMediaRequest(t, h.server, http.MethodGet, "/api/v2/admin/files/objects/"+serviceImage.ID.String(), env.adminToken, nil)
	if status != http.StatusNotFound {
		t.Fatalf("admin must not read a service-owned object through the admin API, got %d", status)
	}

	status, publicBytes, headers := fileMediaRequest(t, h.server, http.MethodGet, "/media/"+publicImage.ID.String(), "", nil)
	if status != http.StatusOK || !bytes.Equal(publicBytes, blue) || headers.Get("Content-Type") != "image/png" {
		t.Fatalf("public media stream mismatch: status=%d type=%q bytes=%d", status, headers.Get("Content-Type"), len(publicBytes))
	}
	status, _, _ = fileMediaRequest(t, h.server, http.MethodGet, "/media/"+privateImage.ID.String(), "", nil)
	if status != http.StatusNotFound {
		t.Fatalf("private file must not be served on a public URL, got %d", status)
	}
	status, privateBytes, _ := fileMediaRequest(t, h.server, http.MethodGet, "/api/v2/admin/files/objects/"+privateImage.ID.String()+"/content", env.adminToken, nil)
	if status != http.StatusOK || !bytes.Equal(privateBytes, blue) {
		t.Fatalf("authenticated admin private read failed: status=%d bytes=%d", status, len(privateBytes))
	}
	objectURL := strings.TrimRight(os.Getenv("TEST_FILES_S3_ENDPOINT"), "/") + "/" + os.Getenv("TEST_FILES_S3_BUCKET") + "/" + privateImage.ObjectKey
	objectResponse, err := http.Get(objectURL)
	if err != nil {
		t.Fatalf("request private S3 object without credentials: %v", err)
	}
	objectResponse.Body.Close()
	if objectResponse.StatusCode < http.StatusBadRequest {
		t.Fatalf("private S3 bucket exposed an anonymous object: %d", objectResponse.StatusCode)
	}

	// Confirm that an outdated dependency revision cannot authorize deletion.
	snapshot := createFileMediaSnapshot(t, env, publicImage.ID.String())
	before, err := h.service.References(ctx, admin, publicImage.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 1 {
		t.Fatalf("snapshot reference was not discoverable: %+v", before)
	}
	status, staleBody, _ := fileMediaRequest(t, h.server, http.MethodDelete,
		"/api/v2/admin/files/objects/"+publicImage.ID.String()+"?confirm=true&referencesRevision=stale", env.adminToken, nil)
	if status != http.StatusConflict {
		t.Fatalf("stale reference revision must fail closed: status=%d body=%s", status, staleBody)
	}
	if _, err := h.service.Get(ctx, admin, publicImage.ID); err != nil {
		t.Fatalf("stale delete removed file: %v", err)
	}
	if snapshot.ID == uuid.Nil {
		t.Fatal("reference fixture did not create a snapshot")
	}
}

func TestAdminMediaGalleryAndPickerBrowserAcceptance(t *testing.T) {
	node, module, browser := os.Getenv("PRERENDER_TEST_NODE"), os.Getenv("PRERENDER_TEST_MODULE"), os.Getenv("PRERENDER_TEST_BROWSER")
	if node == "" || module == "" || browser == "" {
		t.Skip("set PRERENDER_TEST_NODE, PRERENDER_TEST_MODULE, and PRERENDER_TEST_BROWSER for file-media browser acceptance")
	}
	requireUniqueFileMediaEnvironment(t)
	env := newTestEnv(t)
	h := newFileMediaHarness(t, env)
	admin := h.service.AdminOwner()
	blue := makeFileMediaPNG(t, color.RGBA{R: 10, G: 120, B: 80, A: 255})
	privateImage := uploadFileMedia(t, h, admin, "gallery-private.png", "private", blue)
	publicImage := uploadFileMedia(t, h, admin, "gallery-public.png", "public", blue)
	serviceA, err := h.service.Authenticate(os.Getenv("FILE_MEDIA_SERVICE_A_KEY"), "upload")
	if err != nil {
		t.Fatalf("authenticate service owner: %v", err)
	}
	serviceImage := uploadFileMedia(t, h, serviceA, "gallery-service-only.png", "public", blue)
	snapshot := createFileMediaSnapshot(t, env, publicImage.ID.String())

	status, tokenBody, _ := fileMediaRequest(t, h.server, http.MethodPost, "/api/v1/admin/tokens", env.adminToken, map[string]any{"name": "file media viewer", "role": "user"})
	if status != http.StatusCreated {
		t.Fatalf("create viewer role for browser acceptance: status=%d body=%s", status, tokenBody)
	}
	userToken := decodeJSONMap(t, tokenBody)["token"].(string)

	mediaURL := h.server.URL + "/admin/media"
	settingsURL := h.server.URL + "/admin/site/settings?snapshot=" + url.QueryEscape(snapshot.ID.String())
	dataBase64 := base64.StdEncoding.EncodeToString(blue)
	browserScript := `import { pathToFileURL } from "node:url";
const { chromium } = await import(pathToFileURL(process.env.PLAYWRIGHT_TEST_MODULE).href);
const browser = await chromium.launch({ executablePath: process.env.PLAYWRIGHT_TEST_BROWSER, headless: true, args: ["--no-sandbox"] });
try {
  const page = await browser.newPage({ viewport: { width: 1280, height: 900 } });
  const errors = [];
  const mediaResponses = [];
  page.on("pageerror", error => errors.push(error.message));
  page.on("response", async response => {
    if (new URL(response.url()).pathname === "/api/v1/admin/media") {
      mediaResponses.push({ status: response.status(), body: await response.text().catch(() => "<unreadable>") });
    }
  });
  await page.addInitScript(token => { if (!localStorage.getItem("we_admin_token")) localStorage.setItem("we_admin_token", token); }, process.env.TEST_ADMIN_TOKEN);
  await page.goto(process.argv[1], { waitUntil: "domcontentloaded" });
  await page.getByRole("heading", { name: "Файлы", exact: true }).waitFor();
  await page.getByLabel("Поиск медиа").fill("gallery");
  await page.getByRole("button", { name: "Открыть gallery-public.png", exact: true }).waitFor().catch(async error => {
    const state = await page.evaluate(() => ({ url: location.href, body: document.body.innerText }));
    throw new Error("public gallery file did not render: " + JSON.stringify({ state, mediaResponses, errors, cause: error.message }));
  });
  await page.getByRole("button", { name: "Открыть gallery-private.png", exact: true }).waitFor();
  if (await page.getByRole("button", { name: "Открыть gallery-service-only.png", exact: true }).count()) throw new Error("admin gallery exposed service-owned files");
  await page.getByRole("combobox", { name: "Тип файла" }).click();
  await page.getByRole("option", { name: "Изображения", exact: true }).click();
  await page.getByRole("button", { name: "Открыть gallery-public.png", exact: true }).waitFor();
  await page.getByRole("button", { name: "Открыть gallery-public.png", exact: true }).click();
  const publicDetail = page.getByRole("dialog");
  await publicDetail.getByRole("heading", { name: "gallery-public.png", exact: true }).waitFor();
  await page.waitForFunction(() => [...document.querySelectorAll('img[alt="gallery-public.png"]')].some(image => image.complete && image.naturalWidth > 0));
  await publicDetail.getByText("Снапшот", { exact: false }).waitFor();
  await publicDetail.getByRole("button", { name: "Закрыть", exact: true }).first().click();

  // A user can manage and preview files but cannot delete from the admin library.
  await page.evaluate(token => localStorage.setItem("we_admin_token", token), process.env.TEST_USER_TOKEN);
  await page.reload({ waitUntil: "domcontentloaded" });
  await page.getByRole("heading", { name: "Файлы", exact: true }).waitFor();
  await page.getByLabel("Поиск медиа").fill("gallery-public");
  await page.getByRole("button", { name: "Открыть gallery-public.png", exact: true }).click();
  const userDetail = page.getByRole("dialog");
  await userDetail.getByRole("heading", { name: "gallery-public.png", exact: true }).waitFor();
  if (await userDetail.getByRole("button", { name: "Удалить", exact: true }).count()) throw new Error("non-admin role saw file deletion control");
  await userDetail.getByRole("button", { name: "Закрыть", exact: true }).first().click();

  // The picker only selects public files already in the library; it has no upload control.
  await page.evaluate(token => localStorage.setItem("we_admin_token", token), process.env.TEST_ADMIN_TOKEN);
  await page.goto(process.argv[2], { waitUntil: "domcontentloaded" });
  await page.getByRole("heading", { name: "Настройки сайта", exact: true }).waitFor();
  if (await page.locator('input[type="file"]').count()) throw new Error("site editor exposed a file upload input");
  await page.getByRole("button", { name: "Выбрать файл", exact: true }).first().click();
  const picker = page.getByRole("dialog");
  await picker.getByRole("heading", { name: "Выбрать медиа", exact: true }).waitFor();
  await picker.getByText("gallery-public.png", { exact: true }).waitFor();
  if (await picker.getByText("gallery-private.png", { exact: true }).count()) throw new Error("media picker exposed private file");
  if (await picker.getByText("gallery-service-only.png", { exact: true }).count()) throw new Error("media picker exposed service-owned file");
  if (await picker.locator('input[type="file"]').count()) throw new Error("media picker exposed a direct upload input");
  const libraryLink = picker.getByRole("link", { name: "Открыть медиатеку" }).first();
  if (await libraryLink.getAttribute("href") !== "/admin/media" || await libraryLink.getAttribute("target") !== "_blank") throw new Error("picker should link to the media library in a separate tab");
  await picker.getByRole("button", { name: "Отмена", exact: true }).click();

  // Admin upload uses the resumable file API and completes into the same gallery.
  await page.goto(process.argv[1], { waitUntil: "domcontentloaded" });
  await page.getByRole("heading", { name: "Файлы", exact: true }).waitFor();
  await page.getByRole("button", { name: "Загрузить", exact: true }).click();
  const upload = page.getByRole("dialog");
  await upload.getByRole("heading", { name: "Загрузка медиа", exact: true }).waitFor();
  await upload.locator("#upload-visibility").click();
  await page.getByRole("option", { name: "Публичный — доступен по ссылке", exact: true }).click();
  const input = upload.locator('input[type="file"][aria-label="Выбрать файлы"]');
  await input.setInputFiles({ name: "browser-upload.png", mimeType: "image/png", buffer: Buffer.from(process.env.TEST_PNG_BASE64, "base64") });
  await upload.getByText("Готово: 1 · Ошибок: 0", { exact: true }).waitFor({ timeout: 30000 });
  await upload.getByRole("button", { name: "Закрыть", exact: true }).first().click();
  await page.getByLabel("Поиск медиа").fill("browser-upload");
  await page.getByRole("button", { name: "Открыть browser-upload.png", exact: true }).waitFor();

  // Deletion displays usages, can be cancelled, then clears the reference only after confirmation.
  await page.getByLabel("Поиск медиа").fill("gallery-public");
  await page.getByRole("button", { name: "Открыть gallery-public.png", exact: true }).click();
  const adminDetail = page.getByRole("dialog");
  await adminDetail.getByRole("heading", { name: "gallery-public.png", exact: true }).waitFor();
  await adminDetail.getByRole("button", { name: "Удалить", exact: true }).click();
  const warning = page.getByRole("alertdialog");
  await warning.getByText("Известные использования: 1", { exact: true }).waitFor();
	  await warning.locator("li").filter({ hasText: "snapshot:" }).first().waitFor();
  await warning.getByRole("button", { name: "Отмена", exact: true }).click();
  await page.getByRole("alertdialog").waitFor({ state: "detached" });
  await adminDetail.getByRole("heading", { name: "gallery-public.png", exact: true }).waitFor();
  await adminDetail.getByRole("button", { name: "Удалить", exact: true }).click();
  const accepted = page.getByRole("alertdialog");
  await accepted.getByRole("button", { name: "Удалить и очистить ссылки", exact: true }).click();
  await page.getByRole("heading", { name: "Файлы", exact: true }).waitFor();
  await page.getByLabel("Поиск медиа").fill("gallery-public");
  await page.getByText("Файлы не найдены", { exact: true }).waitFor();
  if (errors.length) throw new Error("file-media browser raised exceptions: " + errors.join(" | "));
} finally { await browser.close(); }`
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, node, "--input-type=module", "-e", browserScript, mediaURL, settingsURL)
	command.Env = append(os.Environ(),
		"PLAYWRIGHT_TEST_MODULE="+module,
		"PLAYWRIGHT_TEST_BROWSER="+browser,
		"TEST_ADMIN_TOKEN="+env.adminToken,
		"TEST_USER_TOKEN="+userToken,
		"TEST_PNG_BASE64="+dataBase64,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("admin media/picker browser acceptance failed: %v\n%s", err, output)
	}
	if errs := strings.TrimSpace(string(output)); errs != "" {
		t.Log(errs)
	}
	if _, err := h.service.Get(context.Background(), h.service.AdminOwner(), publicImage.ID); err == nil {
		t.Fatal("confirmed browser deletion left the file available")
	}
	if _, err := h.service.Get(context.Background(), h.service.AdminOwner(), privateImage.ID); err != nil {
		t.Fatalf("browser deletion of public file affected private object: %v", err)
	}
	if _, err := h.service.Get(context.Background(), serviceA, serviceImage.ID); err != nil {
		t.Fatalf("browser deletion of admin file affected service owner: %v", err)
	}
	detail, err := env.snapshots.Snapshot(context.Background(), snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Payload.Metadata.OGImageMediaID != "" {
		t.Fatalf("confirmed file deletion did not clear snapshot metadata: %+v", detail.Payload.Metadata)
	}
	if detail.Snapshot.Digest == snapshot.Digest {
		t.Fatal("confirmed file deletion did not revise the snapshot structural digest")
	}
	if refs, err := h.service.References(context.Background(), h.service.AdminOwner(), publicImage.ID); err == nil || len(refs) != 0 {
		t.Fatalf("deleted file should have no discoverable reference, refs=%+v err=%v", refs, err)
	}
	req, err := http.NewRequest(http.MethodGet, h.server.URL+"/media/"+publicImage.ID.String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("If-None-Match", `"`+publicImage.SHA256+`"`)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("deleted media must not return a cached 304 response: got %d", resp.StatusCode)
	}
}
