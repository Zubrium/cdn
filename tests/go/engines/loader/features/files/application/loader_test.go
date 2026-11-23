package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Zubrium/cdn/pkg/contracts"
	"io"
	"sort"
	"testing"
	"time"

	api "github.com/Zubrium/cdn/engines/loader/presentation"
	"github.com/google/uuid"
)

type testIdentity struct {
	principalID string
	writable    bool
	active      bool
	sessions    *testIdentitySessions
}

type testIdentitySessions struct {
	principal contracts.Principal
	err       error
	token     string
	enabled   bool
}

func (i *testIdentity) AuthenticateSession(_ context.Context, token string) (contracts.AuthorizationSubject, contracts.Principal, error) {
	if i.sessions == nil || !i.sessions.enabled {
		return contracts.AuthorizationSubject{}, contracts.Principal{}, contracts.ErrAccessUnauthorized
	}
	i.sessions.token = token
	return contracts.AuthorizationSubject{SessionToken: token}, i.sessions.principal, i.sessions.err
}

func (i *testIdentity) Authenticate(context.Context, string) (contracts.AuthorizationSubject, contracts.Principal, error) {
	return contracts.AuthorizationSubject{}, contracts.Principal{ID: i.principalID, Active: i.active}, nil
}

func (i *testIdentity) RequireGroupLevel(_ context.Context, _ contracts.AuthorizationSubject, groups []string, required contracts.AccessLevel) error {
	if !i.writable || len(groups) == 0 || required != contracts.AccessLevelWrite {
		return contracts.ErrAccessForbidden
	}
	return nil
}

type testFiles struct {
	Files
	staged        map[string][]byte
	files         map[string]contracts.File
	byKey         map[string]string
	last          []byte
	beforeCreate  func()
	requestStatus contracts.UploadRequestStatus
	requestErr    error
}

func newTestFiles() *testFiles {
	return &testFiles{staged: make(map[string][]byte), files: make(map[string]contracts.File), byKey: make(map[string]string)}
}

func (f *testFiles) PutStagedObject(_ context.Context, _ contracts.AuthorizationSubject, id string, _ []string, size int64, checksum string, body io.Reader) error {
	if existing, ok := f.staged[id]; ok {
		if int64(len(existing)) != size || digest(existing) != checksum {
			return contracts.ErrFileRequestConflict
		}
		return nil
	}
	value, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	if int64(len(value)) != size || digest(value) != checksum {
		return api.ErrInvalid
	}
	f.staged[id] = value
	return nil
}

func (f *testFiles) ReadStagedObject(_ context.Context, _ contracts.AuthorizationSubject, id string) (io.ReadCloser, int64, string, error) {
	value, ok := f.staged[id]
	if !ok {
		return nil, 0, "", contracts.ErrFileNotFound
	}
	return io.NopCloser(bytes.NewReader(value)), int64(len(value)), digest(value), nil
}

func (f *testFiles) PurgeStagedObject(_ context.Context, id string) error {
	delete(f.staged, id)
	return nil
}

func (f *testFiles) CreateFileVerified(_ context.Context, _ contracts.AuthorizationSubject, requestKey string, groups []string, metadata map[string]string, contentType string, size int64, checksum string, body io.Reader) (string, error) {
	if f.beforeCreate != nil {
		f.beforeCreate()
	}
	if id, ok := f.byKey[requestKey]; ok {
		return id, nil
	}
	value, err := io.ReadAll(body)
	if err != nil {
		return "", err
	}
	if int64(len(value)) != size || digest(value) != checksum {
		return "", api.ErrInvalid
	}
	id := uuid.NewString()
	f.files[id] = contracts.File{ID: id, Groups: append([]string(nil), groups...), Metadata: clone(metadata), ContentType: contentType, SHA256: checksum, Revision: 1, Size: size}
	f.byKey[requestKey] = id
	f.last = append([]byte(nil), value...)
	return id, nil
}

func (f *testFiles) StatFile(_ context.Context, _ contracts.AuthorizationSubject, id string) (contracts.File, error) {
	file, ok := f.files[id]
	if !ok {
		return contracts.File{}, contracts.ErrFileNotFound
	}
	file.Groups = append([]string(nil), file.Groups...)
	file.Metadata = clone(file.Metadata)
	return file, nil
}

func (f *testFiles) ResolveUploadRequest(context.Context, string, string) (contracts.UploadRequestStatus, error) {
	return f.requestStatus, f.requestErr
}

type testData struct {
	records      map[contracts.RecordKey]contracts.Record
	now          func() time.Time
	next         int64
	page         int
	indexBytes   int
	batchBytes   int
	beforeDelete func(contracts.RecordKey)
}

func newTestData() *testData {
	return &testData{records: make(map[contracts.RecordKey]contracts.Record), now: time.Now, page: 100,
		indexBytes: 1 << 16, batchBytes: 4 << 20}
}

func (d *testData) MaxPageSize() int     { return d.page }
func (d *testData) MaxBatchSize() int    { return 10 }
func (d *testData) MaxPayloadBytes() int { return 1 << 20 }
func (d *testData) MaxIndexBytes() int   { return d.indexBytes }
func (d *testData) MaxBatchBytes() int   { return d.batchBytes }

func (d *testData) Get(_ context.Context, key contracts.RecordKey) (contracts.Record, error) {
	record, ok := d.records[key]
	if !ok || expired(record, d.now()) {
		return contracts.Record{}, contracts.ErrRecordNotFound
	}
	return cloneRecord(record), nil
}

func (d *testData) GetExpired(_ context.Context, key contracts.RecordKey) (contracts.Record, error) {
	record, ok := d.records[key]
	if !ok || !expired(record, d.now()) {
		return contracts.Record{}, contracts.ErrRecordNotFound
	}
	return cloneRecord(record), nil
}

func (d *testData) Put(_ context.Context, key contracts.RecordKey, value contracts.RecordValue, expected int64) (contracts.Record, error) {
	records := cloneRecords(d.records)
	record, err := d.write(records, key, value, expected)
	if err != nil {
		return contracts.Record{}, err
	}
	d.records = records
	return record, nil
}

func (d *testData) Delete(_ context.Context, key contracts.RecordKey, expected int64) error {
	if d.beforeDelete != nil {
		d.beforeDelete(key)
	}
	record, ok := d.records[key]
	if !ok || expired(record, d.now()) {
		return contracts.ErrRecordNotFound
	}
	if record.Revision != expected {
		return contracts.ErrRecordConflict
	}
	delete(d.records, key)
	return nil
}

func (d *testData) Batch(_ context.Context, mutations []contracts.RecordMutation) error {
	if err := validateTestDataBatch(mutations); err != nil {
		return contracts.ErrRecordInvalid
	}
	records := cloneRecords(d.records)
	next := d.next
	for _, mutation := range mutations {
		if mutation.Value == nil {
			current, ok := records[mutation.Key]
			if !ok || expired(current, d.now()) {
				return contracts.ErrRecordNotFound
			}
			if current.Revision != mutation.ExpectedRevision {
				return contracts.ErrRecordConflict
			}
			delete(records, mutation.Key)
			continue
		}
		oldNext := d.next
		d.next = next
		record, err := d.write(records, mutation.Key, *mutation.Value, mutation.ExpectedRevision)
		next = d.next
		d.next = oldNext
		if err != nil {
			return err
		}
		records[mutation.Key] = record
	}
	d.records, d.next = records, next
	return nil
}

func validateTestDataBatch(mutations []contracts.RecordMutation) error {
	if len(mutations) == 0 {
		return contracts.ErrRecordInvalid
	}
	first := mutations[0].Key
	seen := make(map[contracts.RecordKey]struct{}, len(mutations))
	for _, mutation := range mutations {
		key := mutation.Key
		if key.Namespace != first.Namespace || key.ScopeType != first.ScopeType || key.ScopeID != first.ScopeID || mutation.ExpectedRevision < 0 ||
			(mutation.Value == nil && mutation.ExpectedRevision == 0) {
			return contracts.ErrRecordInvalid
		}
		if _, exists := seen[key]; exists {
			return contracts.ErrRecordInvalid
		}
		seen[key] = struct{}{}
	}
	return nil
}

func (d *testData) List(_ context.Context, namespace, scopeType, scopeID, after string, selectors []contracts.RecordSelector, limit int) ([]contracts.RecordKey, []contracts.Record, string, error) {
	var keys []contracts.RecordKey
	for key, record := range d.records {
		if key.Namespace == namespace && key.ScopeType == scopeType && key.ScopeID == scopeID && key.RecordKey > after && !expired(record, d.now()) && matchesSelectors(record.IndexData, selectors) {
			keys = append(keys, key)
		}
	}
	return d.pageRecords(keys, limit)
}

func (d *testData) ListExpired(_ context.Context, namespace, scopeType, scopeID, after string, limit int) ([]contracts.RecordKey, []contracts.Record, string, error) {
	var keys []contracts.RecordKey
	for key, record := range d.records {
		if key.Namespace == namespace && key.ScopeType == scopeType && key.ScopeID == scopeID && key.RecordKey > after && expired(record, d.now()) {
			keys = append(keys, key)
		}
	}
	return d.pageRecords(keys, limit)
}

func (d *testData) pageRecords(keys []contracts.RecordKey, limit int) ([]contracts.RecordKey, []contracts.Record, string, error) {
	sort.Slice(keys, func(i, j int) bool { return keys[i].RecordKey < keys[j].RecordKey })
	next := ""
	if len(keys) > limit {
		keys = keys[:limit]
		next = keys[len(keys)-1].RecordKey
	}
	records := make([]contracts.Record, len(keys))
	for i, key := range keys {
		records[i] = cloneRecord(d.records[key])
	}
	return keys, records, next, nil
}

func (d *testData) write(records map[contracts.RecordKey]contracts.Record, key contracts.RecordKey, value contracts.RecordValue, expected int64) (contracts.Record, error) {
	current, exists := records[key]
	if exists && expired(current, d.now()) {
		exists = false
	}
	if (!exists && expected != 0) || (exists && current.Revision != expected) || (exists && expected == 0) {
		return contracts.Record{}, contracts.ErrRecordConflict
	}
	d.next++
	record := contracts.Record{Payload: append(json.RawMessage(nil), value.Payload...), PayloadVersion: value.PayloadVersion,
		IndexData: append(json.RawMessage(nil), value.IndexData...), Revision: d.next, CreatedAt: d.now(), UpdatedAt: d.now()}
	if value.TTL > 0 {
		expires := d.now().Add(value.TTL)
		record.ExpiresAt = &expires
	}
	if exists {
		record.CreatedAt = current.CreatedAt
	}
	records[key] = record
	return record, nil
}

func cloneRecords(source map[contracts.RecordKey]contracts.Record) map[contracts.RecordKey]contracts.Record {
	result := make(map[contracts.RecordKey]contracts.Record, len(source))
	for key, record := range source {
		result[key] = cloneRecord(record)
	}
	return result
}

func cloneRecord(record contracts.Record) contracts.Record {
	record.Payload = append(json.RawMessage(nil), record.Payload...)
	record.IndexData = append(json.RawMessage(nil), record.IndexData...)
	if record.ExpiresAt != nil {
		expires := *record.ExpiresAt
		record.ExpiresAt = &expires
	}
	return record
}

func expired(record contracts.Record, now time.Time) bool {
	return record.ExpiresAt != nil && !record.ExpiresAt.After(now)
}

func matchesSelectors(encoded json.RawMessage, selectors []contracts.RecordSelector) bool {
	var values map[string]any
	if json.Unmarshal(encoded, &values) != nil {
		return false
	}
	for _, selector := range selectors {
		actual, ok := values[selector.Field]
		if !ok {
			return false
		}
		match := false
		switch selector.Operator {
		case contracts.RecordSelectorEqual:
			match = len(selector.Values) == 1 && fmt.Sprint(actual) == fmt.Sprint(selector.Values[0])
		case contracts.RecordSelectorIn:
			for _, expected := range selector.Values {
				match = match || fmt.Sprint(actual) == fmt.Sprint(expected)
			}
		case contracts.RecordSelectorContains:
			array, ok := actual.([]any)
			if ok {
				for _, item := range array {
					match = match || fmt.Sprint(item) == fmt.Sprint(selector.Values[0])
				}
			}
		}
		if !match {
			return false
		}
	}
	return true
}

var _ ModuleData = (*testData)(nil)

func newTestService(t *testing.T) (*Service, context.Context, *testIdentity, *testFiles, *testData) {
	t.Helper()
	files, data := newTestFiles(), newTestData()
	service, ctx, identity := newTestServiceWithFiles(t, files, data)
	return service, ctx, identity, files, data
}

func newTestServiceWithFiles(t *testing.T, files Files, data *testData) (*Service, context.Context, *testIdentity) {
	t.Helper()
	id := &testIdentity{principalID: uuid.NewString(), writable: true, active: true}
	cfg := Config{PartSize: 4, MaxFileBytes: 128, MaxPartBytes: 4, MaxParts: 32, MaxConcurrentCommits: 1, SessionTTL: time.Hour,
		CommitTimeout: time.Minute, Mode: ModeAuto, BufferThreshold: 8, MaxBufferedBytes: 16,
		SessionCacheEntries: 8, SessionCacheMaxBytes: 1 << 20, SessionCacheTTL: time.Minute,
		CleanupInterval: time.Minute, CleanupBatchSize: 16}
	service, err := New(id, files, data, cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := service.Authenticate(context.Background(), "key")
	if err != nil {
		t.Fatal(err)
	}
	return service, ctx, id
}

func TestAuthenticateSessionUsesCoreSessionAndRejectsInactivePrincipal(t *testing.T) {
	identity := &testIdentity{principalID: uuid.NewString(), writable: true, active: true}
	identity.sessions = &testIdentitySessions{enabled: true, principal: contracts.Principal{ID: identity.principalID, Active: true}}
	data := newTestData()
	service, err := New(identity, newTestFiles(), data, testLoaderConfig())
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := service.AuthenticateSession(context.Background(), "opaque-session-token")
	if err != nil {
		t.Fatal(err)
	}
	if identity.sessions.token != "opaque-session-token" {
		t.Fatalf("Core session token=%q", identity.sessions.token)
	}
	principal, ok := ctx.Value(authKey{}).(authenticated)
	if !ok || principal.principalID != identity.principalID {
		t.Fatalf("authenticated context=%+v", principal)
	}

	identity.sessions.principal.Active = false
	if _, err := service.AuthenticateSession(context.Background(), "opaque-session-token"); !errors.Is(err, api.ErrUnauthorized) {
		t.Fatalf("inactive session principal returned %v", err)
	}

	identityWithoutSessions := &testIdentity{principalID: uuid.NewString(), writable: true, active: true}
	serviceWithoutSessions, err := New(identityWithoutSessions, newTestFiles(), newTestData(), testLoaderConfig())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := serviceWithoutSessions.AuthenticateSession(context.Background(), "opaque-session-token"); !errors.Is(err, api.ErrUnauthorized) {
		t.Fatalf("unconfigured session auth returned %v", err)
	}
}

func testLoaderConfig() Config {
	return Config{PartSize: 4, MaxFileBytes: 128, MaxPartBytes: 4, MaxParts: 32, MaxConcurrentCommits: 1, SessionTTL: time.Hour,
		CommitTimeout: time.Minute, Mode: ModeAuto, BufferThreshold: 8, MaxBufferedBytes: 16,
		SessionCacheEntries: 8, SessionCacheMaxBytes: 1 << 20, SessionCacheTTL: time.Minute,
		CleanupInterval: time.Minute, CleanupBatchSize: 16}
}

func TestBeginUploadIdempotencyIsPrincipalScopedAndDoesNotCollapseKeys(t *testing.T) {
	service, ctx, _, _, _ := newTestService(t)
	body := []byte("data")
	input := uploadInput("key-a", body)
	first, err := service.BeginUpload(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := service.BeginUpload(ctx, input)
	if err != nil || retry.ID != first.ID {
		t.Fatalf("same idempotency key did not return its session: first=%s retry=%s err=%v", first.ID, retry.ID, err)
	}
	second, err := service.BeginUpload(ctx, uploadInput("key-b", body))
	if err != nil || second.ID == first.ID {
		t.Fatalf("different idempotency keys collided: first=%s second=%s err=%v", first.ID, second.ID, err)
	}
	changed := uploadInput("key-a", []byte("else"))
	if _, err := service.BeginUpload(ctx, changed); !errors.Is(err, api.ErrConflict) {
		t.Fatalf("reused key with different request was not rejected: %v", err)
	}
}

func TestNewRequiresModuleDataCapacityForLoaderTransactions(t *testing.T) {
	identity := &testIdentity{principalID: uuid.NewString(), writable: true, active: true}
	files := newTestFiles()
	cfg := Config{PartSize: 4, MaxFileBytes: 128, MaxPartBytes: 4, MaxParts: 32, MaxConcurrentCommits: 1, SessionTTL: time.Hour,
		CommitTimeout: time.Minute, Mode: ModeAuto, BufferThreshold: 8, MaxBufferedBytes: 16,
		SessionCacheEntries: 8, SessionCacheMaxBytes: 1 << 20, SessionCacheTTL: time.Minute,
		CleanupInterval: time.Minute, CleanupBatchSize: 16}

	data := newTestData()
	cfg.MaxConcurrentCommits = 0
	if _, err := New(identity, files, data, cfg); err == nil {
		t.Fatal("Loader accepted an implicit commit concurrency limit")
	}
	cfg.MaxConcurrentCommits = 1
	data.indexBytes = MinimumModuleDataIndexBytes - 1
	if _, err := New(identity, files, data, cfg); err == nil {
		t.Fatal("Loader accepted module-data indexes that cannot fit its own projections")
	}

	data = newTestData()
	data.batchBytes = data.MaxPayloadBytes() + ModuleDataBatchOverheadBytes - 1
	if _, err := New(identity, files, data, cfg); err == nil {
		t.Fatal("Loader accepted a batch budget too small for session, idempotency, and cleanup records")
	}
}

func TestIdempotencyTTLTracksSessionDuringAndAfterCommit(t *testing.T) {
	service, ctx, identity, files, data := newTestService(t)
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	service.now, data.now = func() time.Time { return now }, func() time.Time { return now }
	body := []byte("data")
	input := uploadInput("ttl-sync", body)
	upload, err := service.BeginUpload(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	key := idempotencyDataKey(identity.principalID, input.IdempotencyKey)
	assertMatchingExpiry := func(stage string) {
		t.Helper()
		session, sessionErr := data.Get(context.Background(), sessionDataKey(upload.ID))
		idem, idemErr := data.Get(context.Background(), key)
		if sessionErr != nil || idemErr != nil || session.ExpiresAt == nil || idem.ExpiresAt == nil || !session.ExpiresAt.Equal(*idem.ExpiresAt) {
			t.Fatalf("%s session/idempotency TTL mismatch: session=%+v (%v) idempotency=%+v (%v)", stage, session.ExpiresAt, sessionErr, idem.ExpiresAt, idemErr)
		}
	}
	assertMatchingExpiry("begin")
	files.beforeCreate = func() { assertMatchingExpiry("committing") }
	if _, err := service.PutUploadPart(ctx, upload.ID, 1, int64(len(body)), digest(body), bytes.NewReader(body)); err != nil {
		t.Fatal(err)
	}
	file, err := service.CommitUpload(ctx, upload.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertMatchingExpiry("committed")
	retry, err := service.BeginUpload(ctx, input)
	if err != nil || retry.ID != upload.ID || retry.State != "committed" || retry.FileID != file.ID {
		t.Fatalf("idempotent retry did not return the committed session: retry=%+v file=%+v err=%v", retry, file, err)
	}
}

func TestValidUploadRejectsChangedInputFingerprint(t *testing.T) {
	service, ctx, _, _, _ := newTestService(t)
	upload, err := service.BeginUpload(ctx, uploadInput("tamper", []byte("data")))
	if err != nil {
		t.Fatal(err)
	}
	request, err := service.request(ctx)
	if err != nil {
		t.Fatal(err)
	}
	stored, _, err := service.loadUpload(ctx, request, upload.ID)
	if err != nil {
		t.Fatal(err)
	}
	stored.Input.Name = "changed.bin"
	if service.validUpload(stored, stored.ID, stored.PrincipalID) {
		t.Fatal("stored upload accepted input that no longer matches its fingerprint")
	}
}

func TestAuthenticateRejectsInactiveOrMalformedCorePrincipal(t *testing.T) {
	service, _, identity, _, _ := newTestService(t)
	identity.active = false
	if _, err := service.Authenticate(context.Background(), "key"); !errors.Is(err, api.ErrUnauthorized) {
		t.Fatalf("inactive principal authenticated: %v", err)
	}
	identity.active, identity.principalID = true, uuid.Nil.String()
	if _, err := service.Authenticate(context.Background(), "key"); !errors.Is(err, api.ErrUnauthorized) {
		t.Fatalf("nil principal ID authenticated: %v", err)
	}
}

func TestUploadPartsCommitAndCommitRetryReturnSameFile(t *testing.T) {
	service, ctx, _, files, _ := newTestService(t)
	body := []byte("abcdef")
	upload, err := service.BeginUpload(ctx, uploadInput("complete", body))
	if err != nil {
		t.Fatal(err)
	}
	for number, part := range [][]byte{body[:4], body[4:]} {
		partNumber := number + 1
		status, err := service.PutUploadPart(ctx, upload.ID, partNumber, int64(len(part)), digest(part), bytes.NewReader(part))
		if err != nil || len(status.Parts) != partNumber {
			t.Fatalf("upload part %d: parts=%d err=%v", partNumber, len(status.Parts), err)
		}
	}
	file, err := service.CommitUpload(ctx, upload.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(files.last, body) || file.SHA256 != digest(body) || file.Size != int64(len(body)) {
		t.Fatalf("committed file mismatch: file=%+v body=%q", file, files.last)
	}
	retried, err := service.CommitUpload(ctx, upload.ID)
	if err != nil || retried.ID != file.ID {
		t.Fatalf("commit retry did not return the same file: first=%s retry=%s err=%v", file.ID, retried.ID, err)
	}
}

func TestCommitWaitsWithinConfiguredConcurrencyLimit(t *testing.T) {
	service, ctx, _, files, _ := newTestService(t)
	body := []byte("data")
	upload, err := service.BeginUpload(ctx, uploadInput("commit-slot", body))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.PutUploadPart(ctx, upload.ID, 1, int64(len(body)), digest(body), bytes.NewReader(body)); err != nil {
		t.Fatal(err)
	}
	release, err := service.acquireCommitSlot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	blockedCtx, cancel := context.WithTimeout(ctx, 10*time.Millisecond)
	defer cancel()
	if _, err := service.CommitUpload(blockedCtx, upload.ID); !errors.Is(err, api.ErrUnavailable) {
		t.Fatalf("commit without a reserved DB connection pair did not stop at its deadline: %v", err)
	}
	if len(files.files) != 0 {
		t.Fatal("commit waiting for a slot changed upload state or created a file")
	}
	release()
	if _, err := service.CommitUpload(ctx, upload.ID); err != nil {
		t.Fatalf("commit did not resume after a slot became available: %v", err)
	}
}

func TestCommitRechecksCurrentGroupAccess(t *testing.T) {
	service, ctx, identity, files, _ := newTestService(t)
	body := []byte("data")
	upload, err := service.BeginUpload(ctx, uploadInput("revoked", body))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.PutUploadPart(ctx, upload.ID, 1, int64(len(body)), digest(body), bytes.NewReader(body)); err != nil {
		t.Fatal(err)
	}
	identity.writable = false
	if _, err := service.CommitUpload(ctx, upload.ID); !errors.Is(err, api.ErrForbidden) {
		t.Fatalf("commit ignored revoked group access: %v", err)
	}
	if len(files.files) != 0 {
		t.Fatal("file was created after write access had been revoked")
	}
}

func TestAbortSchedulesDurableStagingCleanup(t *testing.T) {
	service, ctx, _, files, data := newTestService(t)
	body := []byte("data")
	upload, err := service.BeginUpload(ctx, uploadInput("abort", body))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.PutUploadPart(ctx, upload.ID, 1, int64(len(body)), digest(body), bytes.NewReader(body)); err != nil {
		t.Fatal(err)
	}
	if err := service.AbortUpload(ctx, upload.ID); err != nil {
		t.Fatal(err)
	}
	if err := service.CleanupOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if len(files.staged) != 0 {
		t.Fatalf("aborted staging objects were not removed: %d remain", len(files.staged))
	}
	if _, err := data.Get(ctx, cleanupDataKey(upload.ID)); !errors.Is(err, contracts.ErrRecordNotFound) {
		t.Fatalf("cleanup job was not retired: %v", err)
	}
}

func TestExpiredUploadCannotBeExtendedByPartWrite(t *testing.T) {
	service, ctx, _, _, data := newTestService(t)
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	service.now, data.now = func() time.Time { return now }, func() time.Time { return now }
	body := []byte("data")
	upload, err := service.BeginUpload(ctx, uploadInput("expires", body))
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Hour)
	if _, err := service.UploadStatus(ctx, upload.ID); !errors.Is(err, api.ErrNotFound) {
		t.Fatalf("expired session remained visible: %v", err)
	}
	if _, err := service.PutUploadPart(ctx, upload.ID, 1, int64(len(body)), digest(body), bytes.NewReader(body)); !errors.Is(err, api.ErrNotFound) {
		t.Fatalf("expired session accepted a part: %v", err)
	}
}

func TestPendingCoreCommitRetainsExpiredSessionAndPartData(t *testing.T) {
	service, ctx, _, _, data := newTestService(t)
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	service.now, data.now = func() time.Time { return now }, func() time.Time { return now }
	body := []byte("data")
	upload, err := service.BeginUpload(ctx, uploadInput("pending-recovery", body))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.PutUploadPart(ctx, upload.ID, 1, int64(len(body)), digest(body), bytes.NewReader(body)); err != nil {
		t.Fatal(err)
	}
	markCommittingExpired(t, service, data, upload.ID, now.Add(30*time.Minute))
	service.files.(*testFiles).requestStatus = contracts.UploadRequestStatus{State: "pending"}
	now = now.Add(2 * time.Hour)

	if err := service.CleanupOnce(ctx); err != nil {
		t.Fatal(err)
	}
	session, err := data.Get(ctx, sessionDataKey(upload.ID))
	if err != nil {
		t.Fatalf("pending Core commit lost its resumable session: %v", err)
	}
	var recovered storedUpload
	if err := json.Unmarshal(session.Payload, &recovered); err != nil || recovered.State != "committing" || !recovered.ExpiresAt.After(now) {
		t.Fatalf("pending Core commit was not retained: upload=%+v err=%v", recovered, err)
	}
	if _, err := data.Get(ctx, partDataKey(upload.ID, 1)); err != nil {
		t.Fatalf("pending Core commit lost its staged part metadata: %v", err)
	}
}

func TestExpiredCommitRecoveryPromotesReadyFileBeforeCleanup(t *testing.T) {
	service, ctx, _, files, data := newTestService(t)
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	service.now, data.now = func() time.Time { return now }, func() time.Time { return now }
	body := []byte("data")
	upload, err := service.BeginUpload(ctx, uploadInput("ready-recovery", body))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.PutUploadPart(ctx, upload.ID, 1, int64(len(body)), digest(body), bytes.NewReader(body)); err != nil {
		t.Fatal(err)
	}
	markCommittingExpired(t, service, data, upload.ID, now.Add(30*time.Minute))
	fileID := uuid.NewString()
	files.requestStatus = contracts.UploadRequestStatus{State: "ready", FileID: fileID}
	now = now.Add(2 * time.Hour)

	if err := service.CleanupOnce(ctx); err != nil {
		t.Fatal(err)
	}
	session, err := data.Get(ctx, sessionDataKey(upload.ID))
	if err != nil {
		t.Fatalf("completed Core commit lost its Loader result: %v", err)
	}
	var recovered storedUpload
	if err := json.Unmarshal(session.Payload, &recovered); err != nil || recovered.State != "committed" || recovered.FileID != fileID {
		t.Fatalf("Core's ready file was not recorded in the Loader session: upload=%+v err=%v", recovered, err)
	}
	if len(files.staged) != 0 {
		t.Fatalf("recovered commit left staged bytes behind: %d remain", len(files.staged))
	}
	if _, err := data.Get(ctx, cleanupDataKey(upload.ID)); !errors.Is(err, contracts.ErrRecordNotFound) {
		t.Fatalf("cleanup job was not retired after confirmed Core commit: %v", err)
	}
}

func TestExpiredCommitRecoveryMarksFailedCoreRequestAborted(t *testing.T) {
	service, ctx, _, _, data := newTestService(t)
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	service.now, data.now = func() time.Time { return now }, func() time.Time { return now }
	body := []byte("data")
	upload, err := service.BeginUpload(ctx, uploadInput("failed-recovery", body))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.PutUploadPart(ctx, upload.ID, 1, int64(len(body)), digest(body), bytes.NewReader(body)); err != nil {
		t.Fatal(err)
	}
	markCommittingExpired(t, service, data, upload.ID, now.Add(30*time.Minute))
	service.files.(*testFiles).requestStatus = contracts.UploadRequestStatus{State: "failed"}
	now = now.Add(2 * time.Hour)

	if err := service.CleanupOnce(ctx); err != nil {
		t.Fatal(err)
	}
	session, err := data.Get(ctx, sessionDataKey(upload.ID))
	if err != nil {
		t.Fatalf("failed Core request lost its Loader result: %v", err)
	}
	var recovered storedUpload
	if err := json.Unmarshal(session.Payload, &recovered); err != nil || recovered.State != "aborted" || recovered.FileID != "" {
		t.Fatalf("failed Core request remained in committing state: upload=%+v err=%v", recovered, err)
	}
}

func TestCleanupCursorRotatesPastPendingCoreCommits(t *testing.T) {
	service, _, identity, files, data := newTestService(t)
	data.page = 1
	service.cfg.CleanupBatchSize = 1
	pendingID := "00000000-0000-4000-8000-000000000001"
	readyID := "00000000-0000-4000-8000-000000000002"
	expiredAt := time.Now().UTC().Add(-time.Hour)
	for _, item := range []struct {
		id            string
		commitAttempt bool
	}{{pendingID, true}, {readyID, false}} {
		state := "aborted"
		if item.commitAttempt {
			state = "committing"
		}
		input := api.BeginUploadInput{Name: "asset.bin", ContentType: "application/octet-stream", SHA256: digest(nil), Groups: []string{"assets"}}
		inputHash, err := hashInput(input)
		if err != nil {
			t.Fatal(err)
		}
		upload := storedUpload{ID: item.id, PrincipalID: identity.principalID, Input: input, InputHash: inputHash,
			IdempotencyKeyHash: sha256Hex("cleanup-test-key"), Mode: ModeStream, State: state,
			CreatedAt: expiredAt.Add(-time.Hour), ExpiresAt: expiredAt}
		sessionValue, err := dataValue(upload, sessionIndex(upload), 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := data.Put(context.Background(), sessionDataKey(item.id), sessionValue, 0); err != nil {
			t.Fatal(err)
		}
		sessionRecord := data.records[sessionDataKey(item.id)]
		sessionRecord.ExpiresAt = &expiredAt
		data.records[sessionDataKey(item.id)] = sessionRecord
		job := cleanupRecord{UploadID: item.id, PrincipalID: identity.principalID, ExpectedParts: 0,
			CommitAttempt: item.commitAttempt, State: "pending"}
		value, err := dataValue(job, uploadIndex("cleanup", item.id, "pending", identity.principalID), 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := data.Put(context.Background(), cleanupDataKey(item.id), value, 0); err != nil {
			t.Fatal(err)
		}
	}
	files.requestStatus = contracts.UploadRequestStatus{State: "pending"}
	if err := service.CleanupOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := service.CleanupOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := data.Get(context.Background(), cleanupDataKey(readyID)); !errors.Is(err, contracts.ErrRecordNotFound) {
		t.Fatalf("a pending earlier commit starved a later cleanup job: %v", err)
	}
	if _, err := data.Get(context.Background(), cleanupDataKey(pendingID)); err != nil {
		t.Fatalf("pending Core commit cleanup job was removed: %v", err)
	}
}

func TestCleanupRefusesMismatchedJobIdentity(t *testing.T) {
	service, ctx, _, files, data := newTestService(t)
	body := []byte("data")
	upload, err := service.BeginUpload(ctx, uploadInput("cleanup-identity", body))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.PutUploadPart(ctx, upload.ID, 1, int64(len(body)), digest(body), bytes.NewReader(body)); err != nil {
		t.Fatal(err)
	}
	if err := service.AbortUpload(ctx, upload.ID); err != nil {
		t.Fatal(err)
	}
	job, record, err := service.getCleanup(ctx, upload.ID)
	if err != nil {
		t.Fatal(err)
	}
	job.PrincipalID = uuid.NewString()
	value, err := dataValue(job, uploadIndex("cleanup", upload.ID, job.State, job.PrincipalID), 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := data.Put(ctx, cleanupDataKey(upload.ID), value, record.Revision); err != nil {
		t.Fatal(err)
	}
	if err := service.CleanupOnce(ctx); !errors.Is(err, api.ErrUnavailable) {
		t.Fatalf("cleanup accepted a job detached from its session principal: %v", err)
	}
	if len(files.staged) != 1 {
		t.Fatalf("cleanup deleted staged bytes after detecting inconsistent job data: %d remain", len(files.staged))
	}
}

func TestCleanupToleratesJobRemovedByAnotherInstance(t *testing.T) {
	service, ctx, _, _, data := newTestService(t)
	upload, err := service.BeginUpload(ctx, uploadInput("cleanup-peer", nil))
	if err != nil {
		t.Fatal(err)
	}
	if err := service.AbortUpload(ctx, upload.ID); err != nil {
		t.Fatal(err)
	}
	jobKey := cleanupDataKey(upload.ID)
	data.beforeDelete = func(key contracts.RecordKey) {
		if key == jobKey {
			delete(data.records, key)
		}
	}
	if err := service.CleanupOnce(ctx); err != nil {
		t.Fatalf("peer cleanup deletion should be idempotent: %v", err)
	}
}

func markCommittingExpired(t *testing.T, service *Service, data *testData, uploadID string, expiresAt time.Time) {
	t.Helper()
	key := sessionDataKey(uploadID)
	record := data.records[key]
	var upload storedUpload
	if err := json.Unmarshal(record.Payload, &upload); err != nil {
		t.Fatal(err)
	}
	upload.State = "committing"
	upload.ExpiresAt = expiresAt
	payload, err := json.Marshal(upload)
	if err != nil {
		t.Fatal(err)
	}
	index, err := json.Marshal(sessionIndex(upload))
	if err != nil {
		t.Fatal(err)
	}
	record.Payload, record.IndexData = payload, index
	record.ExpiresAt = &expiresAt
	data.records[key] = record
	cleanupKey := cleanupDataKey(uploadID)
	job, jobRecord, err := service.getCleanup(context.Background(), uploadID)
	if err != nil {
		t.Fatal(err)
	}
	job.CommitAttempt, job.State = true, "pending"
	jobValue, err := dataValue(job, uploadIndex("cleanup", uploadID, job.State, upload.PrincipalID), 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := data.Put(context.Background(), cleanupKey, jobValue, jobRecord.Revision); err != nil {
		t.Fatal(err)
	}
}

func TestExpectedPartCountDoesNotOverflow(t *testing.T) {
	if got := expectedPartCount(int64(^uint64(0)>>1), 1); got != int(^uint(0)>>1) {
		t.Fatalf("extreme upload size should saturate at max int, got %d", got)
	}
}

func uploadInput(key string, body []byte) api.BeginUploadInput {
	return api.BeginUploadInput{IdempotencyKey: key + "-1234567890", Name: "asset.bin", ContentType: "application/octet-stream", Size: int64(len(body)), SHA256: digest(body), Groups: []string{"assets"}}
}

func digest(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func TestFileSetNameConflictMapsToClientConflict(t *testing.T) {
	if got := mapCoreError(contracts.ErrFileSetConflict); !errors.Is(got, api.ErrConflict) {
		t.Fatalf("file-set name conflict mapped to %v", got)
	}
}

func TestInvalidCoreSearchInputMapsToClientError(t *testing.T) {
	if got := mapCoreError(contracts.ErrFileInvalidSearch); !errors.Is(got, api.ErrInvalid) {
		t.Fatalf("invalid Core search input mapped to %v", got)
	}
}
