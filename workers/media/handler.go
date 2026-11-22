package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"time"

	core "github.com/Zubrium/cdn/core/presentation"
	mediaprofile "github.com/Zubrium/cdn/pkg/contracts/media"
)

const (
	DataModuleID       = "worker.media"
	DataNamespace      = "media"
	DataScopeType      = "task"
	DataPayloadVersion = 1
	OperationID        = "derivative"
)

type Descriptor struct{}

func (Descriptor) ComponentDescriptor() core.ComponentDescriptor { return ComponentDescriptor() }
func (Descriptor) DataDescriptor() core.ModuleDataDescriptor     { return DataDescriptor() }
func (Descriptor) Operations() []core.Operation                  { return nil }

var (
	_ core.ComponentDescriptorProvider = Descriptor{}
	_ core.Module                      = Descriptor{}
)

func ComponentDescriptor() core.ComponentDescriptor {
	return core.ComponentDescriptor{ID: DataModuleID, Kind: "worker", Version: 1,
		Operations: []core.ComponentOperation{
			{ID: OperationID, ContractVersion: 1},
			{ID: OperationID, ContractVersion: mediaprofile.WorkContractVersion},
		}}
}

// DataDescriptor declares media records under this worker's own Core module
// identity. The namespace is not the Website module's website/media namespace.
func DataDescriptor() core.ModuleDataDescriptor {
	return core.ModuleDataDescriptor{ModuleID: DataModuleID, Namespaces: []core.ModuleDataNamespace{{
		Name: DataNamespace, ScopeTypes: []string{DataScopeType},
	}}}
}

var (
	ErrNotReady        = errors.New("media_worker_not_ready")
	ErrTaskConflict    = errors.New("media_job_conflict")
	ErrOutputUncertain = errors.New("media_output_reconciliation_required")
	ErrOutputInvalid   = errors.New("media_output_invalid")
)

type SessionCapability interface {
	// WithSession must supply an authenticated, revocation-aware Core session.
	WithSession(context.Context, func(core.Session) error) error
}

type FileCapability interface {
	StatFile(context.Context, core.Session, string) (core.File, error)
	ReadFile(context.Context, core.Session, string) (io.ReadCloser, core.File, error)
	CreateFileVerified(context.Context, core.Session, string, []string, map[string]string, string, int64, string, io.Reader) (string, error)
}

type DataCapability interface {
	Get(context.Context, core.ModuleDataKey) (core.ModuleDataRecord, error)
	Put(context.Context, core.ModuleDataKey, core.ModuleDataValue, int64) (core.ModuleDataRecord, error)
}

type ProcessedOutput struct {
	Body        io.ReadCloser
	Size        int64
	SHA256      string
	ContentType string
}

type Runner interface {
	Run(context.Context, Preset, io.Reader) (ProcessedOutput, error)
	// Isolated is true only when an external supervisor enforces OS-level
	// CPU, memory, filesystem, network and process limits for this execution.
	Isolated() bool
}

type RunRecord struct {
	Version          int       `json:"version"`
	TaskID           string    `json:"job_id"`
	SourceFileID     string    `json:"source_file_id"`
	Profile          string    `json:"profile"`
	ProfileVersion   string    `json:"profile_version"`
	IdempotencyKey   string    `json:"idempotency_key"`
	Fingerprint      string    `json:"fingerprint"`
	State            string    `json:"state"`
	Result           *Result   `json:"result,omitempty"`
	OutputRequestKey string    `json:"output_request_key,omitempty"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type Handler struct {
	registry  *Registry
	sessions  SessionCapability
	files     FileCapability
	data      DataCapability
	runner    Runner
	semaphore chan struct{}
	// outputGroups is intentionally absent: output groups are derived from the
	// source file's current Core metadata for every execution/reconciliation.
}

// NewHandler refuses runners without a separately supervised OS isolation
// boundary. FFmpegRunner is deliberately not marked isolated.
func NewHandler(registry *Registry, sessions SessionCapability, files FileCapability, data DataCapability, runner Runner, maxConcurrency int, maxStagedBytes int64) (*Handler, error) {
	if registry == nil || sessions == nil || files == nil || data == nil || runner == nil || !runner.Isolated() || maxConcurrency <= 0 || maxStagedBytes <= 0 ||
		!registry.validExecutionLimits(maxConcurrency, maxStagedBytes) {
		return nil, ErrNotReady
	}
	return &Handler{registry: registry, sessions: sessions, files: files, data: data, runner: runner, semaphore: make(chan struct{}, maxConcurrency)}, nil
}

func (h *Handler) WorkerHandler(ctx context.Context, task core.Task) (core.TaskOutcome, error) {
	if task.Kind != core.TaskKindWorker || task.Worker == nil || task.Worker.ComponentID != "worker.media" ||
		task.Worker.OperationID != OperationID || task.Worker.ContractVersion != 1 || len(task.Payload) == 0 || len(task.Payload) > 16<<10 {
		return core.TaskOutcome{}, ErrInvalidRequest
	}
	decoder := json.NewDecoder(bytes.NewReader(task.Payload))
	decoder.DisallowUnknownFields()
	var request Request
	if err := decoder.Decode(&request); err != nil {
		return core.TaskOutcome{}, ErrInvalidRequest
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return core.TaskOutcome{}, ErrInvalidRequest
	}
	if (request.TaskID != "" && request.TaskID != task.ID.String()) || request.IdempotencyKey != task.IdempotencyKey {
		return core.TaskOutcome{}, ErrInvalidRequest
	}
	request.TaskID = task.ID.String()
	result, err := h.Handle(ctx, request)
	if err != nil {
		return core.TaskOutcome{}, err
	}
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded) > 1<<20 {
		return core.TaskOutcome{}, ErrOutputInvalid
	}
	return core.TaskOutcome{Result: encoded}, nil
}

// Handle executes one trusted, versioned worker request. Durable state is
// written with optimistic revisions before/after external effects; ambiguous
// create outcomes are reconciled by the same Core idempotency key.
func (h *Handler) Handle(ctx context.Context, request Request) (Result, error) {
	if h == nil || ctx == nil || h.registry == nil || h.sessions == nil || h.files == nil || h.data == nil || h.runner == nil || h.semaphore == nil || !h.runner.Isolated() {
		return Result{}, ErrNotReady
	}
	preset, err := h.registry.ValidateRequest(request)
	if err != nil {
		return Result{}, err
	}
	select {
	case h.semaphore <- struct{}{}:
		defer func() { <-h.semaphore }()
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
	var result Result
	err = h.sessions.WithSession(ctx, func(session core.Session) error {
		var callErr error
		result, callErr = h.handleWithSession(ctx, session, request, preset)
		return callErr
	})
	return result, err
}

func (h *Handler) handleWithSession(ctx context.Context, session core.Session, request Request, preset Preset) (Result, error) {
	source, err := h.files.StatFile(ctx, session, request.SourceFileID)
	if err != nil {
		return Result{}, err
	}
	if source.ID != request.SourceFileID {
		return Result{}, ErrTaskConflict
	}
	if err := ValidateSource(preset, source.ContentType, source.Size); err != nil {
		return Result{}, err
	}
	fingerprint, err := Fingerprint(source.SHA256, request)
	if err != nil {
		return Result{}, err
	}
	key := taskRunKey(request.TaskID)
	record, err := h.data.Get(ctx, key)
	if err == nil {
		var persisted RunRecord
		if record.PayloadVersion != DataPayloadVersion || json.Unmarshal(record.Payload, &persisted) != nil || !sameRun(persisted, request) {
			return Result{}, ErrTaskConflict
		}
		if persisted.Fingerprint != fingerprint {
			// A changed source checksum or preset version cannot silently reuse a task.
			return Result{}, ErrTaskConflict
		}
		if persisted.State == "ready" && persisted.Result != nil {
			if err := ValidateOutput(preset, *persisted.Result); err != nil {
				return Result{}, ErrOutputInvalid
			}
			if err := h.verifyOutput(ctx, session, *persisted.Result); err != nil {
				return Result{}, err
			}
			return *persisted.Result, nil
		}
		if persisted.State != "queued" && persisted.State != "retryable_failed" && persisted.State != "processing" && persisted.State != "storing" {
			return Result{}, ErrTaskConflict
		}
	} else if errors.Is(err, core.ErrModuleDataNotFound) {
		record = core.ModuleDataRecord{}
	} else {
		return Result{}, err
	}

	state := RunRecord{Version: DataPayloadVersion, TaskID: request.TaskID, SourceFileID: request.SourceFileID,
		Profile: request.Profile, ProfileVersion: request.ProfileVersion, IdempotencyKey: request.IdempotencyKey,
		Fingerprint: fingerprint, State: "queued", UpdatedAt: time.Now().UTC()}
	if record.Revision > 0 {
		var prior RunRecord
		if record.PayloadVersion != DataPayloadVersion || json.Unmarshal(record.Payload, &prior) != nil || (prior.State != "retryable_failed" && prior.State != "queued" && prior.State != "processing" && prior.State != "storing") {
			return Result{}, ErrTaskConflict
		}
	}
	record, err = h.putStateWithRecord(ctx, key, state, record.Revision)
	if err != nil {
		return Result{}, err
	}

	body, actualSource, err := h.files.ReadFile(ctx, session, request.SourceFileID)
	if err != nil {
		return Result{}, err
	}
	defer body.Close()
	if actualSource.SHA256 != source.SHA256 || actualSource.Size != source.Size || !sameGroups(actualSource.Groups, source.Groups) {
		return Result{}, ErrTaskConflict
	}
	if err := ValidateSource(preset, actualSource.ContentType, actualSource.Size); err != nil {
		return Result{}, err
	}
	state.State = "processing"
	state.UpdatedAt = time.Now().UTC()
	record, err = h.putStateWithRecord(ctx, key, state, record.Revision)
	if err != nil {
		return Result{}, err
	}
	output, err := h.runner.Run(ctx, preset, body)
	if err != nil {
		state.State = "retryable_failed"
		state.UpdatedAt = time.Now().UTC()
		_ = h.putState(ctx, key, state, record.Revision)
		return Result{}, err
	}
	if output.Body == nil {
		return Result{}, ErrOutputInvalid
	}
	defer output.Body.Close()
	if output.Size <= 0 || output.Size > preset.MaxOutputBytes || output.ContentType != preset.OutputContentType || !sha256Token.MatchString(output.SHA256) {
		return Result{}, ErrOutputInvalid
	}
	state.State = "storing"
	state.OutputRequestKey = outputRequestKey(request, fingerprint)
	state.UpdatedAt = time.Now().UTC()
	record, err = h.putStateWithRecord(ctx, key, state, record.Revision)
	if err != nil {
		return Result{}, err
	}
	metadata := map[string]string{"worker": "media", "job_id": request.TaskID, "source_file_id": request.SourceFileID, "fingerprint": fingerprint}
	outputID, err := h.files.CreateFileVerified(ctx, session, state.OutputRequestKey, actualSource.Groups, metadata,
		output.ContentType, output.Size, output.SHA256, output.Body)
	if err != nil {
		// A retry with the same request key asks Core to reconcile idempotently.
		return Result{}, ErrOutputUncertain
	}
	final := Result{OutputFileID: outputID, Checksum: output.SHA256, ContentType: output.ContentType, Size: output.Size}
	if err := ValidateOutput(preset, final); err != nil {
		return Result{}, ErrOutputInvalid
	}
	if err := h.verifyOutput(ctx, session, final); err != nil {
		return Result{}, err
	}
	state.State, state.Result, state.UpdatedAt = "ready", &final, time.Now().UTC()
	if _, err := h.putStateWithRecord(ctx, key, state, record.Revision); err != nil {
		return Result{}, err
	}
	return final, nil
}

func (h *Handler) verifyOutput(ctx context.Context, session core.Session, expected Result) error {
	file, err := h.files.StatFile(ctx, session, expected.OutputFileID)
	if err != nil {
		return ErrOutputUncertain
	}
	if file.ID != expected.OutputFileID || file.SHA256 != expected.Checksum || file.ContentType != expected.ContentType || file.Size != expected.Size {
		return ErrOutputInvalid
	}
	return nil
}

func (h *Handler) putState(ctx context.Context, key core.ModuleDataKey, value RunRecord, revision int64) error {
	_, err := h.putStateWithRecord(ctx, key, value, revision)
	return err
}

func (h *Handler) putStateWithRecord(ctx context.Context, key core.ModuleDataKey, state RunRecord, revision int64) (core.ModuleDataRecord, error) {
	payload, err := json.Marshal(state)
	if err != nil {
		return core.ModuleDataRecord{}, err
	}
	return h.data.Put(ctx, key, core.ModuleDataValue{Payload: payload, PayloadVersion: DataPayloadVersion, IndexData: json.RawMessage(`{}`)}, revision)
}

func taskRunKey(id string) core.ModuleDataKey {
	return core.ModuleDataKey{Namespace: DataNamespace, ScopeType: DataScopeType, ScopeID: id, RecordKey: "task"}
}
func sameRun(saved RunRecord, request Request) bool {
	return saved.Version == DataPayloadVersion && saved.TaskID == request.TaskID && saved.SourceFileID == request.SourceFileID &&
		saved.Profile == request.Profile && saved.ProfileVersion == request.ProfileVersion && saved.IdempotencyKey == request.IdempotencyKey
}
func outputRequestKey(request Request, fingerprint string) string {
	sum := sha256.Sum256([]byte(request.IdempotencyKey + "\x00" + fingerprint))
	return "media-" + hex.EncodeToString(sum[:])
}
func sameGroups(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	counts := make(map[string]int, len(left))
	for _, item := range left {
		counts[item]++
	}
	for _, item := range right {
		counts[item]--
		if counts[item] < 0 {
			return false
		}
	}
	for _, count := range counts {
		if count != 0 {
			return false
		}
	}
	return true
}
