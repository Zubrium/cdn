package publicupload_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/Zubrium/cdn/pkg/contracts"
	"github.com/Zubrium/cdn/presentation/router/publicupload"
	"github.com/google/uuid"
)

type testStreams struct {
	ref  contracts.StreamRef
	body io.Reader
}

func (streams *testStreams) AddReader(io.ReadCloser) (contracts.StreamRef, error) {
	return streams.ref, nil
}
func (streams *testStreams) OpenRead(context.Context, contracts.StreamRef) (io.ReadCloser, error) {
	return io.NopCloser(streams.body), nil
}
func (streams *testStreams) OpenWrite(context.Context, contracts.StreamRef) (io.WriteCloser, error) {
	return nil, contracts.ErrStreamUnavailable
}
func (streams *testStreams) Close() error { return nil }

type testBody struct {
	io.Reader
	ref contracts.StreamRef
}

func (body testBody) StreamReference() contracts.StreamRef { return body.ref }

type testAuthorization struct{}

func (testAuthorization) Authenticate(context.Context, string) (contracts.AuthorizationSubject, contracts.Principal, error) {
	return contracts.AuthorizationSubject{Credential: "router"}, contracts.Principal{Active: true}, nil
}

func (testAuthorization) RequireGroupLevel(context.Context, contracts.AuthorizationSubject, []string, contracts.AccessLevel) error {
	return nil
}

type testLoader struct {
	begin  contracts.PublicLoaderBeginUploadRequest
	commit bool
}

func (loader *testLoader) BeginUpload(_ context.Context, request contracts.PublicLoaderBeginUploadRequest, _ contracts.PublicUploadStreams) (contracts.PublicLoaderUploadSession, error) {
	loader.begin = request
	return contracts.PublicLoaderUploadSession{
		ID: uuid.NewString(), State: "uploading", PartSize: request.PartSize, ExpectedParts: 0,
	}, nil
}

func (loader *testLoader) PutPart(context.Context, contracts.PublicLoaderPutPartRequest) (contracts.PublicLoaderUploadSession, error) {
	return contracts.PublicLoaderUploadSession{}, contracts.ErrPublicUnavailable
}

func (loader *testLoader) Commit(_ context.Context, _ contracts.PublicLoaderCommitRequest) (contracts.PublicLoaderFile, error) {
	loader.commit = true
	return contracts.PublicLoaderFile{
		ID: uuid.NewString(), Name: "public-upload", ContentType: "image/png", Size: 5,
		SHA256: strings.Repeat("a", 64), Revision: 1, Groups: []string{"uploads"},
	}, nil
}

func (loader *testLoader) Abort(context.Context, contracts.PublicLoaderAbortRequest) error {
	return nil
}

func (loader *testLoader) StatFile(context.Context, contracts.PublicLoaderStatFileRequest) (contracts.PublicLoaderFile, error) {
	return contracts.PublicLoaderFile{}, contracts.ErrPublicNotFound
}

func (loader *testLoader) DeleteFile(context.Context, contracts.PublicLoaderDeleteFileRequest) error {
	return nil
}

func (loader *testLoader) GetFileSet(context.Context, contracts.PublicLoaderGetFileSetRequest) (contracts.PublicLoaderFileSet, error) {
	return contracts.PublicLoaderFileSet{}, contracts.ErrPublicNotFound
}

func (loader *testLoader) AddFileToSet(context.Context, contracts.PublicLoaderAddFileSetRequest) (contracts.PublicLoaderFileSet, error) {
	return contracts.PublicLoaderFileSet{}, contracts.ErrPublicUnavailable
}

func newTestOperation(t *testing.T) (*publicupload.Operation, *testLoader) {
	t.Helper()
	loader := &testLoader{}
	operation, err := publicupload.New(loader, publicupload.Config{
		Credential: "router-service-credential", GroupAuthorization: testAuthorization{},
		MaxFileBytes: 100, PartSize: 5, MaxParts: 20, MaxConcurrentUploads: 2,
		AllowedContentTypes: []string{"image/png"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return operation, loader
}

func TestPublicUploadSuccessResponseContract(t *testing.T) {
	operation, loader := newTestOperation(t)
	streams := &testStreams{ref: "stream-1", body: strings.NewReader("hello")}
	raw := json.RawMessage(`{"enabled":true,"access_group":"uploads","max_bytes":10,"allowed_content_types":["image/png"]}`)
	if err := operation.ValidateRouteConfig(raw); err != nil {
		t.Fatalf("route config: %v", err)
	}
	response, err := operation.Execute(context.Background(), raw, contracts.PublicOperationRequest{
		Method: "POST", Host: "example.test", EscapedPath: "/upload", ContentType: "image/png",
		ContentLength: 5, IdempotencyKey: "1111111111111111",
		Body: testBody{Reader: strings.NewReader("hello"), ref: streams.ref}, Streams: streams,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if response.StatusCode != 201 || response.ContentType != "application/json; charset=utf-8" ||
		response.CacheControl != "no-store" || response.ContentLength <= 0 || response.Body == nil {
		t.Fatalf("response metadata = %#v", response)
	}
	var result struct {
		FileID    string `json:"file_id"`
		FileSetID string `json:"file_set_id,omitempty"`
	}
	decoder := json.NewDecoder(response.Body)
	if err := decoder.Decode(&result); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if _, err := uuid.Parse(result.FileID); err != nil || result.FileSetID != "" {
		t.Fatalf("body = %#v", result)
	}
	if !loader.commit || loader.begin.Stream != streams.ref || loader.begin.Name == "" {
		t.Fatalf("loader calls: begin=%#v commit=%v", loader.begin, loader.commit)
	}
}

func TestPublicUploadRejectsMissingScopedStream(t *testing.T) {
	operation, _ := newTestOperation(t)
	raw := json.RawMessage(`{"enabled":true,"access_group":"uploads","max_bytes":10,"allowed_content_types":["image/png"]}`)
	_, err := operation.Execute(context.Background(), raw, contracts.PublicOperationRequest{
		Method: "POST", Host: "example.test", EscapedPath: "/upload", ContentType: "image/png",
		ContentLength: 5, IdempotencyKey: "1111111111111111", Body: strings.NewReader("hello"),
	})
	if !errors.Is(err, contracts.ErrPublicInvalid) {
		t.Fatalf("error = %v, want ErrPublicInvalid", err)
	}
}
