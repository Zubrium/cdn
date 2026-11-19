package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"

	core "github.com/Zubrium/cdn/core/presentation"
)

type testSessions struct{}

func (testSessions) WithSession(ctx context.Context, fn func(core.Session) error) error {
	return fn(core.Session{})
}

type testFiles struct {
	source       core.File
	created      map[string]Result
	createGroups []string
	createCalls  int
}

func (f *testFiles) StatFile(_ context.Context, _ core.Session, fileID string) (core.File, error) {
	if fileID == f.source.ID {
		return f.source, nil
	}
	for _, result := range f.created {
		if result.OutputFileID == fileID {
			return core.File{ID: fileID, Groups: append([]string(nil), f.source.Groups...), ContentType: result.ContentType, SHA256: result.Checksum, Size: result.Size}, nil
		}
	}
	return core.File{}, core.ErrFileNotFound
}
func (f *testFiles) ReadFile(context.Context, core.Session, string) (io.ReadCloser, core.File, error) {
	return io.NopCloser(bytes.NewReader([]byte("source bytes"))), f.source, nil
}
func (f *testFiles) CreateFileVerified(_ context.Context, _ core.Session, requestKey string, groups []string, _ map[string]string, contentType string, size int64, checksum string, body io.Reader) (string, error) {
	f.createCalls++
	f.createGroups = append([]string(nil), groups...)
	if prior, ok := f.created[requestKey]; ok {
		return prior.OutputFileID, nil
	}
	data, err := io.ReadAll(body)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(data)
	if int64(len(data)) != size || hex.EncodeToString(hash[:]) != checksum {
		return "", core.ErrChecksumMismatch
	}
	result := Result{OutputFileID: "output-1", Checksum: checksum, ContentType: contentType, Size: size}
	f.created[requestKey] = result
	return result.OutputFileID, nil
}

type testData struct {
	record core.ModuleDataRecord
	key    core.ModuleDataKey
}

func (d *testData) Get(_ context.Context, key core.ModuleDataKey) (core.ModuleDataRecord, error) {
	d.key = key
	if d.record.Revision == 0 {
		return core.ModuleDataRecord{}, core.ErrModuleDataNotFound
	}
	return d.record, nil
}
func (d *testData) Put(_ context.Context, key core.ModuleDataKey, value core.ModuleDataValue, revision int64) (core.ModuleDataRecord, error) {
	d.key = key
	if revision != d.record.Revision {
		return core.ModuleDataRecord{}, core.ErrModuleDataConflict
	}
	d.record = core.ModuleDataRecord{Payload: append(json.RawMessage(nil), value.Payload...), PayloadVersion: value.PayloadVersion, Revision: revision + 1}
	return d.record, nil
}

type testRunner struct {
	calls    int
	isolated bool
}

func (r *testRunner) Isolated() bool { return r.isolated }
func (r *testRunner) Run(context.Context, Preset, io.Reader) (ProcessedOutput, error) {
	r.calls++
	data := []byte("converted bytes")
	sum := sha256.Sum256(data)
	return ProcessedOutput{Body: io.NopCloser(bytes.NewReader(data)), Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:]), ContentType: "image/webp"}, nil
}

func handlerFixture(t *testing.T, runner Runner) (*Handler, *testFiles, *testData) {
	t.Helper()
	registry, err := NewRegistry([]Preset{{Name: "web", Version: "1", InputContentTypes: []string{"image/png"}, OutputContentType: "image/webp", OutputFormat: "webp", FFmpegArgs: []string{"-c:v", "libwebp"}, Timeout: time.Minute, MaxInputBytes: 1000, MaxOutputBytes: 2000}})
	if err != nil {
		t.Fatal(err)
	}
	input := []byte("source bytes")
	sum := sha256.Sum256(input)
	files := &testFiles{source: core.File{ID: "source-1", Groups: []string{"media", "uploads"}, ContentType: "image/png", SHA256: hex.EncodeToString(sum[:]), Size: int64(len(input))}, created: map[string]Result{}}
	data := &testData{}
	handler, err := NewHandler(registry, testSessions{}, files, data, runner, 2, 6000)
	if err != nil {
		t.Fatal(err)
	}
	return handler, files, data
}

func TestHandlerRequiresIsolatedRunner(t *testing.T) {
	registry, err := NewRegistry([]Preset{{Name: "web", Version: "1", InputContentTypes: []string{"image/png"}, OutputContentType: "image/webp", MaxInputBytes: 10, MaxOutputBytes: 10}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewHandler(registry, testSessions{}, &testFiles{}, &testData{}, &testRunner{}, 1, 20); !errors.Is(err, ErrNotReady) {
		t.Fatalf("unisolated runner accepted: %v", err)
	}
}

func TestDataDescriptorUsesWorkerNamespaceNotWebsiteNamespace(t *testing.T) {
	descriptor := DataDescriptor()
	if descriptor.ModuleID != "worker.media" || len(descriptor.Namespaces) != 1 || descriptor.Namespaces[0].Name != "media" || descriptor.Namespaces[0].Name == "website/media" {
		t.Fatalf("unexpected worker data descriptor: %#v", descriptor)
	}
	if descriptor.Namespaces[0].ScopeTypes[0] != "job" {
		t.Fatalf("unexpected scope: %#v", descriptor.Namespaces[0].ScopeTypes)
	}
}

func TestSignatureValidationRejectsMislabeledBytes(t *testing.T) {
	if !matchesSignature([]byte{0xff, 0xd8, 0xff, 0}, "image/jpeg") {
		t.Fatal("valid JPEG signature rejected")
	}
	if matchesSignature([]byte("not an image"), "image/jpeg") {
		t.Fatal("mislabeled bytes accepted as JPEG")
	}
	if !matchesSignature([]byte("RIFF0000WEBPdata"), "image/webp") {
		t.Fatal("valid WebP signature rejected")
	}
}

func TestFFmpegRunnerIsNotClaimedAsIsolated(t *testing.T) {
	// The process runner cannot be admitted to production handler execution
	// until composition supplies a separately supervised OS sandbox.
	var runner Runner = &FFmpegRunner{}
	if runner.Isolated() {
		t.Fatal("FFmpeg runner claimed OS isolation")
	}
}

func TestHandlerPersistsReadyAndUsesCurrentSourceGroups(t *testing.T) {
	runner := &testRunner{isolated: true}
	handler, files, data := handlerFixture(t, runner)
	request := Request{JobID: "job-1", SourceFileID: "source-1", Profile: "web", ProfileVersion: "1", IdempotencyKey: "idem-1"}
	first, err := handler.Handle(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if first.OutputFileID != "output-1" || first.ContentType != "image/webp" || first.Size == 0 {
		t.Fatalf("unexpected result: %#v", first)
	}
	if len(files.createGroups) != 2 || files.createGroups[0] != "media" || files.createGroups[1] != "uploads" {
		t.Fatalf("output groups were not copied from source: %v", files.createGroups)
	}
	if data.key.Namespace != DataNamespace || data.key.ScopeType != DataScopeType || data.key.ScopeID != request.JobID || data.key.RecordKey != "job" {
		t.Fatalf("job was not persisted in its own worker scope: %#v", data.key)
	}
	second, err := handler.Handle(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if second != first || runner.calls != 1 || files.createCalls != 1 {
		t.Fatalf("ready job was not idempotent: result=%#v calls=%d/%d", second, runner.calls, files.createCalls)
	}
}

func TestReadyJobRequiresAccessiblePersistedOutput(t *testing.T) {
	runner := &testRunner{isolated: true}
	handler, files, _ := handlerFixture(t, runner)
	request := Request{JobID: "job-1", SourceFileID: "source-1", Profile: "web", ProfileVersion: "1", IdempotencyKey: "idem-1"}
	if _, err := handler.Handle(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	files.created = map[string]Result{}
	if _, err := handler.Handle(context.Background(), request); !errors.Is(err, ErrOutputUncertain) {
		t.Fatalf("ready job returned an inaccessible output: %v", err)
	}
	if runner.calls != 1 {
		t.Fatalf("missing output triggered a new conversion: %d", runner.calls)
	}
}

func TestHandlerRejectsIdempotencyKeyReuseWithDifferentJob(t *testing.T) {
	runner := &testRunner{isolated: true}
	handler, _, _ := handlerFixture(t, runner)
	request := Request{JobID: "job-1", SourceFileID: "source-1", Profile: "web", ProfileVersion: "1", IdempotencyKey: "idem-1"}
	if _, err := handler.Handle(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	request.ProfileVersion = "2"
	if _, err := handler.Handle(context.Background(), request); !errors.Is(err, ErrPresetUnavailable) {
		t.Fatalf("unconfigured version accepted: %v", err)
	}
	request.ProfileVersion, request.SourceFileID = "1", "source-2"
	if _, err := handler.Handle(context.Background(), request); err == nil {
		t.Fatal("different source accepted for existing job")
	}
}

func TestTaskHandlerRejectsWrongOperationAndUnknownFields(t *testing.T) {
	runner := &testRunner{isolated: true}
	handler, _, _ := handlerFixture(t, runner)
	job := core.CoreJob{ComponentID: "other", OperationID: OperationID, ContractVersion: 1}
	if _, err := handler.TaskHandler(context.Background(), job); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("wrong component accepted: %v", err)
	}
}
