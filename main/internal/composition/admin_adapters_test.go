package composition

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/Zubrium/cdn/main/internal/bus"
	"github.com/Zubrium/cdn/pkg/contracts"
)

type adminTestContentEngine struct {
	bus.AdminContentEngine
	replace contracts.AdminContentReplaceLocaleConfigRequest
	save    contracts.AdminContentSaveWorkspaceDraftRequest
	err     error
}

func (engine *adminTestContentEngine) Authenticate(_ context.Context, credential string) (context.Context, error) {
	if credential == "" {
		return nil, contracts.ErrAdminContentUnauthorized
	}
	return context.Background(), nil
}

func (engine *adminTestContentEngine) AuthenticateSession(_ context.Context, token string) (context.Context, error) {
	if token == "" {
		return nil, contracts.ErrAdminContentUnauthorized
	}
	return context.Background(), nil
}

func (engine *adminTestContentEngine) ReplaceLocaleConfig(_ context.Context, request contracts.AdminContentReplaceLocaleConfigRequest) (contracts.AdminContentLocaleConfig, error) {
	engine.replace = request
	if engine.err != nil {
		return contracts.AdminContentLocaleConfig{}, engine.err
	}
	return contracts.AdminContentLocaleConfig{Revision: 1, DefaultLocale: request.DefaultLocale, Locales: request.Locales}, nil
}

func (engine *adminTestContentEngine) SaveWorkspaceDraft(_ context.Context, _ contracts.AdminContentProjections, request contracts.AdminContentSaveWorkspaceDraftRequest) (contracts.AdminContentWorkspace, error) {
	engine.save = request
	return contracts.AdminContentWorkspace{SnapshotID: request.SnapshotID, Revision: request.ExpectedRevision}, nil
}

type adminTestFormsEngine struct {
	bus.AdminFormsEngine
	status contracts.AdminFormsSetSubmissionStatusRequest
}

func (engine *adminTestFormsEngine) Authenticate(_ context.Context, credential string) (context.Context, error) {
	if credential == "" {
		return nil, contracts.ErrAdminFormsUnauthorized
	}
	return context.Background(), nil
}

func (engine *adminTestFormsEngine) AuthenticateSession(_ context.Context, token string) (context.Context, error) {
	if token == "" {
		return nil, contracts.ErrAdminFormsUnauthorized
	}
	return context.Background(), nil
}

func (engine *adminTestFormsEngine) SetSubmissionStatus(_ context.Context, request contracts.AdminFormsSetSubmissionStatusRequest) (contracts.AdminFormsSubmission, error) {
	engine.status = request
	return contracts.AdminFormsSubmission{ID: request.SubmissionID, State: request.Status, Revision: request.ExpectedRevision}, nil
}

type adminTestWebsiteEngine struct {
	bus.AdminWebsiteEngine
	deleteTemplate contracts.AdminWebsiteDeletePageTemplateRequest
	createSnapshot contracts.AdminWebsiteCreateSnapshotRequest
}

func (engine *adminTestWebsiteEngine) Authenticate(_ context.Context, credential string) (context.Context, error) {
	if credential == "" {
		return nil, contracts.ErrAdminWebsiteUnauthorized
	}
	return context.Background(), nil
}

func (engine *adminTestWebsiteEngine) AuthenticateSession(_ context.Context, token string) (context.Context, error) {
	if token == "" {
		return nil, contracts.ErrAdminWebsiteUnauthorized
	}
	return context.Background(), nil
}

func (engine *adminTestWebsiteEngine) DeletePageTemplate(_ context.Context, request contracts.AdminWebsiteDeletePageTemplateRequest) error {
	engine.deleteTemplate = request
	return nil
}

func (engine *adminTestWebsiteEngine) CreateSnapshot(_ context.Context, request contracts.AdminWebsiteCreateSnapshotRequest) (contracts.AdminWebsiteSnapshot, error) {
	engine.createSnapshot = request
	return contracts.AdminWebsiteSnapshot{ID: "snapshot", PageID: request.PageID, Name: request.Name}, nil
}

type adminTestFilesEngine struct {
	contracts.AdminFilesAPI
	begin       contracts.AdminFileBeginUploadInput
	part        []byte
	readPayload []byte
}

func (engine *adminTestFilesEngine) Authenticate(_ context.Context, credential string) (context.Context, error) {
	if credential == "" {
		return nil, contracts.ErrAdminFilesUnauthorized
	}
	return context.Background(), nil
}

func (engine *adminTestFilesEngine) AuthenticateSession(_ context.Context, token string) (context.Context, error) {
	if token == "" {
		return nil, contracts.ErrAdminFilesUnauthorized
	}
	return context.Background(), nil
}

func (engine *adminTestFilesEngine) BeginUpload(_ context.Context, input contracts.AdminFileBeginUploadInput) (contracts.AdminFileUploadSession, error) {
	engine.begin = input
	return contracts.AdminFileUploadSession{ID: "upload", State: "uploading"}, nil
}

func (engine *adminTestFilesEngine) PutUploadPart(_ context.Context, _ string, _ int, _ int64, _ string, body io.Reader) (contracts.AdminFileUploadSession, error) {
	payload, err := io.ReadAll(body)
	if err != nil {
		return contracts.AdminFileUploadSession{}, err
	}
	engine.part = payload
	return contracts.AdminFileUploadSession{ID: "upload", State: "uploading"}, nil
}

func (engine *adminTestFilesEngine) ReadFile(_ context.Context, id string) (io.ReadCloser, contracts.AdminFile, error) {
	return io.NopCloser(bytes.NewReader(engine.readPayload)), contracts.AdminFile{ID: id, Size: int64(len(engine.readPayload))}, nil
}

func adminTestBus(t *testing.T, content *adminTestContentEngine, forms *adminTestFormsEngine,
	website *adminTestWebsiteEngine, files *adminTestFilesEngine) contracts.Dispatcher {
	t.Helper()
	dispatcher, err := bus.NewAdminEngineBus(bus.ConcurrencyLimits{MaxInFlight: 8}, bus.AdminEngineProviders{
		Content: func() bus.AdminContentEngine { return content },
		Forms:   func() bus.AdminFormsEngine { return forms },
		Website: func() bus.AdminWebsiteEngine { return website },
		Files:   func() contracts.AdminFilesAPI { return files },
	})
	if err != nil {
		t.Fatal(err)
	}
	return dispatcher
}

func adminTestContext() context.Context {
	return contracts.WithAdminSubject(context.Background(), contracts.AuthorizationSubject{Credential: "test-credential"})
}

func TestAdminAdaptersCarryServerOnlyFields(t *testing.T) {
	content := &adminTestContentEngine{}
	forms := &adminTestFormsEngine{}
	website := &adminTestWebsiteEngine{}
	files := &adminTestFilesEngine{readPayload: []byte("file-payload")}
	dispatcher := adminTestBus(t, content, forms, website, files)
	ctx := adminTestContext()

	if _, err := (&adminContentAdapter{dispatcher: dispatcher}).ReplaceLocaleConfig(ctx, contracts.AdminContentReplaceLocaleConfigRequest{
		Locales: []string{"en"}, DefaultLocale: "en", IdempotencyKey: "idem-locale-123456",
	}); err != nil {
		t.Fatal(err)
	}
	if content.replace.IdempotencyKey != "idem-locale-123456" {
		t.Fatalf("locale idempotency key was lost: %+v", content.replace)
	}
	if _, err := (&adminContentAdapter{dispatcher: dispatcher}).SaveWorkspaceDraft(ctx, contracts.AdminContentProjections{}, contracts.AdminContentSaveWorkspaceDraftRequest{
		SnapshotID: "snapshot-1", IdempotencyKey: "idem-save-123456", ExpectedRevision: 2,
	}); err != nil {
		t.Fatal(err)
	}
	if content.save.SnapshotID != "snapshot-1" || content.save.IdempotencyKey != "idem-save-123456" {
		t.Fatalf("workspace server-only fields were lost: %+v", content.save)
	}
	if _, err := (&adminFormsAdapter{dispatcher: dispatcher}).SetSubmissionStatus(ctx, contracts.AdminFormsSetSubmissionStatusRequest{
		SubmissionID: "submission-1", Status: "approved", ExpectedRevision: 9,
	}); err != nil {
		t.Fatal(err)
	}
	if forms.status.SubmissionID != "submission-1" || forms.status.ExpectedRevision != 9 {
		t.Fatalf("forms CAS fields were lost: %+v", forms.status)
	}
	if err := (&adminWebsiteAdapter{dispatcher: dispatcher}).DeletePageTemplate(ctx, contracts.AdminWebsiteDeletePageTemplateRequest{
		TemplateID: "template-1", ExpectedRevision: 12,
	}); err != nil {
		t.Fatal(err)
	}
	if website.deleteTemplate.TemplateID != "template-1" || website.deleteTemplate.ExpectedRevision != 12 {
		t.Fatalf("website template CAS fields were lost: %+v", website.deleteTemplate)
	}
	if _, err := (&adminWebsiteAdapter{dispatcher: dispatcher}).CreateSnapshot(ctx, contracts.AdminWebsiteCreateSnapshotRequest{
		PageID: "page-1", Name: "v1", IdempotencyKey: "idem-snapshot-1234",
	}); err != nil {
		t.Fatal(err)
	}
	if website.createSnapshot.IdempotencyKey != "idem-snapshot-1234" {
		t.Fatalf("snapshot idempotency key was lost: %+v", website.createSnapshot)
	}
}

func TestAdminFilesAdapterPreservesResumableStreams(t *testing.T) {
	files := &adminTestFilesEngine{readPayload: []byte("file-payload")}
	dispatcher := adminTestBus(t, &adminTestContentEngine{}, &adminTestFormsEngine{}, &adminTestWebsiteEngine{}, files)
	adapter := &adminFilesAdapter{dispatcher: dispatcher}
	ctx := adminTestContext()

	if _, err := adapter.BeginUpload(ctx, contracts.AdminFileBeginUploadInput{
		Name: "report.txt", ContentType: "text/plain", Size: 5, SHA256: "sha256", IdempotencyKey: "idem-upload-123456",
	}); err != nil {
		t.Fatal(err)
	}
	if files.begin.IdempotencyKey != "idem-upload-123456" {
		t.Fatalf("upload idempotency key was lost: %+v", files.begin)
	}
	if _, err := adapter.PutUploadPart(ctx, "upload", 1, 5, "sha256", strings.NewReader("chunk")); err != nil {
		t.Fatal(err)
	}
	if string(files.part) != "chunk" {
		t.Fatalf("part body was not streamed: %q", files.part)
	}
	reader, file, err := adapter.ReadFile(ctx, "file-1")
	if err != nil {
		t.Fatal(err)
	}
	payload, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if string(payload) != "file-payload" || file.ID != "file-1" {
		t.Fatalf("read body was not streamed: %q %+v", payload, file)
	}
}

func TestAdminAdaptersPreserveNeutralEngineErrors(t *testing.T) {
	content := &adminTestContentEngine{err: contracts.ErrAdminContentForbidden}
	dispatcher := adminTestBus(t, content, &adminTestFormsEngine{}, &adminTestWebsiteEngine{}, &adminTestFilesEngine{})
	_, err := (&adminContentAdapter{dispatcher: dispatcher}).ReplaceLocaleConfig(adminTestContext(), contracts.AdminContentReplaceLocaleConfigRequest{
		Locales: []string{"en"}, DefaultLocale: "en",
	})
	if !errors.Is(err, contracts.ErrAdminContentForbidden) {
		t.Fatalf("expected neutral forbidden error, got %v", err)
	}
}

func TestAdminAdaptersRejectMissingAuthenticatedSubject(t *testing.T) {
	dispatcher := adminTestBus(t, &adminTestContentEngine{}, &adminTestFormsEngine{}, &adminTestWebsiteEngine{}, &adminTestFilesEngine{})
	if _, err := (&adminContentAdapter{dispatcher: dispatcher}).GetLocaleConfig(context.Background()); !errors.Is(err, contracts.ErrAdminContentUnauthorized) {
		t.Fatalf("expected neutral unauthorized, got %v", err)
	}
	if _, err := (&adminFormsAdapter{dispatcher: dispatcher}).ListSchemas(context.Background(), "", 1); !errors.Is(err, contracts.ErrAdminFormsUnauthorized) {
		t.Fatalf("expected neutral unauthorized, got %v", err)
	}
	if _, err := (&adminFilesAdapter{dispatcher: dispatcher}).StatFile(context.Background(), "file-1"); !errors.Is(err, contracts.ErrAdminFilesUnauthorized) {
		t.Fatalf("expected neutral unauthorized, got %v", err)
	}
}

type adminTestUnderlyingDispatcher struct{ module string }

func (dispatcher *adminTestUnderlyingDispatcher) Dispatch(ctx context.Context, _ contracts.Envelope) (contracts.Envelope, error) {
	dispatcher.module = bus.ModuleIDFromContext(ctx)
	return contracts.Envelope{}, nil
}

func TestAdminEngineDispatcherGuard(t *testing.T) {
	underlying := &adminTestUnderlyingDispatcher{}
	guarded := bus.NewAdminEngineDispatcher(underlying)
	if _, err := guarded.Dispatch(context.Background(), contracts.Envelope{
		OperationID: contracts.AdminFilesStatFileOperation().Key.ID,
		Version:     1,
		Payload:     json.RawMessage(`{}`),
	}); err != nil {
		t.Fatal(err)
	}
	if underlying.module != bus.AdminEngineCallerModuleID {
		t.Fatalf("unexpected module identity: %q", underlying.module)
	}
	if _, err := guarded.Dispatch(context.Background(), contracts.Envelope{
		OperationID: "core.admin.principal.create",
		Version:     1,
		Payload:     json.RawMessage(`{}`),
	}); !errors.Is(err, bus.ErrOperationNotAllowedForModule) {
		t.Fatalf("expected disallowed operation, got %v", err)
	}
}

func TestAdminEngineBusOperationKeysAreExact(t *testing.T) {
	keys := bus.AdminEngineOperationKeys()
	counts := map[string]int{}
	unique := map[contracts.Key]struct{}{}
	for _, key := range keys {
		if _, duplicate := unique[key]; duplicate {
			t.Fatalf("duplicate Admin engine key %s/v%d", key.ID, key.Version)
		}
		unique[key] = struct{}{}
		if key.Version != 1 {
			t.Fatalf("Admin engine key %s has version %d, want 1", key.ID, key.Version)
		}
		switch {
		case strings.HasPrefix(key.ID, "admin.content."):
			counts["admin.content."]++
		case strings.HasPrefix(key.ID, "admin.forms."):
			counts["admin.forms."]++
		case strings.HasPrefix(key.ID, "admin.website."):
			counts["admin.website."]++
		case strings.HasPrefix(key.ID, "admin.files."):
			counts["admin.files."]++
		default:
			t.Fatalf("Admin engine bus contains foreign key %s/v%d", key.ID, key.Version)
		}
	}
	for prefix, want := range map[string]int{
		"admin.content.": 9, "admin.forms.": 9, "admin.website.": 27, "admin.files.": 22,
	} {
		if counts[prefix] != want {
			t.Errorf("%s has %d keys, want %d", prefix, counts[prefix], want)
		}
	}
	if len(keys) != 67 {
		t.Fatalf("Admin engine bus has %d keys, want 67", len(keys))
	}

	dispatcher := adminTestBus(t, &adminTestContentEngine{}, &adminTestFormsEngine{}, &adminTestWebsiteEngine{}, &adminTestFilesEngine{})
	for _, key := range keys {
		_, err := dispatcher.Dispatch(adminTestContext(), contracts.Envelope{
			OperationID: key.ID, Version: key.Version, Payload: json.RawMessage(`{}`),
		})
		if errors.Is(err, bus.ErrOperationNotAllowedForModule) || errors.Is(err, bus.ErrOperationNotAllowed) || errors.Is(err, bus.ErrHandlerNotFound) {
			t.Errorf("Admin engine key %s/v%d is not registered on the DataBus: %v", key.ID, key.Version, err)
		}
	}
	if _, err := dispatcher.Dispatch(adminTestContext(), contracts.Envelope{
		OperationID: contracts.CoreReadinessOperation().Key.ID,
		Version:     contracts.CoreReadinessOperation().Key.Version,
		Payload:     json.RawMessage(`{}`),
	}); !errors.Is(err, bus.ErrOperationNotAllowedForModule) {
		t.Fatalf("foreign DataBus operation was accepted by the Admin engine bus: %v", err)
	}
}
