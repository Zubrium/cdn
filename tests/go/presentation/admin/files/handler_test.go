package fileapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const (
	testUploadID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	testFileID   = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
)

type authContextKey struct{}

type fakeLoader struct {
	validCredential  string
	validSession     string
	forbidden        bool
	loaderErr        error
	authCalls        int
	sessionCalls     int
	beginCalls       int
	partCalls        int
	statusCalls      int
	commitCalls      int
	abortCalls       int
	statCalls        int
	readCalls        int
	rangeCalls       int
	updateCalls      int
	deleteCalls      int
	addGroupCalls    int
	removeGroupCalls int
	lastInput        BeginUploadInput
	lastPart         []byte
	lastPartNumber   int
	lastPartSize     int64
	lastRange        [2]int64
	lastRevision     int64
	lastGroup        string
	lastList         ListQuery
	setListCalls     int
	setCreateCalls   int
	setGetCalls      int
	setUpdateCalls   int
	setDeleteCalls   int
	setFileListCalls int
	setAddCalls      int
	setRemoveCalls   int
	lastSetInput     CreateFileSetInput
	lastSetQuery     SetListQuery
	lastSetID        string
	lastFileID       string
	lastSetRevision  int64
	set              FileSet
	file             File
}

func newFakeLoader() *fakeLoader {
	return &fakeLoader{
		validCredential: "test-core-key",
		validSession:    "ss_" + strings.Repeat("A", 43),
		file:            File{ID: testFileID, Name: "asset.txt", ContentType: "text/plain", Size: 6, SHA256: "bef57ec7f53d40beb640a780a639c83bc29ac8a9816f1fc6c5c6dcd93c4721", Revision: 1, Groups: []string{"assets"}, Attributes: map[string]string{"purpose": "test"}},
		set:             FileSet{ID: "dddddddd-dddd-4ddd-8ddd-dddddddddddd", Namespace: "features", Name: "catalog assets", ManagementGroup: "features", Revision: 4, CreatedAt: time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)},
	}
}

func TestFileAPIGroupAndUUIDValidationMatchCoreBoundary(t *testing.T) {
	if validGroupKey("assets.public") {
		t.Fatal("File API accepted a group key Core rejects")
	}
	if !validGroupKey("feature_assets-2") {
		t.Fatal("File API rejected a valid Core group key")
	}
	if validUUID("00000000-0000-0000-0000-000000000000") {
		t.Fatal("File API accepted the nil UUID as a resource identifier")
	}
	if !validUUID(testUploadID) {
		t.Fatal("File API rejected a valid UUID")
	}
}

func (f *fakeLoader) Authenticate(ctx context.Context, credential string) (context.Context, error) {
	f.authCalls++
	if credential != f.validCredential {
		return nil, errors.Join(ErrUnauthorized, errors.New("credential verification failed"))
	}
	return context.WithValue(ctx, authContextKey{}, true), nil
}

func (f *fakeLoader) AuthenticateSession(ctx context.Context, token string) (context.Context, error) {
	f.sessionCalls++
	if token != f.validSession {
		return nil, ErrUnauthorized
	}
	return context.WithValue(ctx, authContextKey{}, true), nil
}

func (f *fakeLoader) authorized(ctx context.Context) bool { return ctx.Value(authContextKey{}) == true }

func (f *fakeLoader) BeginUpload(ctx context.Context, input BeginUploadInput) (UploadSession, error) {
	if !f.authorized(ctx) {
		return UploadSession{}, ErrUnauthorized
	}
	f.beginCalls++
	f.lastInput = input
	return UploadSession{ID: testUploadID, State: "uploading", PartSize: 8, ExpiresAt: time.Now().Add(time.Hour)}, nil
}

func (f *fakeLoader) UploadStatus(ctx context.Context, id string) (UploadSession, error) {
	if !f.authorized(ctx) {
		return UploadSession{}, ErrUnauthorized
	}
	f.statusCalls++
	if f.forbidden {
		return UploadSession{}, ErrForbidden
	}
	if id != testUploadID {
		return UploadSession{}, ErrNotFound
	}
	return UploadSession{ID: id, State: "uploading", PartSize: 8, ExpiresAt: time.Now().Add(time.Hour)}, nil
}

func (f *fakeLoader) PutUploadPart(ctx context.Context, id string, number int, size int64, digest string, body io.Reader) (UploadSession, error) {
	if !f.authorized(ctx) {
		return UploadSession{}, ErrUnauthorized
	}
	f.partCalls++
	data, err := io.ReadAll(body)
	if err != nil {
		return UploadSession{}, err
	}
	sum := sha256.Sum256(data)
	if int64(len(data)) != size || hex.EncodeToString(sum[:]) != digest {
		return UploadSession{}, ErrInvalid
	}
	f.lastPart, f.lastPartNumber, f.lastPartSize = data, number, size
	return UploadSession{ID: id, State: "uploading", PartSize: 8, Parts: []UploadPart{{Number: number, Size: size, SHA256: digest}}}, nil
}

func (f *fakeLoader) CommitUpload(ctx context.Context, id string) (File, error) {
	if !f.authorized(ctx) {
		return File{}, ErrUnauthorized
	}
	f.commitCalls++
	if id != testUploadID {
		return File{}, ErrNotFound
	}
	return f.file, nil
}

func (f *fakeLoader) AbortUpload(ctx context.Context, id string) error {
	if !f.authorized(ctx) {
		return ErrUnauthorized
	}
	f.abortCalls++
	if id != testUploadID {
		return ErrNotFound
	}
	return nil
}

func (f *fakeLoader) ListFiles(ctx context.Context, query ListQuery) (FilePage, error) {
	if !f.authorized(ctx) {
		return FilePage{}, ErrUnauthorized
	}
	if f.forbidden {
		return FilePage{}, ErrForbidden
	}
	f.lastList = query
	return FilePage{Items: []File{f.file}, NextCursor: "next"}, nil
}

func (f *fakeLoader) StatFile(ctx context.Context, id string) (File, error) {
	if !f.authorized(ctx) {
		return File{}, ErrUnauthorized
	}
	f.statCalls++
	if f.loaderErr != nil {
		return File{}, f.loaderErr
	}
	if f.forbidden {
		return File{}, ErrForbidden
	}
	if id != testFileID {
		return File{}, ErrNotFound
	}
	return f.file, nil
}

func (f *fakeLoader) ReadFile(ctx context.Context, id string) (io.ReadCloser, File, error) {
	if !f.authorized(ctx) {
		return nil, File{}, ErrUnauthorized
	}
	f.readCalls++
	if f.forbidden {
		return nil, File{}, ErrForbidden
	}
	if id != testFileID {
		return nil, File{}, ErrNotFound
	}
	return io.NopCloser(strings.NewReader("abcdef")), f.file, nil
}

func (f *fakeLoader) ReadFileRange(ctx context.Context, id string, start, end int64) (io.ReadCloser, File, error) {
	if !f.authorized(ctx) {
		return nil, File{}, ErrUnauthorized
	}
	f.rangeCalls++
	if f.forbidden {
		return nil, File{}, ErrForbidden
	}
	if id != testFileID {
		return nil, File{}, ErrNotFound
	}
	f.lastRange = [2]int64{start, end}
	return io.NopCloser(strings.NewReader("abcdef"[start : end+1])), f.file, nil
}

func (f *fakeLoader) UpdateFile(ctx context.Context, id string, revision int64, patch FileMetadataPatch) (File, error) {
	if !f.authorized(ctx) {
		return File{}, ErrUnauthorized
	}
	f.updateCalls++
	if f.forbidden {
		return File{}, ErrForbidden
	}
	if id != testFileID {
		return File{}, ErrNotFound
	}
	f.lastRevision = revision
	if revision != f.file.Revision {
		return File{}, ErrConflict
	}
	if patch.Name != nil {
		f.file.Name = *patch.Name
	}
	if patch.ContentType != nil {
		f.file.ContentType = *patch.ContentType
	}
	f.file.Revision++
	return f.file, nil
}

func (f *fakeLoader) DeleteFile(ctx context.Context, id string, revision int64) error {
	if !f.authorized(ctx) {
		return ErrUnauthorized
	}
	f.deleteCalls++
	f.lastRevision = revision
	if f.forbidden {
		return ErrForbidden
	}
	return nil
}

func (f *fakeLoader) AddFileToGroup(ctx context.Context, id, group string, revision int64) (File, error) {
	if !f.authorized(ctx) {
		return File{}, ErrUnauthorized
	}
	f.addGroupCalls++
	f.lastGroup, f.lastRevision = group, revision
	if f.forbidden {
		return File{}, ErrForbidden
	}
	return f.file, nil
}

func (f *fakeLoader) RemoveFileFromGroup(ctx context.Context, id, group string, revision int64) (File, error) {
	if !f.authorized(ctx) {
		return File{}, ErrUnauthorized
	}
	f.removeGroupCalls++
	f.lastGroup, f.lastRevision = group, revision
	if f.forbidden {
		return File{}, ErrForbidden
	}
	return f.file, nil
}

func (f *fakeLoader) ListFileSets(ctx context.Context, query SetListQuery) (FileSetPage, error) {
	if !f.authorized(ctx) {
		return FileSetPage{}, ErrUnauthorized
	}
	f.setListCalls++
	f.lastSetQuery = query
	if f.forbidden {
		return FileSetPage{}, ErrForbidden
	}
	return FileSetPage{Items: []FileSet{f.set}}, nil
}

func (f *fakeLoader) CreateFileSet(ctx context.Context, input CreateFileSetInput) (FileSet, error) {
	if !f.authorized(ctx) {
		return FileSet{}, ErrUnauthorized
	}
	f.setCreateCalls++
	f.lastSetInput = input
	if f.forbidden {
		return FileSet{}, ErrForbidden
	}
	f.set.Namespace, f.set.Name, f.set.ManagementGroup = input.Namespace, input.Name, input.ManagementGroup
	return f.set, nil
}

func (f *fakeLoader) GetFileSet(ctx context.Context, id string) (FileSet, error) {
	if !f.authorized(ctx) {
		return FileSet{}, ErrUnauthorized
	}
	f.setGetCalls++
	f.lastSetID = id
	if f.forbidden {
		return FileSet{}, ErrForbidden
	}
	if id != f.set.ID {
		return FileSet{}, ErrNotFound
	}
	return f.set, nil
}

func (f *fakeLoader) UpdateFileSet(ctx context.Context, id string, revision int64, patch FileSetMetadataPatch) (FileSet, error) {
	if !f.authorized(ctx) {
		return FileSet{}, ErrUnauthorized
	}
	f.setUpdateCalls++
	f.lastSetID, f.lastSetRevision = id, revision
	if f.forbidden {
		return FileSet{}, ErrForbidden
	}
	if id != f.set.ID || revision != f.set.Revision {
		return FileSet{}, ErrConflict
	}
	if patch.Name != nil {
		f.set.Name = *patch.Name
	}
	f.set.Revision++
	return f.set, nil
}

func (f *fakeLoader) DeleteFileSet(ctx context.Context, id string, revision int64) error {
	if !f.authorized(ctx) {
		return ErrUnauthorized
	}
	f.setDeleteCalls++
	f.lastSetID, f.lastSetRevision = id, revision
	if f.forbidden {
		return ErrForbidden
	}
	if id != f.set.ID || revision != f.set.Revision {
		return ErrConflict
	}
	return nil
}

func (f *fakeLoader) ListSetFiles(ctx context.Context, id string, query ListQuery) (FilePage, error) {
	if !f.authorized(ctx) {
		return FilePage{}, ErrUnauthorized
	}
	f.setFileListCalls++
	f.lastSetID, f.lastList = id, query
	if f.forbidden {
		return FilePage{}, ErrForbidden
	}
	if id != f.set.ID {
		return FilePage{}, ErrNotFound
	}
	return FilePage{Items: []File{f.file}}, nil
}

func (f *fakeLoader) AddFileToSet(ctx context.Context, setID, fileID string, revision int64) (FileSet, error) {
	if !f.authorized(ctx) {
		return FileSet{}, ErrUnauthorized
	}
	f.setAddCalls++
	f.lastSetID, f.lastFileID, f.lastSetRevision = setID, fileID, revision
	if f.forbidden {
		return FileSet{}, ErrForbidden
	}
	if setID != f.set.ID || fileID != f.file.ID || revision != f.set.Revision {
		return FileSet{}, ErrConflict
	}
	f.set.Revision++
	return f.set, nil
}

func (f *fakeLoader) RemoveFileFromSet(ctx context.Context, setID, fileID string, revision int64) (FileSet, error) {
	if !f.authorized(ctx) {
		return FileSet{}, ErrUnauthorized
	}
	f.setRemoveCalls++
	f.lastSetID, f.lastFileID, f.lastSetRevision = setID, fileID, revision
	if f.forbidden {
		return FileSet{}, ErrForbidden
	}
	if setID != f.set.ID || fileID != f.file.ID || revision != f.set.Revision {
		return FileSet{}, ErrConflict
	}
	f.set.Revision++
	return f.set, nil
}

func testHandler(t *testing.T, loader Loader) *Handler {
	t.Helper()
	handler, err := NewHandler(loader, Config{
		MaxFileBytes: 1024, MaxPartBytes: 8, MaxUploadParts: 128,
		MaxJSONBytes: 2048, MaxAttributeBytes: 256, MaxGroupsPerFile: 8,
		DefaultPageSize: 10, MaxPageSize: 100, AllowedOrigins: []string{"https://admin.example"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func TestFileAPIUsesCoreSessionsFromCookiesAndRequiresCSRFForWrites(t *testing.T) {
	loader := newFakeLoader()
	handler := testHandler(t, loader)
	read := httptest.NewRequest(http.MethodGet, apiPrefix, nil)
	read.AddCookie(&http.Cookie{Name: "zubrium_admin_session", Value: loader.validSession})
	readResponse := httptest.NewRecorder()
	handler.ServeHTTP(readResponse, read)
	if readResponse.Code != http.StatusOK || loader.sessionCalls != 1 || loader.authCalls != 0 {
		t.Fatalf("cookie session read status=%d session-auth=%d credential-auth=%d", readResponse.Code, loader.sessionCalls, loader.authCalls)
	}

	write := httptest.NewRequest(http.MethodPost, apiPrefix+"/uploads", strings.NewReader(`{}`))
	write.Header.Set("Content-Type", "application/json")
	write.AddCookie(&http.Cookie{Name: "zubrium_admin_session", Value: loader.validSession})
	write.Header.Set("Origin", "https://admin.example")
	denied := httptest.NewRecorder()
	handler.ServeHTTP(denied, write)
	if denied.Code != http.StatusForbidden || loader.sessionCalls != 1 {
		t.Fatalf("cookie write without CSRF status=%d session-auth=%d", denied.Code, loader.sessionCalls)
	}

	csrf := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	write = httptest.NewRequest(http.MethodPost, apiPrefix+"/uploads", strings.NewReader(`{}`))
	write.Header.Set("Content-Type", "application/json")
	write.AddCookie(&http.Cookie{Name: "zubrium_admin_session", Value: loader.validSession})
	write.AddCookie(&http.Cookie{Name: "zubrium_admin_csrf", Value: csrf})
	write.Header.Set("Origin", "https://admin.example")
	write.Header.Set("X-CSRF-Token", csrf)
	accepted := httptest.NewRecorder()
	handler.ServeHTTP(accepted, write)
	if accepted.Code != http.StatusBadRequest || loader.sessionCalls != 2 {
		t.Fatalf("valid cookie/CSRF request did not reach input validation: status=%d session-auth=%d body=%s", accepted.Code, loader.sessionCalls, accepted.Body.String())
	}
}

func TestFileAPIReturnsConfiguredUploadLimits(t *testing.T) {
	loader := newFakeLoader()
	response := request(testHandler(t, loader), http.MethodGet, apiPrefix+"/limits", "", true)
	var limits struct {
		MaxBytes       int64 `json:"maxBytes"`
		PartBytes      int64 `json:"partBytes"`
		MaxUploadParts int   `json:"maxUploadParts"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &limits) != nil {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if limits.MaxBytes != 1024 || limits.PartBytes != 8 || limits.MaxUploadParts != 128 {
		t.Fatalf("unexpected configured limits: %+v", limits)
	}
}

func request(handler http.Handler, method, path, body string, authenticated bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if authenticated {
		req.Header.Set("Authorization", "Bearer test-core-key")
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	return response
}

func TestFileAPIAuthenticatesBeforeCallingLoader(t *testing.T) {
	core := newFakeLoader()
	handler := testHandler(t, core)
	response := request(handler, http.MethodGet, apiPrefix, "", false)
	if response.Code != http.StatusUnauthorized || core.authCalls != 0 {
		t.Fatalf("missing bearer authentication: status=%d core-auth-calls=%d", response.Code, core.authCalls)
	}
	request := httptest.NewRequest(http.MethodGet, apiPrefix, nil)
	request.Header.Set("Authorization", "Bearer wrong-key")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized || core.beginCalls != 0 {
		t.Fatalf("invalid credential: status=%d begin-calls=%d", recorder.Code, core.beginCalls)
	}
}

func TestResumableUploadLifecycleStreamsEachPart(t *testing.T) {
	core := newFakeLoader()
	handler := testHandler(t, core)
	data := "chunk"
	sum := sha256.Sum256([]byte(data))
	input, err := json.Marshal(map[string]any{
		"name": "image.txt", "contentType": "text/plain", "size": len(data),
		"sha256": hex.EncodeToString(sum[:]), "groups": []string{"assets"}, "attributes": map[string]string{"source": "editor"},
	})
	if err != nil {
		t.Fatal(err)
	}
	create := httptest.NewRequest(http.MethodPost, apiPrefix+"/uploads", bytes.NewReader(input))
	create.Header.Set("Authorization", "Bearer test-core-key")
	create.Header.Set("Content-Type", "application/json")
	create.Header.Set("Idempotency-Key", "create-upload-123456")
	created := httptest.NewRecorder()
	handler.ServeHTTP(created, create)
	if created.Code != http.StatusCreated || core.beginCalls != 1 {
		t.Fatalf("create upload: status=%d body=%s calls=%d", created.Code, created.Body.String(), core.beginCalls)
	}
	if core.lastInput.IdempotencyKey != "create-upload-123456" || len(core.lastInput.Groups) != 1 || core.lastInput.Groups[0] != "assets" {
		t.Fatalf("upload reservation input was not forwarded: %+v", core.lastInput)
	}
	if created.Header().Get("Location") != apiPrefix+"/uploads/"+testUploadID {
		t.Fatalf("upload Location: %q", created.Header().Get("Location"))
	}

	status := request(handler, http.MethodGet, apiPrefix+"/uploads/"+testUploadID, "", true)
	if status.Code != http.StatusOK || core.statusCalls != 1 {
		t.Fatalf("upload status: status=%d calls=%d", status.Code, core.statusCalls)
	}
	part := httptest.NewRequest(http.MethodPut, apiPrefix+"/uploads/"+testUploadID+"/parts/1", strings.NewReader(data))
	part.Header.Set("Authorization", "Bearer test-core-key")
	part.Header.Set("Content-Type", "application/octet-stream")
	part.Header.Set("X-Content-SHA256", hex.EncodeToString(sum[:]))
	partRecorder := httptest.NewRecorder()
	handler.ServeHTTP(partRecorder, part)
	if partRecorder.Code != http.StatusOK || core.partCalls != 1 || string(core.lastPart) != data || core.lastPartNumber != 1 || core.lastPartSize != int64(len(data)) {
		t.Fatalf("streamed part was not passed intact: status=%d body=%s part=%q", partRecorder.Code, partRecorder.Body.String(), core.lastPart)
	}
	commit := request(handler, http.MethodPost, apiPrefix+"/uploads/"+testUploadID+"/commit", "", true)
	if commit.Code != http.StatusCreated || core.commitCalls != 1 || commit.Header().Get("ETag") == "" {
		t.Fatalf("commit: status=%d body=%s calls=%d", commit.Code, commit.Body.String(), core.commitCalls)
	}
	if strings.Contains(commit.Body.String(), "owner") || strings.Contains(commit.Body.String(), "public") || strings.Contains(commit.Body.String(), "role") || strings.Contains(commit.Body.String(), "version") {
		t.Fatalf("response exposes retired file fields: %s", commit.Body.String())
	}
	abort := request(handler, http.MethodDelete, apiPrefix+"/uploads/"+testUploadID, "", true)
	if abort.Code != http.StatusNoContent || core.abortCalls != 1 {
		t.Fatalf("abort: status=%d calls=%d", abort.Code, core.abortCalls)
	}
}

func TestUploadRejectsInvalidMetadataBeforeCoreReservation(t *testing.T) {
	core := newFakeLoader()
	handler := testHandler(t, core)
	validSum := strings.Repeat("0", 64)
	cases := []string{
		`{"name":"../unsafe","contentType":"text/plain","size":1,"sha256":"` + validSum + `","groups":["assets"]}`,
		`{"name":"file.txt","contentType":"text/plain","size":1025,"sha256":"` + validSum + `","groups":["assets"]}`,
		`{"name":"file.txt","contentType":"text/plain","size":1,"sha256":"` + validSum + `","groups":["assets","assets"]}`,
		`{"name":"file.txt","contentType":"text/plain","size":1,"sha256":"` + validSum + `","groups":["assets"],"public":true}`,
		`{"name":"file.txt","contentType":"text/plain","size":1,"sha256":"` + validSum + `","groups":["assets"],"role":"admin"}`,
	}
	for _, body := range cases {
		req := httptest.NewRequest(http.MethodPost, apiPrefix+"/uploads", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer test-core-key")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "create-upload-123456")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != http.StatusBadRequest && response.Code != http.StatusRequestEntityTooLarge {
			t.Errorf("invalid upload metadata accepted: status=%d body=%s", response.Code, response.Body.String())
		}
	}
	if core.beginCalls != 0 {
		t.Fatalf("Core reserved %d invalid uploads", core.beginCalls)
	}
}

func TestUploadAndPartRejectDuplicateSecurityHeaders(t *testing.T) {
	core := newFakeLoader()
	handler := testHandler(t, core)
	body := `{"name":"asset.txt","contentType":"text/plain","size":1,"sha256":"` + strings.Repeat("0", 64) + `","groups":["assets"]}`
	duplicateKey := httptest.NewRequest(http.MethodPost, apiPrefix+"/uploads", strings.NewReader(body))
	duplicateKey.Header.Set("Authorization", "Bearer test-core-key")
	duplicateKey.Header.Set("Content-Type", "application/json")
	duplicateKey.Header.Add("Idempotency-Key", "create-upload-123456")
	duplicateKey.Header.Add("Idempotency-Key", "create-upload-123457")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, duplicateKey)
	if response.Code != http.StatusBadRequest || core.beginCalls != 0 {
		t.Fatalf("duplicate Idempotency-Key status=%d calls=%d body=%s", response.Code, core.beginCalls, response.Body.String())
	}

	duplicateType := httptest.NewRequest(http.MethodPost, apiPrefix+"/uploads", strings.NewReader(body))
	duplicateType.Header.Set("Authorization", "Bearer test-core-key")
	duplicateType.Header.Add("Content-Type", "application/json")
	duplicateType.Header.Add("Content-Type", "text/plain")
	duplicateType.Header.Set("Idempotency-Key", "create-upload-123456")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, duplicateType)
	if response.Code != http.StatusBadRequest || core.beginCalls != 0 {
		t.Fatalf("duplicate Content-Type status=%d calls=%d body=%s", response.Code, core.beginCalls, response.Body.String())
	}

	part := httptest.NewRequest(http.MethodPut, apiPrefix+"/uploads/"+testUploadID+"/parts/1", strings.NewReader("x"))
	part.Header.Set("Authorization", "Bearer test-core-key")
	part.Header.Set("Content-Type", "application/octet-stream")
	part.Header.Add("X-Content-SHA256", strings.Repeat("0", 64))
	part.Header.Add("X-Content-SHA256", strings.Repeat("1", 64))
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, part)
	if response.Code != http.StatusBadRequest || core.partCalls != 0 {
		t.Fatalf("duplicate part checksum status=%d calls=%d body=%s", response.Code, core.partCalls, response.Body.String())
	}
}

func TestFileAPIRejectsAmbiguousJSONAndInvalidUTF8(t *testing.T) {
	core := newFakeLoader()
	handler := testHandler(t, core)
	validSum := strings.Repeat("0", 64)
	duplicate := `{"name":"asset.txt","contentType":"text/plain","size":1,"size":2,"sha256":"` + validSum + `","groups":["assets"]}`
	request := httptest.NewRequest(http.MethodPost, apiPrefix+"/uploads", strings.NewReader(duplicate))
	request.Header.Set("Authorization", "Bearer test-core-key")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "create-upload-123456")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || core.beginCalls != 0 {
		t.Fatalf("duplicate JSON key status=%d calls=%d body=%s", response.Code, core.beginCalls, response.Body.String())
	}

	invalidUTF8 := []byte(`{"name":"`)
	invalidUTF8 = append(invalidUTF8, 0xff)
	invalidUTF8 = append(invalidUTF8, []byte(`.txt","contentType":"text/plain","size":1,"sha256":"`+validSum+`","groups":["assets"]}`)...)
	request = httptest.NewRequest(http.MethodPost, apiPrefix+"/uploads", bytes.NewReader(invalidUTF8))
	request.Header.Set("Authorization", "Bearer test-core-key")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "create-upload-123456")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || core.beginCalls != 0 {
		t.Fatalf("invalid UTF-8 status=%d calls=%d body=%s", response.Code, core.beginCalls, response.Body.String())
	}
}

func TestMetadataMutationsRequireCurrentETag(t *testing.T) {
	core := newFakeLoader()
	handler := testHandler(t, core)
	path := apiPrefix + "/objects/" + testFileID
	missing := request(handler, http.MethodPatch, path, `{"name":"renamed.txt"}`, true)
	if missing.Code != http.StatusPreconditionRequired || core.updateCalls != 0 {
		t.Fatalf("mutation without If-Match: status=%d calls=%d", missing.Code, core.updateCalls)
	}
	patch := httptest.NewRequest(http.MethodPatch, path, strings.NewReader(`{"name":"renamed.txt"}`))
	patch.Header.Set("Authorization", "Bearer test-core-key")
	patch.Header.Set("Content-Type", "application/json")
	patch.Header.Set("If-Match", `"`+testFileID+`-r1"`)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, patch)
	if response.Code != http.StatusOK || core.lastRevision != 1 || response.Header().Get("ETag") != `"`+testFileID+`-r2"` {
		t.Fatalf("conditional metadata update: status=%d revision=%d ETag=%q body=%s", response.Code, core.lastRevision, response.Header().Get("ETag"), response.Body.String())
	}
	stale := httptest.NewRequest(http.MethodPatch, path, strings.NewReader(`{"name":"stale.txt"}`))
	stale.Header.Set("Authorization", "Bearer test-core-key")
	stale.Header.Set("Content-Type", "application/json")
	stale.Header.Set("If-Match", `"`+testFileID+`-r1"`)
	staleResponse := httptest.NewRecorder()
	handler.ServeHTTP(staleResponse, stale)
	if staleResponse.Code != http.StatusConflict {
		t.Fatalf("stale metadata revision: status=%d body=%s", staleResponse.Code, staleResponse.Body.String())
	}
	wrongFile := httptest.NewRequest(http.MethodPatch, path, strings.NewReader(`{"name":"wrong-etag.txt"}`))
	wrongFile.Header.Set("Authorization", "Bearer test-core-key")
	wrongFile.Header.Set("Content-Type", "application/json")
	wrongFile.Header.Set("If-Match", `"cccccccc-cccc-4ccc-8ccc-cccccccccccc-r2"`)
	wrongFileResponse := httptest.NewRecorder()
	handler.ServeHTTP(wrongFileResponse, wrongFile)
	if wrongFileResponse.Code != http.StatusPreconditionFailed || core.updateCalls != 2 {
		t.Fatalf("ETag from another file was accepted: status=%d calls=%d", wrongFileResponse.Code, core.updateCalls)
	}
	duplicate := httptest.NewRequest(http.MethodPatch, path, strings.NewReader(`{"name":"ambiguous.txt"}`))
	duplicate.Header.Set("Authorization", "Bearer test-core-key")
	duplicate.Header.Set("Content-Type", "application/json")
	duplicate.Header.Add("If-Match", `"`+testFileID+`-r1"`)
	duplicate.Header.Add("If-Match", `"`+testFileID+`-r2"`)
	duplicateResponse := httptest.NewRecorder()
	handler.ServeHTTP(duplicateResponse, duplicate)
	if duplicateResponse.Code != http.StatusBadRequest || core.updateCalls != 2 {
		t.Fatalf("duplicate If-Match status=%d calls=%d body=%s", duplicateResponse.Code, core.updateCalls, duplicateResponse.Body.String())
	}
}

func TestDownloadSupportsRangesAndPrivateHeaders(t *testing.T) {
	core := newFakeLoader()
	handler := testHandler(t, core)
	req := httptest.NewRequest(http.MethodGet, apiPrefix+"/objects/"+testFileID+"/content", nil)
	req.Header.Set("Authorization", "Bearer test-core-key")
	req.Header.Set("Range", "bytes=2-4")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusPartialContent || response.Body.String() != "cde" || core.lastRange != [2]int64{2, 4} {
		t.Fatalf("range download: status=%d bytes=%q range=%v", response.Code, response.Body.String(), core.lastRange)
	}
	if response.Header().Get("Content-Range") != "bytes 2-4/6" || response.Header().Get("Cache-Control") != "private, no-store" || response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("unsafe or incomplete download headers: %v", response.Header())
	}
	duplicateRange := httptest.NewRequest(http.MethodGet, apiPrefix+"/objects/"+testFileID+"/content", nil)
	duplicateRange.Header.Set("Authorization", "Bearer test-core-key")
	duplicateRange.Header.Add("Range", "bytes=0-1")
	duplicateRange.Header.Add("Range", "bytes=2-3")
	duplicateRangeResponse := httptest.NewRecorder()
	handler.ServeHTTP(duplicateRangeResponse, duplicateRange)
	if duplicateRangeResponse.Code != http.StatusBadRequest || core.rangeCalls != 1 {
		t.Fatalf("duplicate Range status=%d calls=%d", duplicateRangeResponse.Code, core.rangeCalls)
	}
}

func TestForbiddenFileLooksMissing(t *testing.T) {
	core := newFakeLoader()
	core.forbidden = true
	handler := testHandler(t, core)
	response := request(handler, http.MethodGet, apiPrefix+"/objects/"+testFileID, "", true)
	if response.Code != http.StatusNotFound || strings.Contains(response.Body.String(), testFileID) {
		t.Fatalf("forbidden file disclosed: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestFileAPISetsAreAuthenticatedAndRevisionProtected(t *testing.T) {
	core := newFakeLoader()
	handler := testHandler(t, core)
	unauthenticated := request(handler, http.MethodGet, apiPrefix+"/sets", "", false)
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("set listing without credentials: status=%d", unauthenticated.Code)
	}
	list := request(handler, http.MethodGet, apiPrefix+"/sets?namespace=features&limit=5", "", true)
	if list.Code != http.StatusOK || core.setListCalls != 1 || core.lastSetQuery.Namespace != "features" || core.lastSetQuery.Limit != 5 {
		t.Fatalf("set listing: status=%d calls=%d query=%+v body=%s", list.Code, core.setListCalls, core.lastSetQuery, list.Body.String())
	}
	noRevision := request(handler, http.MethodDelete, apiPrefix+"/sets/"+core.set.ID, "", true)
	if noRevision.Code != http.StatusPreconditionRequired || core.setDeleteCalls != 0 {
		t.Fatalf("set delete without revision: status=%d calls=%d", noRevision.Code, core.setDeleteCalls)
	}
}

func TestGroupMembershipMutationUsesRevisionAndCoreAuthorization(t *testing.T) {
	core := newFakeLoader()
	handler := testHandler(t, core)
	path := apiPrefix + "/objects/" + testFileID + "/groups/private"
	req := httptest.NewRequest(http.MethodPut, path, nil)
	req.Header.Set("Authorization", "Bearer test-core-key")
	req.Header.Set("If-Match", `"`+testFileID+`-r1"`)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusOK || core.addGroupCalls != 1 || core.lastGroup != "private" || core.lastRevision != 1 {
		t.Fatalf("group add: status=%d calls=%d group=%q revision=%d body=%s", response.Code, core.addGroupCalls, core.lastGroup, core.lastRevision, response.Body.String())
	}
	if strings.Count(response.Body.String(), "\n") != 1 {
		t.Fatalf("group mutation wrote more than one response: %q", response.Body.String())
	}
	core.forbidden = true
	denied := httptest.NewRecorder()
	handler.ServeHTTP(denied, req.Clone(req.Context()))
	if denied.Code != http.StatusNotFound {
		t.Fatalf("denied group operation disclosed file: status=%d body=%s", denied.Code, denied.Body.String())
	}
}

func TestUnauthorizedResponseDoesNotIncludeCoreError(t *testing.T) {
	core := newFakeLoader()
	core.validCredential = "correct"
	handler := testHandler(t, core)
	req := httptest.NewRequest(http.MethodGet, apiPrefix+"/objects/"+testFileID, nil)
	req.Header.Set("Authorization", "Bearer wrong")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusUnauthorized || strings.Contains(response.Body.String(), "credential verification failed") {
		t.Fatalf("credential failure leaked details: %d %q", response.Code, response.Body.String())
	}
}

func TestListFilesForwardsFiltersWithoutTotals(t *testing.T) {
	core := newFakeLoader()
	handler := testHandler(t, core)
	response := request(handler, http.MethodGet, apiPrefix+"?group=assets&q=logo&cursor=opaque&limit=7", "", true)
	if response.Code != http.StatusOK || core.lastList != (ListQuery{Group: "assets", Query: "logo", Cursor: "opaque", Limit: 7}) {
		t.Fatalf("list request: status=%d query=%+v body=%s", response.Code, core.lastList, response.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if _, exists := body["totalCount"]; exists || body["nextCursor"] != "next" {
		t.Fatalf("response leaked totals or lost cursor: %v", body)
	}
	for _, path := range []string{apiPrefix + "?limit=101", apiPrefix + "?group=../private", apiPrefix + "?q=" + strings.Repeat("x", 256), apiPrefix + "?q=%00"} {
		invalid := request(handler, http.MethodGet, path, "", true)
		if invalid.Code != http.StatusBadRequest {
			t.Errorf("invalid list query %q: status=%d", path, invalid.Code)
		}
	}
}

func TestGetAndDeleteFileUseSafeDTOAndConditionalRevision(t *testing.T) {
	core := newFakeLoader()
	handler := testHandler(t, core)
	get := request(handler, http.MethodGet, apiPrefix+"/objects/"+testFileID, "", true)
	if get.Code != http.StatusOK || get.Header().Get("ETag") != `"`+testFileID+`-r1"` {
		t.Fatalf("get metadata: status=%d ETag=%q body=%s", get.Code, get.Header().Get("ETag"), get.Body.String())
	}
	delete := httptest.NewRequest(http.MethodDelete, apiPrefix+"/objects/"+testFileID, nil)
	delete.Header.Set("Authorization", "Bearer test-core-key")
	delete.Header.Set("If-Match", `"`+testFileID+`-r1"`)
	deleted := httptest.NewRecorder()
	handler.ServeHTTP(deleted, delete)
	if deleted.Code != http.StatusNoContent || core.deleteCalls != 1 || core.lastRevision != 1 {
		t.Fatalf("conditional delete: status=%d calls=%d revision=%d", deleted.Code, core.deleteCalls, core.lastRevision)
	}
}

func TestLoaderOutageIsSanitizedAsServiceUnavailable(t *testing.T) {
	core := newFakeLoader()
	core.loaderErr = errors.Join(ErrUnavailable, errors.New("postgres password must not be exposed"))
	handler := testHandler(t, core)
	response := request(handler, http.MethodGet, apiPrefix+"/objects/"+testFileID, "", true)
	if response.Code != http.StatusServiceUnavailable || strings.Contains(response.Body.String(), "postgres password") {
		t.Fatalf("Loader outage response: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestUploadBoundaryRejectsMissingHeadersAndOversizedBodies(t *testing.T) {
	core := newFakeLoader()
	handler := testHandler(t, core)
	missingIdempotency := httptest.NewRequest(http.MethodPost, apiPrefix+"/uploads", strings.NewReader(`{}`))
	missingIdempotency.Header.Set("Authorization", "Bearer test-core-key")
	missingIdempotency.Header.Set("Content-Type", "application/json")
	missingKeyResponse := httptest.NewRecorder()
	handler.ServeHTTP(missingKeyResponse, missingIdempotency)
	if missingKeyResponse.Code != http.StatusBadRequest {
		t.Fatalf("missing Idempotency-Key: %d", missingKeyResponse.Code)
	}
	wrongType := httptest.NewRequest(http.MethodPost, apiPrefix+"/uploads", strings.NewReader(`{}`))
	wrongType.Header.Set("Authorization", "Bearer test-core-key")
	wrongType.Header.Set("Idempotency-Key", "create-upload-123456")
	wrongType.Header.Set("Content-Type", "text/plain")
	wrongTypeResponse := httptest.NewRecorder()
	handler.ServeHTTP(wrongTypeResponse, wrongType)
	if wrongTypeResponse.Code != http.StatusBadRequest {
		t.Fatalf("non-JSON upload metadata: %d", wrongTypeResponse.Code)
	}
	oversized := strings.NewReader(`{"name":"` + strings.Repeat("x", 3000) + `"}`)
	largeRequest := httptest.NewRequest(http.MethodPost, apiPrefix+"/uploads", oversized)
	largeRequest.Header.Set("Authorization", "Bearer test-core-key")
	largeRequest.Header.Set("Idempotency-Key", "create-upload-123456")
	largeRequest.Header.Set("Content-Type", "application/json")
	largeResponse := httptest.NewRecorder()
	handler.ServeHTTP(largeResponse, largeRequest)
	if largeResponse.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized JSON body: status=%d", largeResponse.Code)
	}
	part := httptest.NewRequest(http.MethodPut, apiPrefix+"/uploads/"+testUploadID+"/parts/1", strings.NewReader("123456789"))
	part.Header.Set("Authorization", "Bearer test-core-key")
	part.Header.Set("Content-Type", "application/octet-stream")
	part.Header.Set("X-Content-SHA256", strings.Repeat("0", 64))
	partResponse := httptest.NewRecorder()
	handler.ServeHTTP(partResponse, part)
	if partResponse.Code != http.StatusRequestEntityTooLarge || core.partCalls != 0 {
		t.Fatalf("oversized chunk reached Core: status=%d calls=%d", partResponse.Code, core.partCalls)
	}
	if core.beginCalls != 0 {
		t.Fatalf("invalid upload created %d reservations", core.beginCalls)
	}
}

func TestDownloadFullInvalidRangeAndHead(t *testing.T) {
	core := newFakeLoader()
	handler := testHandler(t, core)
	full := request(handler, http.MethodGet, apiPrefix+"/objects/"+testFileID+"/content", "", true)
	if full.Code != http.StatusOK || full.Body.String() != "abcdef" || full.Header().Get("Content-Length") != "6" {
		t.Fatalf("full download: status=%d bytes=%q headers=%v", full.Code, full.Body.String(), full.Header())
	}
	badRange := httptest.NewRequest(http.MethodGet, apiPrefix+"/objects/"+testFileID+"/content", nil)
	badRange.Header.Set("Authorization", "Bearer test-core-key")
	badRange.Header.Set("Range", "bytes=1-2,4-5")
	badRangeResponse := httptest.NewRecorder()
	handler.ServeHTTP(badRangeResponse, badRange)
	if badRangeResponse.Code != http.StatusRequestedRangeNotSatisfiable || badRangeResponse.Header().Get("Content-Range") != "bytes */6" {
		t.Fatalf("invalid range: status=%d headers=%v", badRangeResponse.Code, badRangeResponse.Header())
	}
	head := httptest.NewRequest(http.MethodHead, apiPrefix+"/objects/"+testFileID+"/content", nil)
	head.Header.Set("Authorization", "Bearer test-core-key")
	head.Header.Set("Range", "bytes=-2")
	headResponse := httptest.NewRecorder()
	handler.ServeHTTP(headResponse, head)
	if headResponse.Code != http.StatusPartialContent || headResponse.Body.Len() != 0 || headResponse.Header().Get("Content-Range") != "bytes 4-5/6" {
		t.Fatalf("HEAD range: status=%d body=%q headers=%v", headResponse.Code, headResponse.Body.String(), headResponse.Header())
	}
}

func TestPatchValidatesFieldsAndAttributes(t *testing.T) {
	core := newFakeLoader()
	handler := testHandler(t, core)
	for _, body := range []string{`{}`, `{"name":"../bad"}`, `{"contentType":"text"}`, `{"attributes":{"key":"` + strings.Repeat("x", 300) + `"}}`} {
		req := httptest.NewRequest(http.MethodPatch, apiPrefix+"/objects/"+testFileID, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer test-core-key")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("If-Match", `"`+testFileID+`-r1"`)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != http.StatusBadRequest && response.Code != http.StatusRequestEntityTooLarge {
			t.Errorf("invalid patch accepted: status=%d body=%s", response.Code, response.Body.String())
		}
	}
	if core.updateCalls != 0 {
		t.Fatalf("invalid patches reached Core %d times", core.updateCalls)
	}
}
