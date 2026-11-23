package archive

import recorddata "github.com/Zubrium/cdn/pkg/contracts"

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/Zubrium/cdn/pkg/contracts"
)

var (
	ErrManifestMissing   = errors.New("archive root manifest missing")
	requestKeyNamespace  = [16]byte{0x5f, 0xe6, 0x21, 0xc9, 0x2a, 0x7b, 0x5d, 0x42, 0x92, 0x39, 0x8a, 0x43, 0x27, 0xf9, 0x10, 0x6c}
	semverPattern        = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-((?:0|[1-9][0-9]*|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*)(?:\.(?:0|[1-9][0-9]*|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*))*))?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)
	featureOriginPattern = regexp.MustCompile(`^https://[A-Za-z0-9.-]+(:[0-9]{1,5})?$`)
	ipLiteralPattern     = regexp.MustCompile(`^[0-9.]+$`)
)

func validFeatureOrigin(value string) bool {
	if !featureOriginPattern.MatchString(value) {
		return false
	}
	host := strings.TrimPrefix(value, "https://")
	host, _, _ = strings.Cut(host, ":")
	return host != "localhost" && !ipLiteralPattern.MatchString(host)
}

type CoreFiles interface{ contracts.CoreFiles }

type CoreIndex interface {
	GetManagedFileSet(context.Context, string) (contracts.FileSet, error)
	ListFileSet(context.Context, string, string, int) ([]contracts.File, string, error)
	AddFileToManagedSet(context.Context, string, string, int64) (contracts.FileSet, error)
	RemoveFileFromManagedSet(context.Context, string, string, int64) (contracts.FileSet, error)
}

type ProgressStore interface {
	Get(context.Context, recorddata.RecordKey) (recorddata.Record, error)
	Put(context.Context, recorddata.RecordKey, recorddata.RecordValue, int64) (recorddata.Record, error)
}

type Config struct {
	Limits          Limits
	StagingDir      string
	TargetFileSetID string // injected by trusted composition, never accepted from Request
	OutputGroups    []string
	Progress        ProgressStore
	Index           CoreIndex
}

type Handler struct {
	files           CoreFiles
	index           CoreIndex
	progress        ProgressStore
	limits          Limits
	stagingDir      string
	targetFileSetID string
	outputGroups    []string
}

func NewHandler(files CoreFiles, config Config) (*Handler, error) {
	if files == nil || config.Limits.MaxArchiveBytes <= 0 || config.Limits.MaxArchiveBytes == int64(^uint64(0)>>1) ||
		config.Limits.MaxEntries <= 0 || config.Limits.MaxEntryBytes <= 0 || config.Limits.MaxEntryBytes == int64(^uint64(0)>>1) || config.Limits.MaxExpandedBytes <= 0 || config.Limits.MaxCompressionRatio == 0 ||
		strings.TrimSpace(config.TargetFileSetID) == "" || config.Progress == nil || config.Index == nil || len(config.OutputGroups) == 0 || !filepath.IsAbs(config.StagingDir) {
		return nil, errors.New("invalid archive handler configuration")
	}
	info, err := os.Lstat(config.StagingDir)
	if err != nil {
		return nil, fmt.Errorf("inspect archive staging directory: %w", err)
	}
	if !info.IsDir() || info.Mode().Perm() != 0o700 || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("archive staging directory must be a real directory with mode 0700")
	}
	groups := append([]string(nil), config.OutputGroups...)
	return &Handler{files: files, index: config.Index, progress: config.Progress, limits: config.Limits, stagingDir: config.StagingDir,
		targetFileSetID: config.TargetFileSetID, outputGroups: groups}, nil
}

// WorkerHandler adapts one registered Coordinator task to the Archive operation.
func (h *Handler) WorkerHandler(ctx context.Context, task contracts.OperationTask) (contracts.OperationOutcome, error) {
	if h == nil || task.TaskID == "" || len(task.Payload) == 0 || len(task.Payload) > 16<<10 {
		return contracts.OperationOutcome{}, errors.New("invalid archive task")
	}
	decoder := json.NewDecoder(bytes.NewReader(task.Payload))
	decoder.DisallowUnknownFields()
	var request Request
	if err := decoder.Decode(&request); err != nil {
		return contracts.OperationOutcome{}, errors.New("invalid archive task")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) || (request.TaskID != "" && request.TaskID != task.TaskID) {
		return contracts.OperationOutcome{}, errors.New("invalid archive task")
	}
	request.TaskID = task.TaskID
	result, err := h.Import(ctx, request)
	if err != nil {
		return contracts.OperationOutcome{}, err
	}
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded) > 1<<20 {
		return contracts.OperationOutcome{}, errors.New("archive result exceeds limit")
	}
	return contracts.OperationOutcome{Result: encoded}, nil
}

// Import spools the source with a strict byte bound, fully verifies all entries
// and the manifest, then persists progress around idempotent Core writes.
func (h *Handler) Import(ctx context.Context, request Request) (Result, error) {
	if h == nil || h.files == nil || ctx == nil || strings.TrimSpace(request.TaskID) == "" || strings.TrimSpace(request.SourceFileID) == "" {
		return Result{}, errors.New("invalid archive import request")
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	state, revision, err := h.loadProgress(ctx, request)
	if err != nil {
		return Result{}, err
	}
	if state.Ready {
		return h.reconcileReady(ctx, request, state)
	}
	if state.OutputsVerified {
		return h.finishSourceCleanup(ctx, request, state, revision)
	}
	body, source, err := h.files.ReadFile(ctx, request.SourceFileID)
	if err != nil {
		return Result{}, err
	}
	if body == nil || source.ID != request.SourceFileID || source.SHA256 == "" {
		return Result{}, errors.New("Core returned incomplete archive source metadata")
	}
	defer body.Close()
	if source.Size <= 0 || source.Size > h.limits.MaxArchiveBytes {
		return Result{}, errors.New("archive byte limit exceeded")
	}

	spool, err := os.CreateTemp(h.stagingDir, "archive-*.zip")
	if err != nil {
		return Result{}, errors.New("create private archive spool")
	}
	spoolPath := spool.Name()
	defer os.Remove(spoolPath)
	defer spool.Close()
	if err := spool.Chmod(0o600); err != nil {
		return Result{}, errors.New("secure archive spool")
	}
	archiveHash := sha256.New()
	written, err := io.Copy(io.MultiWriter(spool, archiveHash), io.LimitReader(&contextReader{ctx: ctx, reader: body}, h.limits.MaxArchiveBytes+1))
	if err != nil {
		return Result{}, fmt.Errorf("spool source archive: %w", err)
	}
	if written != source.Size || written > h.limits.MaxArchiveBytes || hex.EncodeToString(archiveHash.Sum(nil)) != strings.ToLower(source.SHA256) {
		return Result{}, errors.New("source archive size or checksum mismatch")
	}
	if err := body.Close(); err != nil {
		return Result{}, errors.New("close source archive stream")
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	entries, err := Inspect(ctx, spool, written, h.limits)
	if err != nil {
		return Result{}, err
	}
	manifest, err := readManifest(spool, written, h.limits.MaxEntryBytes)
	if err != nil {
		return Result{}, err
	}
	if err := validateManifest(manifest, entries); err != nil {
		return Result{}, err
	}
	if state.SourceFileID != request.SourceFileID {
		return Result{}, errors.New("archive progress belongs to another source")
	}
	return h.importEntries(ctx, request, source, spool, written, entries, state, revision)
}

type importProgress struct {
	SourceFileID    string            `json:"source_file_id"`
	SourceRevision  int64             `json:"source_revision"`
	SourceSHA256    string            `json:"source_sha256"`
	EntryIDs        map[string]string `json:"entry_ids"`
	EntryFiles      map[string]File   `json:"entry_files"`
	OutputsVerified bool              `json:"outputs_verified"`
	Ready           bool              `json:"ready"`
}

func (h *Handler) progressKey(request Request) recorddata.RecordKey {
	return recorddata.RecordKey{Namespace: DataNamespace, ScopeType: DataScopeType, ScopeID: request.TaskID, RecordKey: "archive"}
}

func (h *Handler) loadProgress(ctx context.Context, request Request) (importProgress, int64, error) {
	key := h.progressKey(request)
	record, err := h.progress.Get(ctx, key)
	if errors.Is(err, recorddata.ErrRecordNotFound) {
		return importProgress{SourceFileID: request.SourceFileID, EntryIDs: map[string]string{}, EntryFiles: map[string]File{}}, 0, nil
	}
	if err != nil {
		return importProgress{}, 0, err
	}
	var state importProgress
	if record.PayloadVersion != DataPayloadVersion || json.Unmarshal(record.Payload, &state) != nil || state.SourceFileID != request.SourceFileID || state.EntryIDs == nil || state.EntryFiles == nil {
		return importProgress{}, 0, errors.New("archive progress record is invalid or belongs to another source")
	}
	return state, record.Revision, nil
}

func (h *Handler) saveProgress(ctx context.Context, request Request, state importProgress, revision int64) (int64, error) {
	payload, err := json.Marshal(state)
	if err != nil {
		return revision, err
	}
	record, err := h.progress.Put(ctx, h.progressKey(request), recorddata.RecordValue{
		Payload: payload, PayloadVersion: DataPayloadVersion, IndexData: json.RawMessage(`{}`),
	}, revision)
	if err != nil {
		return revision, err
	}
	return record.Revision, nil
}

func (h *Handler) importEntries(ctx context.Context, request Request, source contracts.File, spool *os.File, size int64, entries []File, state importProgress, revision int64) (Result, error) {
	if _, err := h.index.GetManagedFileSet(ctx, h.targetFileSetID); err != nil {
		return Result{}, fmt.Errorf("resolve configured archive target set: %w", err)
	}
	verifiedPaths := make(map[string]File, len(entries))
	for _, entry := range entries {
		verifiedPaths[entry.Path] = entry
	}
	for entryPath, fileID := range state.EntryIDs {
		entry, ok := verifiedPaths[entryPath]
		if !ok || strings.TrimSpace(fileID) == "" || state.EntryFiles[entryPath] != entry {
			return Result{}, errors.New("archive progress contains an unverified output reference")
		}
	}
	if state.SourceFileID != request.SourceFileID {
		return Result{}, errors.New("archive progress belongs to another source")
	}
	if state.SourceRevision != 0 && state.SourceRevision != source.Revision || state.SourceSHA256 != "" && !strings.EqualFold(state.SourceSHA256, source.SHA256) {
		return Result{}, errors.New("archive source changed during import")
	}
	state.SourceRevision = source.Revision
	state.SourceSHA256 = strings.ToLower(source.SHA256)
	if err := h.ensureMembership(ctx, request.SourceFileID); err != nil {
		return Result{}, fmt.Errorf("add archive source to target set: %w", err)
	}
	archive, err := zip.NewReader(spool, size)
	if err != nil {
		return Result{}, errors.New("open verified archive for import")
	}
	zipEntries := make(map[string]*zip.File, len(archive.File))
	for _, entry := range archive.File {
		if !entry.FileInfo().IsDir() {
			zipEntries[entry.Name] = entry
		}
	}
	for _, item := range entries {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		if id := state.EntryIDs[item.Path]; id == "" {
			entry := zipEntries[item.Path]
			if entry == nil {
				return Result{}, errors.New("verified archive entry disappeared")
			}
			reader, err := entry.Open()
			if err != nil {
				return Result{}, errors.New("open verified output entry")
			}
			requestKey, _ := FileRequestKey(request.TaskID, request.SourceFileID, item.Path)
			id, writeErr := h.files.CreateFileVerified(ctx, requestKey, source.Groups,
				map[string]string{"archive_job_id": request.TaskID, "archive_source_id": request.SourceFileID, "archive_path": item.Path},
				"application/octet-stream", item.Size, item.SHA256,
				&contextReader{ctx: ctx, reader: reader})
			closeErr := reader.Close()
			if writeErr != nil {
				return Result{}, writeErr
			}
			if closeErr != nil || strings.TrimSpace(id) == "" {
				return Result{}, errors.New("Core returned an invalid archive output")
			}
			state.EntryIDs[item.Path] = id
			state.EntryFiles[item.Path] = item
			revision, err = h.saveProgress(ctx, request, state, revision)
			if err != nil {
				return Result{}, fmt.Errorf("persist archive output progress: %w", err)
			}
		}
	}
	_, err = h.verifyOutputs(ctx, request, state.EntryIDs, verifiedPaths)
	if err != nil {
		return Result{}, err
	}
	state.OutputsVerified = true
	revision, err = h.saveProgress(ctx, request, state, revision)
	if err != nil {
		return Result{}, fmt.Errorf("persist verified archive result before source cleanup: %w", err)
	}
	return h.finishSourceCleanup(ctx, request, state, revision)
}

func (h *Handler) verifyOutputs(ctx context.Context, request Request, entryIDs map[string]string, expected map[string]File) ([]string, error) {
	paths := make([]string, 0, len(entryIDs))
	for archivePath := range entryIDs {
		paths = append(paths, archivePath)
	}
	sort.Strings(paths)
	ids := make([]string, 0, len(paths))
	for _, archivePath := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		want, ok := expected[archivePath]
		if !ok {
			return nil, errors.New("archive progress references an unverified output")
		}
		file, err := h.files.StatFile(ctx, entryIDs[archivePath])
		if err != nil || file.ID != entryIDs[archivePath] || file.Size != want.Size || !strings.EqualFold(file.SHA256, want.SHA256) ||
			file.Metadata["archive_job_id"] != request.TaskID || file.Metadata["archive_source_id"] != request.SourceFileID || file.Metadata["archive_path"] != archivePath {
			return nil, errors.New("archive output verification failed")
		}
		if err := h.ensureMembership(ctx, file.ID); err != nil {
			return nil, fmt.Errorf("reconcile archive output membership: %w", err)
		}
		ids = append(ids, file.ID)
	}
	if len(ids) != len(expected) {
		return nil, errors.New("archive output set is incomplete")
	}
	return ids, nil
}

func (h *Handler) finishSourceCleanup(ctx context.Context, request Request, state importProgress, revision int64) (Result, error) {
	if !state.OutputsVerified || state.SourceRevision <= 0 || state.SourceSHA256 == "" || len(state.EntryIDs) == 0 || len(state.EntryFiles) != len(state.EntryIDs) {
		return Result{}, errors.New("archive outputs are not durably verified")
	}
	ids, err := h.reconcileSavedOutputs(ctx, request, state.EntryIDs, state.EntryFiles)
	if err != nil {
		return Result{}, err
	}
	if err := h.removeMembership(ctx, request.SourceFileID); err != nil {
		return Result{}, fmt.Errorf("remove source archive from target set: %w", err)
	}
	if err := h.files.DeleteFile(ctx, request.SourceFileID, state.SourceRevision); err != nil && !errors.Is(err, contracts.ErrFileNotFound) {
		return Result{}, fmt.Errorf("delete source archive after durable output verification: %w", err)
	}
	state.Ready = true
	if _, err := h.saveProgress(ctx, request, state, revision); err != nil {
		return Result{}, fmt.Errorf("persist archive cleanup completion: %w", err)
	}
	return Result{Ready: true, FileIDs: ids}, nil
}

func (h *Handler) reconcileSavedOutputs(ctx context.Context, request Request, entryIDs map[string]string, expected map[string]File) ([]string, error) {
	paths := make([]string, 0, len(entryIDs))
	for p := range entryIDs {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	ids := make([]string, 0, len(paths))
	for _, p := range paths {
		want, ok := expected[p]
		if !ok {
			return nil, errors.New("saved archive output digest is missing")
		}
		file, err := h.files.StatFile(ctx, entryIDs[p])
		if err != nil || file.ID != entryIDs[p] || file.Size != want.Size || !strings.EqualFold(file.SHA256, want.SHA256) ||
			file.Metadata["archive_job_id"] != request.TaskID || file.Metadata["archive_source_id"] != request.SourceFileID || file.Metadata["archive_path"] != p {
			return nil, errors.New("saved archive output is no longer verifiable")
		}
		if err := h.ensureMembership(ctx, file.ID); err != nil {
			return nil, err
		}
		ids = append(ids, file.ID)
	}
	return ids, nil
}

func (h *Handler) reconcileReady(ctx context.Context, request Request, state importProgress) (Result, error) {
	if len(state.EntryIDs) == 0 || len(state.EntryIDs) != len(state.EntryFiles) {
		return Result{}, errors.New("ready archive state is incomplete")
	}
	ids, err := h.reconcileSavedOutputs(ctx, request, state.EntryIDs, state.EntryFiles)
	if err != nil {
		return Result{}, err
	}
	return Result{Ready: true, FileIDs: ids}, nil
}

func (h *Handler) ensureMembership(ctx context.Context, fileID string) error {
	present, err := h.hasMembership(ctx, fileID)
	if err != nil || present {
		return err
	}
	for attempt := 0; attempt < 5; attempt++ {
		set, err := h.index.GetManagedFileSet(ctx, h.targetFileSetID)
		if err != nil {
			return err
		}
		if _, err = h.index.AddFileToManagedSet(ctx, h.targetFileSetID, fileID, set.Revision); err == nil {
			return nil
		} else if !errors.Is(err, contracts.ErrFileRevisionConflict) {
			return err
		}
	}
	return errors.New("archive target membership could not be reconciled")
}

func (h *Handler) removeMembership(ctx context.Context, fileID string) error {
	for attempt := 0; attempt < 5; attempt++ {
		present, err := h.hasMembership(ctx, fileID)
		if err != nil || !present {
			return err
		}
		set, err := h.index.GetManagedFileSet(ctx, h.targetFileSetID)
		if err != nil {
			return err
		}
		if _, err = h.index.RemoveFileFromManagedSet(ctx, h.targetFileSetID, fileID, set.Revision); err == nil {
			continue
		} else if !errors.Is(err, contracts.ErrFileRevisionConflict) {
			return err
		}
	}
	present, err := h.hasMembership(ctx, fileID)
	if err != nil {
		return err
	}
	if present {
		return errors.New("archive source membership could not be removed")
	}
	return nil
}

func (h *Handler) hasMembership(ctx context.Context, fileID string) (bool, error) {
	cursor := ""
	for {
		files, next, err := h.index.ListFileSet(ctx, h.targetFileSetID, cursor, 100)
		if err != nil {
			return false, err
		}
		for _, file := range files {
			if file.ID == fileID {
				return true, nil
			}
		}
		if next == "" {
			return false, nil
		}
		cursor = next
	}
}

func readManifest(input io.ReaderAt, size, maximum int64) ([]byte, error) {
	archive, err := zip.NewReader(input, size)
	if err != nil {
		return nil, errors.New("read validated ZIP directory")
	}
	for _, entry := range archive.File {
		if entry.Name != "manifest.json" || entry.FileInfo().IsDir() {
			continue
		}
		if entry.UncompressedSize64 > uint64(maximum) {
			return nil, errors.New("manifest size limit exceeded")
		}
		reader, err := entry.Open()
		if err != nil {
			return nil, errors.New("open archive manifest")
		}
		data, readErr := io.ReadAll(io.LimitReader(reader, maximum+1))
		closeErr := reader.Close()
		if readErr != nil || closeErr != nil || int64(len(data)) > maximum || uint64(len(data)) != entry.UncompressedSize64 {
			return nil, errors.New("read archive manifest")
		}
		return data, nil
	}
	return nil, ErrManifestMissing
}

// validateManifest enforces the target format-2 fields explicitly identified
// by the target contract and checks all runtime asset references against the
// already verified ZIP. It does not execute or import feature code.
func validateManifest(data []byte, files []File) error {
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return errors.New("invalid archive manifest JSON")
	}
	var manifest map[string]json.RawMessage
	if err := json.Unmarshal(data, &manifest); err != nil || manifest == nil {
		return errors.New("invalid archive manifest JSON")
	}
	var base struct {
		FormatVersion int               `json:"formatVersion"`
		Key           string            `json:"key"`
		Name          string            `json:"name"`
		Version       string            `json:"version"`
		EngineAPI     string            `json:"engineApi"`
		Slots         []string          `json:"slots"`
		Arguments     map[string]any    `json:"arguments"`
		DataInputs    []json.RawMessage `json:"dataInputs"`
		Content       json.RawMessage   `json:"content"`
		Form          json.RawMessage   `json:"form"`
		Runtime       json.RawMessage   `json:"runtime"`
		Requests      json.RawMessage   `json:"requests"`
	}
	decoderTop := json.NewDecoder(bytes.NewReader(data))
	decoderTop.DisallowUnknownFields()
	if decoderTop.Decode(&base) != nil || base.FormatVersion != 2 || base.Key == "" || strings.TrimSpace(base.Key) != base.Key ||
		base.Name == "" || base.Version == "" || !semverPattern.MatchString(base.Version) || base.EngineAPI == "" || base.Slots == nil ||
		base.Arguments == nil || base.DataInputs == nil || (len(base.Content) > 0 && string(base.Content) != "null") {
		return errors.New("manifest is not a valid target format-2 manifest")
	}
	for _, slot := range base.Slots {
		if slot == "" || strings.TrimSpace(slot) != slot {
			return errors.New("invalid manifest slot")
		}
	}
	inputKeys := make(map[string]struct{}, len(base.DataInputs))
	for _, input := range base.DataInputs {
		var port struct {
			Key     string                     `json:"key"`
			Kind    string                     `json:"kind"`
			Type    string                     `json:"type"`
			Values  []string                   `json:"values"`
			Profile map[string]json.RawMessage `json:"profile"`
			Fields  []struct {
				Path    string                     `json:"path"`
				Type    string                     `json:"type"`
				Values  []string                   `json:"values"`
				Profile map[string]json.RawMessage `json:"profile"`
			} `json:"fields"`
		}
		portDecoder := json.NewDecoder(bytes.NewReader(input))
		portDecoder.DisallowUnknownFields()
		if len(input) == 0 || portDecoder.Decode(&port) != nil || port.Key == "" || strings.TrimSpace(port.Key) != port.Key ||
			(port.Kind != "scalar" && port.Kind != "record" && port.Kind != "collection") {
			return errors.New("invalid manifest data input")
		}
		if port.Type != "" && !validDataFieldType(port.Type) {
			return errors.New("invalid manifest data input type")
		}
		for _, field := range port.Fields {
			if field.Path == "" || strings.TrimSpace(field.Path) != field.Path || !validDataFieldType(field.Type) {
				return errors.New("invalid manifest data input field")
			}
		}
		if _, exists := inputKeys[port.Key]; exists {
			return errors.New("duplicate manifest data input key")
		}
		inputKeys[port.Key] = struct{}{}
	}
	if len(base.Form) > 0 && string(base.Form) != "null" {
		var form struct {
			SubmissionSchema json.RawMessage `json:"submissionSchema"`
		}
		formDecoder := json.NewDecoder(bytes.NewReader(base.Form))
		formDecoder.DisallowUnknownFields()
		if formDecoder.Decode(&form) != nil {
			return errors.New("invalid manifest form contract")
		}
		var schemaObject map[string]json.RawMessage
		if len(form.SubmissionSchema) == 0 || json.Unmarshal(form.SubmissionSchema, &schemaObject) != nil || schemaObject == nil {
			return errors.New("manifest form requires an object submissionSchema")
		}
	}
	var runtime struct {
		ABI          string   `json:"abi"`
		Entry        string   `json:"entry"`
		Styles       []string `json:"styles"`
		SharedStyles []string `json:"sharedStyles"`
		Origins      []string `json:"origins"`
		SDK          []string `json:"sdk"`
	}
	decoder := json.NewDecoder(bytes.NewReader(base.Runtime))
	decoder.DisallowUnknownFields()
	if len(base.Runtime) == 0 || string(base.Runtime) == "null" || decoder.Decode(&runtime) != nil || runtime.ABI != "static-lifecycle" ||
		!validPath(runtime.Entry) || (path.Ext(runtime.Entry) != ".js" && path.Ext(runtime.Entry) != ".mjs") {
		return errors.New("manifest runtime must use static-lifecycle and a local ESM entry")
	}
	for _, origin := range runtime.Origins {
		if !validFeatureOrigin(origin) {
			return errors.New("manifest runtime declares an unsafe origin")
		}
	}
	for _, name := range runtime.SDK {
		if name == "" || strings.TrimSpace(name) != name {
			return errors.New("invalid manifest runtime SDK export")
		}
	}
	available := make(map[string]struct{}, len(files))
	for _, file := range files {
		available[file.Path] = struct{}{}
	}
	if _, ok := available[runtime.Entry]; !ok {
		return errors.New("manifest runtime entry is missing from archive")
	}
	for _, group := range [][]string{runtime.Styles, runtime.SharedStyles} {
		for _, asset := range group {
			if !validPath(asset) || path.Ext(asset) != ".css" {
				return errors.New("invalid manifest stylesheet path")
			}
			if _, ok := available[asset]; !ok {
				return errors.New("manifest stylesheet is missing from archive")
			}
		}
	}
	return nil
}

func validDataFieldType(value string) bool {
	switch value {
	case "text", "longText", "integer", "number", "boolean", "date", "dateTime", "url", "email", "enum", "media":
		return true
	default:
		return false
	}
}

func rejectDuplicateJSONKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := consumeJSONValue(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return errors.New("trailing JSON value")
	}
	return nil
}

func consumeJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("invalid JSON object key")
			}
			if _, exists := seen[key]; exists {
				return errors.New("duplicate JSON object key")
			}
			seen[key] = struct{}{}
			if err := consumeJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return errors.New("unterminated JSON object")
		}
	case '[':
		for decoder.More() {
			if err := consumeJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return errors.New("unterminated JSON array")
		}
	default:
		return errors.New("unexpected JSON delimiter")
	}
	return nil
}

// FileRequestKey returns a deterministic UUIDv5 request key for a task, source
// archive and normalized entry path.
func FileRequestKey(taskID, sourceFileID, entryPath string) (string, error) {
	if strings.TrimSpace(taskID) == "" || strings.TrimSpace(sourceFileID) == "" || !validPath(entryPath) {
		return "", errors.New("invalid archive request key input")
	}
	name := []byte("archive-file/v1\x00" + taskID + "\x00" + sourceFileID + "\x00" + entryPath)
	hash := sha1.New()
	_, _ = hash.Write(requestKeyNamespace[:])
	_, _ = hash.Write(name)
	id := hash.Sum(nil)[:16]
	id[6] = (id[6] & 0x0f) | 0x50
	id[8] = (id[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(id)
	return encoded[:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:], nil
}

var _ Worker = (*Handler)(nil)
