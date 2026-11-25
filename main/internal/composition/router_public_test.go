package composition

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	loaderapi "github.com/Zubrium/cdn/engines/loader/features/files/pkg"
	"github.com/Zubrium/cdn/main/internal/bus"
	"github.com/Zubrium/cdn/pkg/contracts"
	"github.com/google/uuid"
)

type routerPublicTestForms struct{ routerPublicFormsUnavailable }

type routerPublicTestLoader struct {
	beginInput loaderapi.BeginUploadInput
	beginCalls int
	putCalls   int
}

func (loader *routerPublicTestLoader) Authenticate(context.Context, string) (context.Context, error) {
	return context.Background(), nil
}

func (loader *routerPublicTestLoader) BeginUpload(_ context.Context, input loaderapi.BeginUploadInput) (loaderapi.UploadSession, error) {
	loader.beginCalls++
	loader.beginInput = input
	return loaderapi.UploadSession{ID: uuid.NewString(), State: "uploading", PartSize: input.Size}, nil
}

func (loader *routerPublicTestLoader) PutUploadPart(context.Context, string, int, int64, string, io.Reader) (loaderapi.UploadSession, error) {
	loader.putCalls++
	return loaderapi.UploadSession{ID: uuid.NewString(), State: "uploading", PartSize: loader.beginInput.Size}, nil
}

func (loader *routerPublicTestLoader) CommitUpload(context.Context, string) (loaderapi.File, error) {
	return loaderapi.File{}, nil
}
func (loader *routerPublicTestLoader) AbortUpload(context.Context, string) error { return nil }
func (loader *routerPublicTestLoader) StatFile(context.Context, string) (loaderapi.File, error) {
	return loaderapi.File{}, nil
}
func (loader *routerPublicTestLoader) DeleteFile(context.Context, string, int64) error { return nil }
func (loader *routerPublicTestLoader) GetFileSet(context.Context, string) (loaderapi.FileSet, error) {
	return loaderapi.FileSet{}, nil
}
func (loader *routerPublicTestLoader) AddFileToSet(context.Context, string, string, int64) (loaderapi.FileSet, error) {
	return loaderapi.FileSet{}, nil
}

type routerPublicTestStaging struct {
	putCalls int
	objects  map[string][]byte
	checksum map[string]string
}

func newRouterPublicTestStaging() *routerPublicTestStaging {
	return &routerPublicTestStaging{objects: map[string][]byte{}, checksum: map[string]string{}}
}

func (staging *routerPublicTestStaging) PutStagedObject(_ context.Context, _ contracts.AuthorizationSubject, id string, _ []string,
	size int64, checksum string, body io.Reader) error {
	staging.putCalls++
	data, err := io.ReadAll(body)
	if err != nil || int64(len(data)) != size {
		return errors.New("invalid staged object")
	}
	staging.objects[id] = data
	staging.checksum[id] = checksum
	return nil
}

func (staging *routerPublicTestStaging) ReadStagedObject(_ context.Context, _ contracts.AuthorizationSubject, id string) (io.ReadCloser, int64, string, error) {
	data, ok := staging.objects[id]
	if !ok {
		return nil, 0, "", errors.New("staged object missing")
	}
	return io.NopCloser(bytes.NewReader(data)), int64(len(data)), staging.checksum[id], nil
}

func (staging *routerPublicTestStaging) PurgeStagedObject(_ context.Context, id string) error {
	delete(staging.objects, id)
	return nil
}

type routerPublicTestAuthorization struct{}

func (routerPublicTestAuthorization) Authenticate(context.Context, string) (contracts.AuthorizationSubject, contracts.Principal, error) {
	return contracts.AuthorizationSubject{Credential: "router"}, contracts.Principal{Active: true}, nil
}

func (routerPublicTestAuthorization) RequireGroupLevel(context.Context, contracts.AuthorizationSubject, []string, contracts.AccessLevel) error {
	return nil
}

func TestRouterPublicDispatcherRejectsIdentitySubstitution(t *testing.T) {
	key := contracts.PublicLoaderBeginUploadOperation().Key
	dispatcher, err := bus.NewAllowlisted(bus.ConcurrencyLimits{MaxInFlight: 2}, key)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	if err := bus.Register(dispatcher, contracts.PublicLoaderBeginUploadOperation(),
		func(ctx context.Context, _ contracts.PublicLoaderBeginUploadRequest) (contracts.PublicLoaderUploadSession, error) {
			calls++
			if routerPublicModuleID(ctx) != routerPublicLoaderModuleID {
				t.Errorf("module identity = %q", routerPublicModuleID(ctx))
			}
			return contracts.PublicLoaderUploadSession{}, nil
		}); err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.EnableScopedStreams(); err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Seal(); err != nil {
		t.Fatal(err)
	}
	wrapped := routerPublicModuleDispatcher{
		dispatcher: dispatcher,
		allowed:    map[contracts.Key]struct{}{key: {}},
	}
	base := map[string]any{
		"stream_ref": "ref", "name": "file", "idempotency_key": "1111111111111111",
		"content_type": "image/png", "declared_size": 1, "max_bytes": 10,
		"part_size": 1, "groups": []string{"uploads"},
	}
	for _, injected := range []map[string]string{
		{"module": "engine.forms"},
		{"class": "forms"},
		{"id": "override"},
		{"engine": "forms"},
		{"credential": "secret"},
	} {
		payload := make(map[string]any, len(base)+len(injected))
		for key, value := range base {
			payload[key] = value
		}
		for key, value := range injected {
			payload[key] = value
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		_, err = wrapped.Dispatch(context.Background(), contracts.Envelope{OperationID: key.ID, Version: key.Version, Payload: raw})
		if !errors.Is(err, contracts.ErrRequestTypeMismatch) {
			t.Fatalf("injected %v error = %v", injected, err)
		}
	}
	if calls != 0 {
		t.Fatalf("handler calls after injection = %d", calls)
	}
	raw, err := json.Marshal(base)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wrapped.Dispatch(context.Background(), contracts.Envelope{OperationID: key.ID, Version: key.Version, Payload: raw}); err != nil {
		t.Fatalf("valid dispatch: %v", err)
	}
	if calls != 1 {
		t.Fatalf("handler calls = %d", calls)
	}
}

func TestRouterPublicLoaderReadsStreamOnce(t *testing.T) {
	loader := &routerPublicTestLoader{}
	staging := newRouterPublicTestStaging()
	provider := &routerPublicLoaderProvider{
		api: loader, staging: staging, authorization: routerPublicTestAuthorization{}, credential: "router",
		partSize: 5, maxFileBytes: 100, maxParts: 4,
	}
	streams := bus.NewScopedStreams()
	ref, err := streams.AddReader(io.NopCloser(strings.NewReader("hello")))
	if err != nil {
		t.Fatal(err)
	}
	defer streams.Close()
	ctx := contracts.WithStreamCapability(
		context.WithValue(context.Background(), routerPublicModuleKey{}, routerPublicLoaderModuleID), streams)
	request := contracts.PublicLoaderBeginUploadRequest{
		Stream: ref, Name: "file", IdempotencyKey: "1111111111111111", ContentType: "image/png",
		DeclaredSize: 5, MaxBytes: 10, PartSize: 5, Groups: []string{"uploads"},
	}
	session, err := provider.beginUpload(ctx, request)
	if err != nil {
		t.Fatalf("first begin: %v", err)
	}
	if session.ID == "" || session.ExpectedParts != 1 || len(session.StagedParts) != 1 {
		t.Fatalf("session = %#v", session)
	}
	if staging.putCalls != 1 || loader.beginCalls != 1 || loader.putCalls != 1 {
		t.Fatalf("put=%d begin=%d putPart=%d", staging.putCalls, loader.beginCalls, loader.putCalls)
	}
	if loader.beginInput.Size != 5 || loader.beginInput.Name != "file" {
		t.Fatalf("begin input = %#v", loader.beginInput)
	}
	sum := sha256.Sum256([]byte("hello"))
	if loader.beginInput.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("sha256 = %s", loader.beginInput.SHA256)
	}
	_, err = provider.beginUpload(ctx, request)
	if !errors.Is(err, contracts.ErrStreamUnavailable) {
		t.Fatalf("second begin error = %v", err)
	}
	if staging.putCalls != 1 || loader.beginCalls != 1 {
		t.Fatalf("stream was read twice: put=%d begin=%d", staging.putCalls, loader.beginCalls)
	}
}

func TestRouterPublicModuleGuardRejectsMissingIdentity(t *testing.T) {
	provider := &routerPublicLoaderProvider{api: &routerPublicTestLoader{}}
	_, err := provider.beginUpload(context.Background(), contracts.PublicLoaderBeginUploadRequest{})
	if !errors.Is(err, contracts.ErrPublicUnauthorized) {
		t.Fatalf("error = %v", err)
	}
}

func TestRouterPublicServicesBuildWithEngineAdapters(t *testing.T) {
	services, err := newRouterPublicServices(routerPublicDependencies{
		forms: &routerPublicTestForms{}, loader: &routerPublicTestLoader{}, staging: newRouterPublicTestStaging(),
		authorization: routerPublicTestAuthorization{}, credential: "router", partSize: 5,
		maxFile: 100, maxParts: 4, maxInFlight: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer services.Close(context.Background())
	if services.Forms == nil || services.Loader == nil || services.Streams == nil {
		t.Fatalf("services = %#v", services)
	}
	if err := services.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}
}
