package publicupload_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	core "github.com/Zubrium/cdn/core/presentation"
	loader "github.com/Zubrium/cdn/engines/loader/features/files/pkg"
	engine "github.com/Zubrium/cdn/engines/router/presentation"
	routerhttp "github.com/Zubrium/cdn/presentation/router"
	"github.com/Zubrium/cdn/presentation/router/publicupload"
)

const (
	fileSetID = "01938f7a-68af-7c10-a51d-78b6ca125301"
	fileID    = "01938f7a-68af-7c10-a51d-78b6ca125302"
	uploadID  = "01938f7a-68af-7c10-a51d-78b6ca125303"
)

func TestUploadOperationStreamsToLoaderAndAddsFileToConfiguredSet(t *testing.T) {
	api := &fakeLoader{set: loader.FileSet{ID: fileSetID, ManagementGroup: "asset_index", Revision: 8}}
	authorizer := &fakeGroupAuthorizer{}
	stagingDirectory := privateTempDir(t)
	operation, err := publicupload.New(api, publicupload.Config{
		Credential: "server-only-secret", StagingDirectory: stagingDirectory, MaxFileBytes: 32,
		PartSize: 4, MaxParts: 8, MaxConcurrentUploads: 2, MaxStagedBytes: 64,
		AllowedContentTypes: []string{"application/octet-stream", "image/png"}, GroupAuthorization: authorizer,
	})
	if err != nil {
		t.Fatal(err)
	}
	config := json.RawMessage(`{"enabled":true,"access_group":"public_uploads","file_set_id":"` + fileSetID + `","max_bytes":16,"allowed_content_types":["image/png"]}`)
	request := engine.PublicOperationRequest{
		Method: "POST", Host: "forms.example", EscapedPath: "/upload", ContentType: "image/png",
		ContentLength: 7, IdempotencyKey: "visitor-upload-request-0001", Body: &checkingReader{t: t, directory: stagingDirectory, data: []byte("pngdata")},
	}
	catalog, err := routerhttp.NewPublicOperationRegistry(operation)
	if err != nil {
		t.Fatal(err)
	}
	response, err := routerhttp.DispatchPublicOperation(context.Background(), catalog, engine.RouteTarget{
		Kind: engine.TargetRegisteredOperation, OperationID: publicupload.OperationID, OperationConfig: config,
	}, request)
	if err != nil {
		t.Fatalf("Execute returned %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		FileID    string `json:"file_id"`
		FileSetID string `json:"file_set_id,omitempty"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("invalid upload response %q: %v", body, err)
	}
	if response.StatusCode != 201 || response.ContentType != "application/json; charset=utf-8" || response.CacheControl != "no-store" || result.FileID != fileID || result.FileSetID != fileSetID {
		t.Fatalf("unexpected response: %+v body=%s", response, body)
	}
	if api.credential != "server-only-secret" || len(api.uploads) != 1 || api.uploads[0].Groups[0] != "public_uploads" || api.uploads[0].ContentType != "image/png" || api.uploads[0].Size != 7 || api.uploads[0].Name == "" {
		t.Fatalf("Loader received an unexpected upload: credential=%q uploads=%+v", api.credential, api.uploads)
	}
	if api.uploads[0].SHA256 != digest("pngdata") || api.uploads[0].IdempotencyKey == request.IdempotencyKey || len(api.uploads[0].IdempotencyKey) != 64 {
		t.Fatalf("upload was not hashed and route-scoped before it reached Loader: %+v", api.uploads[0])
	}
	if strings.Join(api.parts, "") != "pngdata" || api.commits != 1 || api.addedFileID != fileID || api.addedSetID != fileSetID || api.addedRevision != 8 {
		t.Fatalf("Loader upload lifecycle incomplete: parts=%q commits=%d set=%s file=%s rev=%d", api.parts, api.commits, api.addedSetID, api.addedFileID, api.addedRevision)
	}
	entries, err := os.ReadDir(stagingDirectory)
	if err != nil || len(entries) != 0 {
		t.Fatalf("staged body was not removed after completion: entries=%v err=%v", entries, err)
	}
}

func TestUploadOperationValidatesConfiguredCapabilities(t *testing.T) {
	operation := newOperation(t, &fakeLoader{})
	valid := `{"enabled":true,"access_group":"public_uploads","max_bytes":8,"allowed_content_types":["application/octet-stream"]}`
	if err := operation.ValidateRouteConfig([]byte(valid)); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	for _, config := range []string{
		`{"access_group":"public_uploads","max_bytes":8,"allowed_content_types":["application/octet-stream"]}`,
		`{"enabled":false,"access_group":"public_uploads","max_bytes":8,"allowed_content_types":["application/octet-stream"]}`,
		`{"enabled":true,"access_group":"../secret","max_bytes":8,"allowed_content_types":["application/octet-stream"]}`,
		`{"enabled":true,"access_group":"public_uploads","max_bytes":33,"allowed_content_types":["application/octet-stream"]}`,
		`{"enabled":true,"access_group":"public_uploads","max_bytes":8,"allowed_content_types":["text/html"]}`,
		`{"enabled":true,"access_group":"public_uploads","max_bytes":8,"allowed_content_types":["image/*"]}`,
		`{"enabled":true,"access_group":"public_uploads","max_bytes":8,"allowed_content_types":["application/octet-stream","application/octet-stream"]}`,
		`{"enabled":true,"access_group":"public_uploads","file_set_id":"invalid","max_bytes":8,"allowed_content_types":["application/octet-stream"]}`,
		`{"enabled":true,"access_group":"public_uploads","max_bytes":8,"allowed_content_types":["application/octet-stream"],"unknown":true}`,
		`{"enabled":false,"enabled":true,"access_group":"public_uploads","max_bytes":8,"allowed_content_types":["application/octet-stream"]}`,
		`[]`,
	} {
		if err := operation.ValidateRouteConfig([]byte(config)); err == nil {
			t.Errorf("invalid config accepted: %s", config)
		}
	}
}

func TestNewRejectsTypedNilDependencies(t *testing.T) {
	config := publicupload.Config{
		Credential: "server-only-secret", StagingDirectory: privateTempDir(t), MaxFileBytes: 32,
		PartSize: 4, MaxParts: 8, MaxConcurrentUploads: 1, MaxStagedBytes: 32,
		AllowedContentTypes: []string{"application/octet-stream"}, GroupAuthorization: &fakeGroupAuthorizer{},
	}
	var nilLoader *fakeLoader
	if _, err := publicupload.New(nilLoader, config); err == nil {
		t.Fatal("typed nil Loader dependency was accepted")
	}
	config.GroupAuthorization = (*fakeGroupAuthorizer)(nil)
	if _, err := publicupload.New(&fakeLoader{}, config); err == nil {
		t.Fatal("typed nil group authorization dependency was accepted")
	}
}

func TestUploadOperationRequiresWriteAccessToDestinationAndFileSetGroups(t *testing.T) {
	api := &fakeLoader{set: loader.FileSet{ID: fileSetID, ManagementGroup: "asset_index", Revision: 3}}
	authorizer := &fakeGroupAuthorizer{deniedGroups: map[string]bool{"public_uploads": true}}
	operation := newOperationWithAuthorizer(t, api, authorizer)
	config := json.RawMessage(`{"enabled":true,"access_group":"public_uploads","file_set_id":"` + fileSetID + `","max_bytes":8,"allowed_content_types":["application/octet-stream"]}`)
	registry, err := routerhttp.NewPublicOperationRegistry(operation)
	if err != nil {
		t.Fatal(err)
	}
	target := engine.RouteTarget{Kind: engine.TargetRegisteredOperation, OperationID: publicupload.OperationID, OperationConfig: config}

	if err := registry.ValidatePublicOperationTarget(context.Background(), target); !errors.Is(err, engine.ErrForbidden) {
		t.Fatalf("route was accepted without write access to its upload group: %v", err)
	}
	if len(authorizer.checkedGroups) != 1 || authorizer.checkedGroups[0] != "public_uploads" {
		t.Fatalf("upload group was not checked first: %v", authorizer.checkedGroups)
	}

	authorizer.deniedGroups = map[string]bool{"asset_index": true}
	authorizer.checkedGroups = nil
	if err := registry.ValidatePublicOperationTarget(context.Background(), target); !errors.Is(err, engine.ErrForbidden) {
		t.Fatalf("route was accepted without write access to its FileSet management group: %v", err)
	}
	if strings.Join(authorizer.checkedGroups, ",") != "public_uploads,asset_index" {
		t.Fatalf("expected both groups to be checked in order, got %v", authorizer.checkedGroups)
	}
}

func TestUploadOperationChecksCurrentGroupWriteAccessBeforeStaging(t *testing.T) {
	api := &fakeLoader{}
	stagingDirectory := privateTempDir(t)
	authorizer := &fakeGroupAuthorizer{deniedGroups: map[string]bool{"public_uploads": true}}
	operation := newOperationWithAuthorizer(t, api, authorizer, stagingDirectory)
	config := json.RawMessage(`{"enabled":true,"access_group":"public_uploads","max_bytes":8,"allowed_content_types":["application/octet-stream"]}`)
	request := engine.PublicOperationRequest{Method: "POST", Host: "forms.example", EscapedPath: "/upload", ContentType: "application/octet-stream", ContentLength: 3, IdempotencyKey: "visitor-upload-request-denied", Body: strings.NewReader("abc")}

	if _, err := operation.Execute(context.Background(), config, request); !errors.Is(err, engine.ErrUnavailable) {
		t.Fatalf("upload without current group write access was not denied opaquely: %v", err)
	}
	entries, err := os.ReadDir(stagingDirectory)
	if err != nil || len(entries) != 0 || len(api.uploads) != 0 {
		t.Fatalf("denied upload staged or created data: entries=%v uploads=%v err=%v", entries, api.uploads, err)
	}
}

func TestUploadOperationRejectsInvalidAndOversizedRequestsBeforeCreatingUpload(t *testing.T) {
	api := &fakeLoader{}
	operation := newOperation(t, api)
	config := json.RawMessage(`{"enabled":true,"access_group":"public_uploads","max_bytes":8,"allowed_content_types":["application/octet-stream"]}`)
	for _, request := range []engine.PublicOperationRequest{
		{Method: "POST", Host: "forms.example", EscapedPath: "/upload", ContentType: "application/octet-stream", ContentLength: 9, IdempotencyKey: "visitor-upload-request-0001", Body: strings.NewReader("123456789")},
		{Method: "POST", Host: "forms.example", EscapedPath: "/upload", ContentType: "application/octet-stream", ContentLength: -1, IdempotencyKey: "visitor-upload-request-0002", Body: strings.NewReader("123456789")},
		{Method: "POST", Host: "forms.example", EscapedPath: "/upload", ContentType: "text/plain", ContentLength: 1, IdempotencyKey: "visitor-upload-request-0003", Body: strings.NewReader("x")},
		{Method: "POST", Host: "forms.example", EscapedPath: "/upload", ContentType: "application/octet-stream", ContentLength: 1, IdempotencyKey: "bad key", Body: strings.NewReader("x")},
		{Method: "GET", Host: "forms.example", EscapedPath: "/upload", ContentType: "application/octet-stream", ContentLength: 1, IdempotencyKey: "visitor-upload-request-0004", Body: strings.NewReader("x")},
	} {
		if _, err := operation.Execute(context.Background(), config, request); err == nil {
			t.Errorf("invalid request was accepted: %+v", request)
		}
	}
	if len(api.uploads) != 0 {
		t.Fatalf("invalid requests reached Loader: %+v", api.uploads)
	}
}

func TestDispatchPreservesStreamingSizeLimitAsPayloadTooLarge(t *testing.T) {
	api := &fakeLoader{}
	operation := newOperation(t, api)
	catalog, err := routerhttp.NewPublicOperationRegistry(operation)
	if err != nil {
		t.Fatal(err)
	}
	config := json.RawMessage(`{"enabled":true,"access_group":"public_uploads","max_bytes":32,"allowed_content_types":["application/octet-stream"]}`)
	target := engine.RouteTarget{Kind: engine.TargetRegisteredOperation, OperationID: publicupload.OperationID, OperationConfig: config}
	request := engine.PublicOperationRequest{
		Method: "POST", Host: "forms.example", EscapedPath: "/upload", ContentType: "application/octet-stream",
		ContentLength: -1, IdempotencyKey: "visitor-upload-request-0010", Body: strings.NewReader(strings.Repeat("x", 33)),
	}
	if _, err := routerhttp.DispatchPublicOperation(context.Background(), catalog, target, request); !errors.Is(err, engine.ErrTooLarge) {
		t.Fatalf("stream overflow returned %v, want too-large", err)
	}
	if len(api.uploads) != 0 || api.commits != 0 {
		t.Fatalf("overflow reached Loader: uploads=%v commits=%d", api.uploads, api.commits)
	}
}

func TestUploadOperationDoesNotCommitAfterStagingReadFailure(t *testing.T) {
	api := &fakeLoader{}
	stagingDirectory := privateTempDir(t)
	operation, err := publicupload.New(api, publicupload.Config{
		Credential: "server-only-secret", StagingDirectory: stagingDirectory, MaxFileBytes: 32,
		PartSize: 4, MaxParts: 8, MaxConcurrentUploads: 2, MaxStagedBytes: 64,
		AllowedContentTypes: []string{"application/octet-stream"}, GroupAuthorization: &fakeGroupAuthorizer{},
	})
	if err != nil {
		t.Fatal(err)
	}
	config := json.RawMessage(`{"enabled":true,"access_group":"public_uploads","max_bytes":8,"allowed_content_types":["application/octet-stream"]}`)
	request := engine.PublicOperationRequest{
		Method: "POST", Host: "forms.example", EscapedPath: "/upload", ContentType: "application/octet-stream", ContentLength: -1,
		IdempotencyKey: "visitor-upload-request-0005", Body: failingReader{},
	}
	if _, err := operation.Execute(context.Background(), config, request); err == nil {
		t.Fatal("body read error was treated as a successful upload")
	}
	if len(api.uploads) != 0 || api.commits != 0 {
		t.Fatalf("Loader started a file before request body was verified: uploads=%+v commits=%d", api.uploads, api.commits)
	}
	entries, err := os.ReadDir(stagingDirectory)
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed staged body was not removed: entries=%v err=%v", entries, err)
	}
}

func TestUploadOperationFailsBeforeStagingWhenServerCredentialCannotAuthenticate(t *testing.T) {
	api := &fakeLoader{authErr: errors.New("credential rejected")}
	stagingDirectory := privateTempDir(t)
	operation, err := publicupload.New(api, publicupload.Config{
		Credential: "server-only-secret", StagingDirectory: stagingDirectory, MaxFileBytes: 32,
		PartSize: 4, MaxParts: 8, MaxConcurrentUploads: 2, MaxStagedBytes: 64,
		AllowedContentTypes: []string{"application/octet-stream"}, GroupAuthorization: &fakeGroupAuthorizer{},
	})
	if err != nil {
		t.Fatal(err)
	}
	config := json.RawMessage(`{"enabled":true,"access_group":"public_uploads","max_bytes":8,"allowed_content_types":["application/octet-stream"]}`)
	request := engine.PublicOperationRequest{Method: "POST", Host: "forms.example", EscapedPath: "/upload", ContentType: "application/octet-stream", ContentLength: 3, IdempotencyKey: "visitor-upload-request-0007", Body: strings.NewReader("abc")}
	if _, err := operation.Execute(context.Background(), config, request); err == nil {
		t.Fatal("upload succeeded without an authenticated Loader identity")
	}
	entries, err := os.ReadDir(stagingDirectory)
	if err != nil || len(entries) != 0 || len(api.uploads) != 0 {
		t.Fatalf("invalid server identity staged or uploaded data: entries=%v uploads=%v err=%v", entries, api.uploads, err)
	}
}

func TestUploadOperationLeavesCommittedUploadRetryableWhenSetIndexFails(t *testing.T) {
	api := &fakeLoader{set: loader.FileSet{ID: fileSetID, ManagementGroup: "asset_index", Revision: 1}, addErr: errors.New("temporary index outage")}
	operation := newOperation(t, api)
	config := json.RawMessage(`{"enabled":true,"access_group":"public_uploads","file_set_id":"` + fileSetID + `","max_bytes":8,"allowed_content_types":["application/octet-stream"]}`)
	request := engine.PublicOperationRequest{Method: "POST", Host: "forms.example", EscapedPath: "/upload", ContentType: "application/octet-stream", ContentLength: 3, IdempotencyKey: "visitor-upload-request-0006", Body: strings.NewReader("abc")}
	if _, err := operation.Execute(context.Background(), config, request); err == nil {
		t.Fatal("FileSet indexing failure was reported as a successful upload")
	}
	if api.commits != 1 {
		t.Fatalf("file should be committed exactly once before an index retry; commits=%d", api.commits)
	}
	api.addErr = nil
	request.Body = strings.NewReader("abc")
	response, err := operation.Execute(context.Background(), config, request)
	if err != nil {
		t.Fatalf("idempotent retry failed: %v", err)
	}
	response.Body.Close()
	if api.commits != 2 {
		t.Fatalf("retry should re-enter idempotent Loader commit, got %d commits", api.commits)
	}
}

func TestUploadOperationRejectsDifferentBytesForAnExistingIdempotencyKey(t *testing.T) {
	api := &fakeLoader{}
	operation := newOperation(t, api)
	config := json.RawMessage(`{"enabled":true,"access_group":"public_uploads","max_bytes":8,"allowed_content_types":["application/octet-stream"]}`)
	request := engine.PublicOperationRequest{Method: "POST", Host: "forms.example", EscapedPath: "/upload", ContentType: "application/octet-stream", ContentLength: 3, IdempotencyKey: "visitor-upload-request-0008", Body: strings.NewReader("abc")}
	response, err := operation.Execute(context.Background(), config, request)
	if err != nil {
		t.Fatalf("initial upload failed: %v", err)
	}
	response.Body.Close()
	request.Body = strings.NewReader("xyz")
	if _, err := operation.Execute(context.Background(), config, request); !errors.Is(err, engine.ErrConflict) {
		t.Fatalf("changed bytes with the same key returned %v, want conflict", err)
	}
	if api.commits != 1 {
		t.Fatalf("conflicting retry created another file: commits=%d", api.commits)
	}
}

func TestUploadOperationRetriesFileSetRevisionConflict(t *testing.T) {
	api := &fakeLoader{set: loader.FileSet{ID: fileSetID, ManagementGroup: "asset_index", Revision: 1}, conflictsRemaining: 1}
	operation := newOperation(t, api)
	config := json.RawMessage(`{"enabled":true,"access_group":"public_uploads","file_set_id":"` + fileSetID + `","max_bytes":8,"allowed_content_types":["application/octet-stream"]}`)
	request := engine.PublicOperationRequest{Method: "POST", Host: "forms.example", EscapedPath: "/upload", ContentType: "application/octet-stream", ContentLength: 3, IdempotencyKey: "visitor-upload-request-0009", Body: strings.NewReader("abc")}
	response, err := operation.Execute(context.Background(), config, request)
	if err != nil {
		t.Fatalf("FileSet revision conflict was not retried: %v", err)
	}
	response.Body.Close()
	if api.addCalls != 2 || api.commits != 1 {
		t.Fatalf("unexpected retry lifecycle: additions=%d commits=%d", api.addCalls, api.commits)
	}
}

func TestUploadOperationRejectsActiveContentTypesAndInsufficientLoaderCapacity(t *testing.T) {
	base := publicupload.Config{
		Credential: "server-only-secret", StagingDirectory: privateTempDir(t), MaxFileBytes: 33,
		PartSize: 4, MaxParts: 8, MaxConcurrentUploads: 1, MaxStagedBytes: 33,
		AllowedContentTypes: []string{"application/octet-stream"}, GroupAuthorization: &fakeGroupAuthorizer{},
	}
	if _, err := publicupload.New(&fakeLoader{}, base); err == nil {
		t.Fatal("configured upload size above Loader part capacity was accepted")
	}
	base.MaxFileBytes = 32
	base.MaxStagedBytes = 32
	base.AllowedContentTypes = []string{"text/html"}
	if _, err := publicupload.New(&fakeLoader{}, base); err == nil {
		t.Fatal("active HTML content was allowed on a public upload operation")
	}
	base.AllowedContentTypes = []string{"application/octet-stream"}
	base.MaxConcurrentUploads = 2
	base.MaxStagedBytes = 63
	if _, err := publicupload.New(&fakeLoader{}, base); err == nil {
		t.Fatal("concurrent upload capacity above the staging disk budget was accepted")
	}
}

func TestUploadOperationRequiresPrivateNonSymlinkStagingDirectory(t *testing.T) {
	base := publicupload.Config{
		Credential: "server-only-secret", MaxFileBytes: 32, PartSize: 4, MaxParts: 8,
		MaxConcurrentUploads: 1, MaxStagedBytes: 32, AllowedContentTypes: []string{"application/octet-stream"}, GroupAuthorization: &fakeGroupAuthorizer{},
	}
	base.StagingDirectory = "relative/staging"
	if _, err := publicupload.New(&fakeLoader{}, base); err == nil {
		t.Fatal("relative staging directory was accepted")
	}
	readOnlyDirectory := privateTempDir(t)
	if err := os.Chmod(readOnlyDirectory, 0o500); err != nil {
		t.Fatal(err)
	}
	base.StagingDirectory = readOnlyDirectory
	if _, err := publicupload.New(&fakeLoader{}, base); err == nil {
		t.Fatal("read-only staging directory was accepted")
	}
	sharedDirectory := t.TempDir()
	if err := os.Chmod(sharedDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	base.StagingDirectory = sharedDirectory
	if _, err := publicupload.New(&fakeLoader{}, base); err == nil {
		t.Fatal("group/world-readable staging directory was accepted")
	}
	privateDirectory := privateTempDir(t)
	link := filepath.Join(t.TempDir(), "staging-link")
	if err := os.Symlink(privateDirectory, link); err != nil {
		t.Fatal(err)
	}
	base.StagingDirectory = link
	if _, err := publicupload.New(&fakeLoader{}, base); err == nil {
		t.Fatal("symlink staging directory was accepted")
	}
}

type fakeLoader struct {
	credential         string
	authErr            error
	uploads            []loader.BeginUploadInput
	byIdempotency      map[string]loader.BeginUploadInput
	parts              []string
	commits            int
	abortCalls         int
	set                loader.FileSet
	addedSetID         string
	addedFileID        string
	addedRevision      int64
	addCalls           int
	statErr            error
	deleteErr          error
	deleteCalls        int
	conflictsRemaining int
	addErr             error
}

func (f *fakeLoader) Authenticate(ctx context.Context, credential string) (context.Context, error) {
	f.credential = credential
	return ctx, f.authErr
}
func (f *fakeLoader) BeginUpload(_ context.Context, input loader.BeginUploadInput) (loader.UploadSession, error) {
	if f.byIdempotency == nil {
		f.byIdempotency = make(map[string]loader.BeginUploadInput)
	}
	if existing, ok := f.byIdempotency[input.IdempotencyKey]; ok {
		if existing.Name != input.Name || existing.ContentType != input.ContentType || existing.Size != input.Size || existing.SHA256 != input.SHA256 || strings.Join(existing.Groups, ",") != strings.Join(input.Groups, ",") {
			return loader.UploadSession{}, loader.ErrConflict
		}
		return loader.UploadSession{ID: uploadID, State: "uploading", PartSize: 4}, nil
	}
	f.byIdempotency[input.IdempotencyKey] = input
	f.uploads = append(f.uploads, input)
	return loader.UploadSession{ID: uploadID, State: "uploading", PartSize: 4}, nil
}
func (f *fakeLoader) PutUploadPart(_ context.Context, _ string, _ int, size int64, _ string, body io.Reader) (loader.UploadSession, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return loader.UploadSession{}, err
	}
	if int64(len(data)) != size {
		return loader.UploadSession{}, errors.New("wrong part size")
	}
	f.parts = append(f.parts, string(data))
	return loader.UploadSession{ID: uploadID, State: "uploading", PartSize: 4}, nil
}
func (f *fakeLoader) CommitUpload(context.Context, string) (loader.File, error) {
	f.commits++
	return loader.File{ID: fileID, Name: "server-generated", Revision: 1, ContentType: "application/octet-stream", Size: 3, SHA256: digest("abc")}, nil
}
func (f *fakeLoader) AbortUpload(context.Context, string) error {
	f.abortCalls++
	return nil
}
func (f *fakeLoader) StatFile(_ context.Context, id string) (loader.File, error) {
	if f.statErr != nil {
		return loader.File{}, f.statErr
	}
	return loader.File{ID: id, Revision: 1}, nil
}
func (f *fakeLoader) DeleteFile(_ context.Context, _ string, _ int64) error {
	f.deleteCalls++
	return f.deleteErr
}
func (f *fakeLoader) GetFileSet(_ context.Context, id string) (loader.FileSet, error) {
	if id != f.set.ID {
		return loader.FileSet{}, loader.ErrNotFound
	}
	return f.set, nil
}
func (f *fakeLoader) AddFileToSet(_ context.Context, setID, id string, revision int64) (loader.FileSet, error) {
	f.addCalls++
	f.addedSetID, f.addedFileID, f.addedRevision = setID, id, revision
	if f.conflictsRemaining > 0 {
		f.conflictsRemaining--
		f.set.Revision++
		return loader.FileSet{}, loader.ErrConflict
	}
	if f.addErr != nil {
		return loader.FileSet{}, f.addErr
	}
	f.set.Revision++
	return f.set, nil
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("injected body read failure") }

type checkingReader struct {
	t         *testing.T
	directory string
	data      []byte
	checked   bool
}

func (reader *checkingReader) Read(buffer []byte) (int, error) {
	reader.t.Helper()
	if !reader.checked {
		entries, err := os.ReadDir(reader.directory)
		if err != nil || len(entries) != 1 {
			reader.t.Fatalf("expected exactly one staged file before body read: entries=%v err=%v", entries, err)
		}
		info, err := entries[0].Info()
		if err != nil {
			reader.t.Fatalf("cannot inspect staged file: %v", err)
		}
		if info.Mode().Perm() != 0o600 || strings.Contains(entries[0].Name(), "pngdata") {
			reader.t.Fatalf("staged file was not private or contained visitor data in its name: file=%s mode=%v", entries[0].Name(), info.Mode().Perm())
		}
		reader.checked = true
	}
	if len(reader.data) == 0 {
		return 0, io.EOF
	}
	count := copy(buffer, reader.data)
	reader.data = reader.data[count:]
	return count, nil
}

func newOperation(t *testing.T, api publicupload.Loader) *publicupload.Operation {
	return newOperationWithAuthorizer(t, api, &fakeGroupAuthorizer{})
}

func newOperationWithAuthorizer(t *testing.T, api publicupload.Loader, authorizer *fakeGroupAuthorizer, staging ...string) *publicupload.Operation {
	t.Helper()
	stagingDirectory := privateTempDir(t)
	if len(staging) > 0 {
		stagingDirectory = staging[0]
	}
	operation, err := publicupload.New(api, publicupload.Config{
		Credential: "server-only-secret", StagingDirectory: stagingDirectory, MaxFileBytes: 32,
		PartSize: 4, MaxParts: 8, MaxConcurrentUploads: 2, MaxStagedBytes: 64,
		AllowedContentTypes: []string{"application/octet-stream"}, GroupAuthorization: authorizer,
	})
	if err != nil {
		t.Fatal(err)
	}
	return operation
}

func privateTempDir(t *testing.T) string {
	t.Helper()
	path := t.TempDir()
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

type fakeGroupAuthorizer struct {
	deniedGroups  map[string]bool
	checkedGroups []string
	err           error
}

func (*fakeGroupAuthorizer) Authenticate(context.Context, string) (core.Session, core.Principal, error) {
	return core.Session{}, core.Principal{Active: true}, nil
}

func (authorizer *fakeGroupAuthorizer) RequireGroupLevel(_ context.Context, _ core.Session, groups []string, level core.AccessLevel) error {
	if level != core.AccessLevelWrite || len(groups) != 1 {
		return errors.New("unexpected access check")
	}
	authorizer.checkedGroups = append(authorizer.checkedGroups, groups[0])
	if authorizer.err != nil {
		return authorizer.err
	}
	if authorizer.deniedGroups[groups[0]] {
		return core.ErrForbidden
	}
	return nil
}

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
