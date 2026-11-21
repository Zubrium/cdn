package archive

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	core "github.com/Zubrium/cdn/core/presentation"
	"github.com/google/uuid"
)

type fakeCoreFiles struct {
	archive        []byte
	sourceID       string
	reads          int
	creates        int
	outputs        map[string]string
	files          map[string]core.File
	sourceDeleted  bool
	deleteTargets  []string
	deleteFailures int
	progress       *fakeProgress
	index          *fakeCoreIndex
}

func (f *fakeCoreFiles) ReadFile(_ context.Context, _ core.Session, id string) (io.ReadCloser, core.File, error) {
	f.reads++
	f.sourceID = id
	digest := sha256.Sum256(f.archive)
	return io.NopCloser(bytes.NewReader(f.archive)), core.File{ID: id, Groups: []string{"assets"}, Revision: 1, Size: int64(len(f.archive)), SHA256: hex.EncodeToString(digest[:])}, nil
}

func (f *fakeCoreFiles) StatFile(_ context.Context, _ core.Session, id string) (core.File, error) {
	if file, ok := f.files[id]; ok {
		return file, nil
	}
	return core.File{}, core.ErrFileNotFound
}

func (f *fakeCoreFiles) DeleteFile(_ context.Context, _ core.Session, id string, expected int64) error {
	f.deleteTargets = append(f.deleteTargets, id)
	if id == "" || id != f.sourceID || expected != 1 {
		return fmt.Errorf("unexpected file deletion target")
	}
	if f.progress == nil || !f.progress.exists || f.index == nil {
		return fmt.Errorf("source deletion lacks durable archive progress")
	}
	var state importProgress
	if err := json.Unmarshal(f.progress.record.Payload, &state); err != nil || !state.OutputsVerified || state.Ready ||
		state.SourceRevision != expected || state.SourceSHA256 == "" || len(state.EntryIDs) == 0 || len(state.EntryFiles) != len(state.EntryIDs) {
		return fmt.Errorf("source deletion precedes durable output verification")
	}
	for entryPath, outputID := range state.EntryIDs {
		if _, ok := state.EntryFiles[entryPath]; !ok || f.files[outputID].ID != outputID || !f.index.members[outputID] {
			return fmt.Errorf("source deletion precedes output membership reconciliation")
		}
	}
	if f.deleteFailures > 0 {
		f.deleteFailures--
		return fmt.Errorf("temporary delete failure")
	}
	if f.sourceDeleted {
		return core.ErrFileNotFound
	}
	f.sourceDeleted = true
	return nil
}

func (f *fakeCoreFiles) CreateFileVerified(_ context.Context, _ core.Session, requestKey string, groups []string, metadata map[string]string, contentType string, size int64, checksum string, body io.Reader) (string, error) {
	if f.outputs == nil {
		f.outputs = make(map[string]string)
	}
	if id := f.outputs[requestKey]; id != "" {
		return id, nil
	}
	data, err := io.ReadAll(body)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	if int64(len(data)) != size || hex.EncodeToString(digest[:]) != checksum {
		return "", fmt.Errorf("verified file mismatch")
	}
	f.creates++
	id := fmt.Sprintf("file-%d", f.creates)
	f.outputs[requestKey] = id
	if f.files == nil {
		f.files = make(map[string]core.File)
	}
	f.files[id] = core.File{ID: id, Groups: append([]string(nil), groups...), Metadata: metadata, ContentType: contentType, SHA256: hex.EncodeToString(digest[:]), Size: size, Revision: 1}
	return id, nil
}

type fakeProgress struct {
	record core.ModuleDataRecord
	exists bool
}

func (f *fakeProgress) Get(context.Context, core.ModuleDataKey) (core.ModuleDataRecord, error) {
	if !f.exists {
		return core.ModuleDataRecord{}, core.ErrModuleDataNotFound
	}
	return f.record, nil
}
func (f *fakeProgress) Put(_ context.Context, _ core.ModuleDataKey, value core.ModuleDataValue, expected int64) (core.ModuleDataRecord, error) {
	if (f.exists && expected != f.record.Revision) || (!f.exists && expected != 0) {
		return core.ModuleDataRecord{}, core.ErrModuleDataConflict
	}
	f.record = core.ModuleDataRecord{Payload: append([]byte(nil), value.Payload...), PayloadVersion: value.PayloadVersion, Revision: expected + 1}
	f.exists = true
	return f.record, nil
}

type fakeCoreIndex struct {
	members  map[string]bool
	revision int64
}

func (f *fakeCoreIndex) GetManagedFileSet(context.Context, core.Session, string) (core.FileSet, error) {
	if f.members == nil {
		f.members = make(map[string]bool)
	}
	return core.FileSet{Revision: f.revision}, nil
}
func (f *fakeCoreIndex) ListFileSet(_ context.Context, _ core.Session, _ string, cursor string, _ int) ([]core.File, string, error) {
	result := make([]core.File, 0, len(f.members))
	for id := range f.members {
		result = append(result, core.File{ID: id})
	}
	return result, "", nil
}
func (f *fakeCoreIndex) AddFileToManagedSet(_ context.Context, _ core.Session, _, id string, expected int64) (core.FileSet, error) {
	if f.members == nil {
		f.members = make(map[string]bool)
	}
	if expected != f.revision {
		return core.FileSet{}, core.ErrRevisionConflict
	}
	if !f.members[id] {
		f.members[id] = true
		f.revision++
	}
	return core.FileSet{Revision: f.revision}, nil
}
func (f *fakeCoreIndex) RemoveFileFromManagedSet(_ context.Context, _ core.Session, _, id string, expected int64) (core.FileSet, error) {
	if expected != f.revision {
		return core.FileSet{}, core.ErrRevisionConflict
	}
	if f.members[id] {
		delete(f.members, id)
		f.revision++
	}
	return core.FileSet{Revision: f.revision}, nil
}

func testHandler(t *testing.T, files CoreFiles, archiveBytes []byte) *Handler {
	t.Helper()
	staging := filepath.Join(t.TempDir(), "archive-staging")
	if err := os.Mkdir(staging, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(staging, 0o700); err != nil {
		t.Fatal(err)
	}
	files.(*fakeCoreFiles).archive = archiveBytes
	fake := files.(*fakeCoreFiles)
	fake.progress = &fakeProgress{}
	fake.index = &fakeCoreIndex{}
	handler, err := NewHandler(files, core.Session{}, Config{
		Limits:     Limits{MaxArchiveBytes: 4096, MaxEntries: 10, MaxEntryBytes: 1024, MaxExpandedBytes: 2048, MaxCompressionRatio: 1000},
		StagingDir: staging, TargetFileSetID: "trusted-set-1", OutputGroups: []string{"assets"}, Progress: fake.progress, Index: fake.index,
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func manifestZIPFixture(t *testing.T, manifest string) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	for name, content := range map[string]string{"manifest.json": manifest, "assets/app.js": "export function render() {}"} {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(entry, content); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func TestHandlerValidatesThenStoresAndReconcilesIdempotently(t *testing.T) {
	manifest := `{"formatVersion":2,"key":"hero","name":"Hero","version":"1.2.3","engineApi":"v1","slots":["main"],"arguments":{},"dataInputs":[],"runtime":{"abi":"static-lifecycle","entry":"assets/app.js","styles":[]}}`
	archiveBytes := manifestZIPFixture(t, manifest)
	files := &fakeCoreFiles{}
	handler := testHandler(t, files, archiveBytes)
	request := Request{TaskID: "task-1", SourceFileID: "source-1"}
	result, err := handler.Import(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Ready || len(result.FileIDs) != 2 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if files.reads != 1 || files.creates != 2 || len(files.index.members) != 2 {
		t.Fatalf("reads=%d creates=%d memberships=%d", files.reads, files.creates, len(files.index.members))
	}
	again, err := handler.Import(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !again.Ready || len(again.FileIDs) != 2 || files.creates != 2 {
		t.Fatalf("retry was not reconciled idempotently: result=%+v creates=%d", again, files.creates)
	}
	entries, err := os.ReadDir(handler.stagingDir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("staging leftovers=%v err=%v", entries, err)
	}
}

func TestHandlerResumesSourceCleanupWithoutRereadingArchive(t *testing.T) {
	manifest := `{"formatVersion":2,"key":"hero","name":"Hero","version":"1.2.3","engineApi":"v1","slots":["main"],"arguments":{},"dataInputs":[],"runtime":{"abi":"static-lifecycle","entry":"assets/app.js","styles":[]}}`
	files := &fakeCoreFiles{deleteFailures: 1}
	handler := testHandler(t, files, manifestZIPFixture(t, manifest))
	request := Request{TaskID: "task-cleanup", SourceFileID: "source-cleanup"}
	first, err := handler.Import(context.Background(), request)
	if err == nil || first.Ready || len(first.FileIDs) != 0 {
		t.Fatalf("source cleanup failure was reported as completed: result=%+v err=%v", first, err)
	}
	state, _, err := handler.loadProgress(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !state.OutputsVerified || state.Ready || len(state.EntryIDs) != 2 || len(state.EntryFiles) != 2 {
		t.Fatalf("cleanup checkpoint is not durable and retryable: %+v", state)
	}
	for _, outputID := range state.EntryIDs {
		if !files.index.members[outputID] {
			t.Fatalf("verified output %q is missing its FileSet membership", outputID)
		}
	}
	if files.sourceDeleted || len(files.deleteTargets) != 1 || files.deleteTargets[0] != request.SourceFileID {
		t.Fatalf("unexpected source cleanup side effect: deleted=%v targets=%v", files.sourceDeleted, files.deleteTargets)
	}
	reads := files.reads
	result, err := handler.Import(context.Background(), request)
	if err != nil || !result.Ready || len(result.FileIDs) != 2 || files.reads != reads {
		t.Fatalf("cleanup retry result=%+v reads=%d (was %d) err=%v", result, files.reads, reads, err)
	}
	if !files.sourceDeleted || files.index.members[request.SourceFileID] {
		t.Fatal("source file or its FileSet membership remains after successful cleanup")
	}
	for _, outputID := range result.FileIDs {
		if !files.index.members[outputID] {
			t.Fatalf("output %q lost its FileSet membership after cleanup retry", outputID)
		}
	}
	if len(files.deleteTargets) != 2 || files.deleteTargets[1] != request.SourceFileID {
		t.Fatalf("retry attempted to delete an output or unrelated file: %v", files.deleteTargets)
	}
}

func TestWorkerHandlerDispatchesOnlyRegisteredArchiveOperation(t *testing.T) {
	manifest := `{"formatVersion":2,"key":"hero","name":"Hero","version":"1.2.3","engineApi":"v1","slots":["main"],"arguments":{},"dataInputs":[],"runtime":{"abi":"static-lifecycle","entry":"assets/app.js","styles":[]}}`
	files := &fakeCoreFiles{}
	handler := testHandler(t, files, manifestZIPFixture(t, manifest))
	task := core.Task{ID: uuid.New(), Kind: core.TaskKindWorker, Worker: &core.WorkerRef{
		ComponentID: DataModuleID, OperationID: OperationID, ContractVersion: 1,
	}}
	task.Payload, _ = json.Marshal(Request{SourceFileID: "source-1"})
	outcome, err := handler.WorkerHandler(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}
	var result Result
	if err := json.Unmarshal(outcome.Result, &result); err != nil || !result.Ready || len(result.FileIDs) != 2 {
		t.Fatalf("result=%+v decode error=%v", result, err)
	}

	task.Worker.OperationID = "other-operation"
	if _, err := handler.WorkerHandler(context.Background(), task); err == nil {
		t.Fatal("accepted an unregistered archive operation")
	}
	task.Worker.OperationID = OperationID
	task.Payload, _ = json.Marshal(Request{TaskID: "another-task", SourceFileID: "source-1"})
	if _, err := handler.WorkerHandler(context.Background(), task); err == nil {
		t.Fatal("accepted a payload bound to another task")
	}
}

func TestHandlerRejectsInvalidArchiveBeforeAnyWrite(t *testing.T) {
	files := &fakeCoreFiles{}
	_, err := testHandler(t, files, []byte("not a ZIP")).Import(context.Background(), Request{TaskID: "task-1", SourceFileID: "source-1"})
	if err == nil {
		t.Fatalf("err=%v, want ZIP validation failure", err)
	}
	if files.creates != 0 {
		t.Fatalf("created %d files for invalid ZIP", files.creates)
	}
}

func TestHandlerRejectsUnsupportedManifestBeforeAnyWrite(t *testing.T) {
	manifest := `{"formatVersion":2,"key":"hero","name":"Hero","version":"1.2.3","engineApi":"v1","slots":["main"],"arguments":{},"dataInputs":[],"runtime":{"abi":"static-module-v1","entry":"assets/app.js"}}`
	files := &fakeCoreFiles{}
	_, err := testHandler(t, files, manifestZIPFixture(t, manifest)).Import(context.Background(), Request{TaskID: "task-1", SourceFileID: "source-1"})
	if err == nil {
		t.Fatalf("err=%v, want manifest validation failure", err)
	}
	if files.creates != 0 {
		t.Fatalf("created %d files for invalid manifest", files.creates)
	}
}

func TestHandlerRejectsRetiredVersionedABIBeforeAnyWrite(t *testing.T) {
	manifest := `{"formatVersion":2,"key":"hero","name":"Hero","version":"1.2.3","engineApi":"v1","slots":["main"],"arguments":{},"dataInputs":[],"runtime":{"abi":"static-lifecycle-v2","entry":"assets/app.js"}}`
	files := &fakeCoreFiles{}
	_, err := testHandler(t, files, manifestZIPFixture(t, manifest)).Import(context.Background(), Request{TaskID: "task-retired-abi", SourceFileID: "source-1"})
	if err == nil || files.creates != 0 {
		t.Fatalf("retired versioned ABI accepted or wrote outputs: err=%v creates=%d", err, files.creates)
	}
}

func TestHandlerRejectsUnknownManifestFieldsBeforeAnyWrite(t *testing.T) {
	cases := []string{
		`{"formatVersion":2,"key":"hero","name":"Hero","version":"1.2.3","engineApi":"v1","slots":["main"],"arguments":{},"dataInputs":[],"runtime":{"abi":"static-lifecycle","entry":"assets/app.js"},"unexpected":true}`,
		`{"formatVersion":2,"key":"hero","name":"Hero","version":"1.2.3","engineApi":"v1","slots":["main"],"arguments":{},"dataInputs":[{"key":"x","kind":"record","unknown":true}],"runtime":{"abi":"static-lifecycle","entry":"assets/app.js"}}`,
		`{"formatVersion":2,"key":"hero","name":"Hero","version":"1.2.3","engineApi":"v1","slots":["main"],"arguments":{},"dataInputs":[],"form":{"submissionSchema":{"type":"object"},"unknown":true},"runtime":{"abi":"static-lifecycle","entry":"assets/app.js"}}`,
	}
	for _, manifest := range cases {
		files := &fakeCoreFiles{}
		_, err := testHandler(t, files, manifestZIPFixture(t, manifest)).Import(context.Background(), Request{TaskID: "task-1", SourceFileID: "source-1"})
		if err == nil || files.creates != 0 {
			t.Fatalf("manifest accepted or wrote outputs: err=%v creates=%d", err, files.creates)
		}
	}
}

func TestHandlerRequiresPrivateAbsoluteStagingDirectory(t *testing.T) {
	staging := t.TempDir()
	if err := os.Chmod(staging, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := NewHandler(&fakeCoreFiles{}, core.Session{}, Config{
		Limits:     Limits{MaxArchiveBytes: 4096, MaxEntries: 10, MaxEntryBytes: 1024, MaxExpandedBytes: 2048, MaxCompressionRatio: 100},
		StagingDir: staging, TargetFileSetID: "trusted-set-1", OutputGroups: []string{"assets"}, Progress: &fakeProgress{}, Index: &fakeCoreIndex{},
	})
	if err == nil {
		t.Fatal("accepted staging directory with mode 0755")
	}
}

func TestFileRequestKeyIsStableAndEntrySpecific(t *testing.T) {
	first, err := FileRequestKey("task-1", "source-1", "assets/app.js")
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := FileRequestKey("task-1", "source-1", "assets/app.js")
	if err != nil {
		t.Fatal(err)
	}
	other, err := FileRequestKey("task-1", "source-1", "assets/other.js")
	if err != nil {
		t.Fatal(err)
	}
	if first != repeated || first == other || len(first) != 36 {
		t.Fatalf("keys first=%q repeated=%q other=%q", first, repeated, other)
	}
}
