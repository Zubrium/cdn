package bus

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/Zubrium/cdn/pkg/contracts"
)

type fileCreatorFunc func(context.Context, string, []string, map[string]string, string, int64, string, io.Reader) (string, error)

func (creator fileCreatorFunc) CreateFileVerified(ctx context.Context, requestKey string, groups []string, metadata map[string]string, contentType string, size int64, checksum string, body io.Reader) (string, error) {
	return creator(ctx, requestKey, groups, metadata, contentType, size, checksum, body)
}
func (fileCreatorFunc) DeleteFile(context.Context, string, int64) error { return nil }

func TestRegisterVerifiedFileCreateResolvesByBusModuleAndStreamsBody(t *testing.T) {
	operation := contracts.FileCreateVerifiedOperation()
	dispatcher, err := NewAllowlisted(operation.Key)
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.EnableScopedStreams(); err != nil {
		t.Fatal(err)
	}
	var resolvedModule string
	if err := RegisterFileCreateVerified(dispatcher, func(_ context.Context, moduleID string) (contracts.VerifiedFileCreator, error) {
		resolvedModule = moduleID
		return fileCreatorFunc(func(ctx context.Context, key string, groups []string, metadata map[string]string, contentType string, size int64, checksum string, body io.Reader) (string, error) {
			if key != "task/artifact" || len(groups) != 1 || groups[0] != "published" || size != 4 || checksum != "sha256" {
				t.Fatalf("file request did not preserve verified metadata")
			}
			data, err := io.ReadAll(body)
			if err != nil {
				return "", err
			}
			if string(data) != "body" {
				t.Fatalf("body = %q", data)
			}
			return "file-1", nil
		}), nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Seal(); err != nil {
		t.Fatal(err)
	}
	streams := NewScopedStreams()
	client := NewModuleDispatcher(dispatcher, "worker.prerender")
	response, err := contracts.CallFileCreateVerified(context.Background(), client, streams, contracts.FileCreateVerifiedInput{
		RequestKey: "task/artifact", Groups: []string{"published"}, ContentType: "text/html", Size: 4, SHA256: "sha256",
	}, strings.NewReader("body"))
	if err != nil {
		t.Fatal(err)
	}
	if response.FileID != "file-1" || resolvedModule != "worker.prerender" {
		t.Fatalf("response/module = %#v/%q", response, resolvedModule)
	}
}

type coreFileReaderFunc func(context.Context, string) (io.ReadCloser, contracts.File, error)

func (reader coreFileReaderFunc) ReadFile(ctx context.Context, fileID string) (io.ReadCloser, contracts.File, error) {
	return reader(ctx, fileID)
}

func TestRegisterFileReadStreamsBytesAndReturnsMetadata(t *testing.T) {
	operation := contracts.FileReadOperation()
	dispatcher, err := NewAllowlisted(operation.Key)
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.EnableScopedStreams(); err != nil {
		t.Fatal(err)
	}
	var resolvedModule string
	if err := RegisterFileRead(dispatcher, func(_ context.Context, moduleID string) (contracts.CoreFileReader, error) {
		resolvedModule = moduleID
		return coreFileReaderFunc(func(_ context.Context, fileID string) (io.ReadCloser, contracts.File, error) {
			if fileID != "source-1" {
				t.Fatalf("file ID = %q", fileID)
			}
			return io.NopCloser(strings.NewReader("large file body")), contracts.File{ID: fileID, Size: 15, ContentType: "application/octet-stream"}, nil
		}), nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Seal(); err != nil {
		t.Fatal(err)
	}
	streams := NewScopedStreams()
	body, file, err := contracts.CallFileRead(context.Background(), NewModuleDispatcher(dispatcher, "worker.media"), streams, "source-1")
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	if err := body.Close(); err != nil {
		t.Fatal(err)
	}
	if string(data) != "large file body" || file.ID != "source-1" || file.Size != 15 || resolvedModule != "worker.media" {
		t.Fatalf("body/file/module = %q, %#v, %q", data, file, resolvedModule)
	}
	if err := dispatcher.Close(context.Background()); err != nil {
		t.Fatalf("dispatcher did not drain stream lifecycle: %v", err)
	}
}

type failingFileBody struct{ sent bool }

func (body *failingFileBody) Read(buffer []byte) (int, error) {
	if !body.sent {
		body.sent = true
		return copy(buffer, "partial"), nil
	}
	return 0, errors.New("storage read failed")
}

func (*failingFileBody) Close() error { return nil }

func TestRegisterFileReadPropagatesProducerFailureToStreamReader(t *testing.T) {
	operation := contracts.FileReadOperation()
	dispatcher, err := NewAllowlisted(operation.Key)
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.EnableScopedStreams(); err != nil {
		t.Fatal(err)
	}
	if err := RegisterFileRead(dispatcher, func(context.Context, string) (contracts.CoreFileReader, error) {
		return coreFileReaderFunc(func(context.Context, string) (io.ReadCloser, contracts.File, error) {
			return &failingFileBody{}, contracts.File{ID: "file-1"}, nil
		}), nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Seal(); err != nil {
		t.Fatal(err)
	}
	streams := NewScopedStreams()
	body, _, err := contracts.CallFileRead(context.Background(), NewModuleDispatcher(dispatcher, "worker.media"), streams, "file-1")
	if err != nil {
		t.Fatal(err)
	}
	data, readErr := io.ReadAll(body)
	_ = body.Close()
	if string(data) != "partial" || readErr == nil || readErr.Error() != "storage read failed" {
		t.Fatalf("partial stream = %q, %v", data, readErr)
	}
	if err := dispatcher.Close(context.Background()); err != nil {
		t.Fatalf("dispatcher close = %v", err)
	}
}

type fileStatFunc func(context.Context, string) (contracts.File, error)

func (stat fileStatFunc) StatFile(ctx context.Context, fileID string) (contracts.File, error) {
	return stat(ctx, fileID)
}

func TestRegisterFileStatUsesCompositionBoundModuleScope(t *testing.T) {
	operation := contracts.FileStatOperation()
	dispatcher, err := NewAllowlisted(operation.Key)
	if err != nil {
		t.Fatal(err)
	}
	var resolvedModule string
	if err := RegisterFileStat(dispatcher, func(_ context.Context, moduleID string, _ contracts.AuthorizationSubject) (contracts.CoreFileStat, error) {
		resolvedModule = moduleID
		return fileStatFunc(func(_ context.Context, fileID string) (contracts.File, error) {
			return contracts.File{ID: fileID, Revision: 2}, nil
		}), nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Seal(); err != nil {
		t.Fatal(err)
	}
	file, err := contracts.Call(context.Background(), NewModuleDispatcher(dispatcher, "worker.archive"), operation, contracts.FileStatRequest{FileID: "source-1"})
	if err != nil {
		t.Fatal(err)
	}
	if file.File.ID != "source-1" || file.File.Revision != 2 || resolvedModule != "worker.archive" {
		t.Fatalf("file/module = %#v/%q", file.File, resolvedModule)
	}
}

type coreFilesStub struct{ addedRevision int64 }

func (coreFilesStub) StatFile(context.Context, string) (contracts.File, error) {
	return contracts.File{}, nil
}
func (coreFilesStub) ReadFile(context.Context, string) (io.ReadCloser, contracts.File, error) {
	return nil, contracts.File{}, nil
}
func (coreFilesStub) CreateFileVerified(context.Context, string, []string, map[string]string, string, int64, string, io.Reader) (string, error) {
	return "", nil
}
func (coreFilesStub) DeleteFile(context.Context, string, int64) error { return nil }
func (coreFilesStub) GetManagedFileSet(context.Context, string) (contracts.FileSet, error) {
	return contracts.FileSet{}, nil
}
func (coreFilesStub) ListManagedFileSets(context.Context, string, string, string, int) ([]contracts.FileSet, string, error) {
	return nil, "", nil
}
func (coreFilesStub) ListFileSet(context.Context, string, string, int) ([]contracts.File, string, error) {
	return nil, "", nil
}
func (stub coreFilesStub) AddFileToManagedSet(_ context.Context, setID, fileID string, expected int64) (contracts.FileSet, error) {
	stub.addedRevision = expected
	return contracts.FileSet{ID: setID, Name: fileID, Revision: expected + 1}, nil
}
func (coreFilesStub) RemoveFileFromManagedSet(context.Context, string, string, int64) (contracts.FileSet, error) {
	return contracts.FileSet{}, nil
}

func TestRegisterFileSetOperationsKeepExpectedRevisionAndScope(t *testing.T) {
	operations := []contracts.Key{
		contracts.FileSetGetOperation().Key, contracts.FileSetListOperation().Key, contracts.FileSetMembersOperation().Key,
		contracts.FileSetAddOperation().Key, contracts.FileSetRemoveOperation().Key,
	}
	dispatcher, err := NewAllowlisted(operations...)
	if err != nil {
		t.Fatal(err)
	}
	if err := RegisterFileSetOperations(dispatcher, func(context.Context, string) (contracts.CoreFiles, error) { return coreFilesStub{}, nil }); err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Seal(); err != nil {
		t.Fatal(err)
	}
	response, err := contracts.Call(context.Background(), NewModuleDispatcher(dispatcher, "worker.media"), contracts.FileSetAddOperation(),
		contracts.FileSetMutationRequest{SetID: "set-1", FileID: "file-1", ExpectedRevision: 7})
	if err != nil {
		t.Fatal(err)
	}
	if response.Set.ID != "set-1" || response.Set.Revision != 8 {
		t.Fatalf("file set response = %#v", response.Set)
	}
}

type fileDeleteFunc func(context.Context, string, int64) error

func (deleteFile fileDeleteFunc) DeleteFile(ctx context.Context, fileID string, expected int64) error {
	return deleteFile(ctx, fileID, expected)
}

func TestRegisterFileDeleteForwardsRevisionWithinModuleScope(t *testing.T) {
	operation := contracts.FileDeleteOperation()
	dispatcher, err := NewAllowlisted(operation.Key)
	if err != nil {
		t.Fatal(err)
	}
	var resolvedModule, deletedFile string
	if err := RegisterFileDelete(dispatcher, func(_ context.Context, moduleID string) (contracts.CoreFileDeleter, error) {
		resolvedModule = moduleID
		return fileDeleteFunc(func(_ context.Context, fileID string, expected int64) error {
			deletedFile = fileID
			if expected != 12 {
				t.Fatalf("expected revision = %d", expected)
			}
			return nil
		}), nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Seal(); err != nil {
		t.Fatal(err)
	}
	if _, err := contracts.Call(context.Background(), NewModuleDispatcher(dispatcher, "worker.archive"), operation,
		contracts.FileDeleteRequest{FileID: "source-1", ExpectedRevision: 12}); err != nil {
		t.Fatal(err)
	}
	if resolvedModule != "worker.archive" || deletedFile != "source-1" {
		t.Fatalf("module/file = %q/%q", resolvedModule, deletedFile)
	}
}
