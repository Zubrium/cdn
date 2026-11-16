package platform

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Zubrium/cdn/core/access"
	"github.com/Zubrium/cdn/core/internal/postgres"
	"github.com/Zubrium/cdn/core/internal/testsupport/s3mock"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// coreV2RuntimeContract is the frozen public Go contract for the Core v2
// acceptance suite. It is test-local so the tests compile while production
// code is being replaced; Runtime must satisfy it before acceptance runs.
type coreV2RuntimeContract interface {
	Authenticate(context.Context, string) (Session, error)
	CreatePrincipal(context.Context, Session, string, *time.Time) (string, string, error)
	ListPrincipals(context.Context, Session) ([]Principal, error)
	SetPrincipalManagement(context.Context, Session, string, bool) error
	DeactivatePrincipal(context.Context, Session, string) error
	IssueCredential(context.Context, Session, string, *time.Time) (string, string, error)
	ListCredentials(context.Context, Session, string) ([]Credential, error)
	RevokeCredential(context.Context, Session, string) error
	SetGroupAccess(context.Context, Session, string, string, access.Level) error
	GroupAccess(context.Context, Session, string, string) (access.Level, error)
	PutStagedObject(context.Context, Session, string, []string, int64, string, io.Reader) error
	ReadStagedObject(context.Context, Session, string) (io.ReadCloser, int64, string, error)
	DeleteStagedObject(context.Context, Session, string) error
	CreateFile(context.Context, Session, string, []string, map[string]string, string, int64, io.Reader) (string, error)
	ReadFile(context.Context, Session, string) (io.ReadCloser, File, error)
	ReadFileRange(context.Context, Session, string, int64, int64) (io.ReadCloser, File, error)
	StatFile(context.Context, Session, string) (File, error)
	ListFiles(context.Context, Session, string, string, int) ([]File, string, error)
	UpdateFile(context.Context, Session, string, int64, map[string]string) error
	AddFileToGroup(context.Context, Session, string, string, int64) error
	RemoveFileFromGroup(context.Context, Session, string, string, int64) error
	DeleteFile(context.Context, Session, string, int64) error
	CreateFileSet(context.Context, Session, string, string) (string, error)
	AddFileToSet(context.Context, Session, string, string) error
	RemoveFileFromSet(context.Context, Session, string, string) error
	DeleteFileSet(context.Context, Session, string) error
	ListFileSet(context.Context, Session, string, string, int) ([]File, string, error)
	SearchFiles(context.Context, Session, string, string, string, string, int) ([]File, string, error)
}

func TestRuntimeImplementsFrozenCoreV2Contract(t *testing.T) {
	want := reflect.TypeOf((*coreV2RuntimeContract)(nil)).Elem()
	got := reflect.TypeOf((*Runtime)(nil))
	var missing []string
	for i := 0; i < want.NumMethod(); i++ {
		method := want.Method(i)
		implementation, ok := got.MethodByName(method.Name)
		if !ok || !sameMethodSignature(implementation.Type, method.Type) {
			missing = append(missing, method.Name)
		}
	}
	if len(missing) != 0 {
		sort.Strings(missing)
		t.Fatalf("Runtime does not implement frozen Core v2 contract methods: %s", strings.Join(missing, ", "))
	}
}

func TestRuntimeConfigHasExplicitV2StorageAndCacheSettings(t *testing.T) {
	typ := reflect.TypeOf(RuntimeConfig{})
	for name, want := range map[string]reflect.Type{
		"AccessGroups":         reflect.TypeOf(map[string]access.Level{}),
		"CacheMaxBytes":        reflect.TypeOf(int64(0)),
		"CacheTTL":             reflect.TypeOf(time.Duration(0)),
		"MaxMetadataBytes":     reflect.TypeOf(int64(0)),
		"MaxObjectBytes":       reflect.TypeOf(int64(0)),
		"MaxCachedObjectBytes": reflect.TypeOf(int64(0)),
		"PendingTTL":           reflect.TypeOf(time.Duration(0)),
		"MaintenanceInterval":  reflect.TypeOf(time.Duration(0)),
		"MaintenanceTimeout":   reflect.TypeOf(time.Duration(0)),
		"MaintenanceBatchSize": reflect.TypeOf(int(0)),
	} {
		field, ok := typ.FieldByName(name)
		if !ok || field.Type != want {
			t.Errorf("RuntimeConfig.%s must have type %v", name, want)
		}
	}
	if _, ok := typ.FieldByName("DeletedRetention"); ok {
		t.Error("permanent deletion must not keep a deleted-file retention setting")
	}
}

func sameMethodSignature(implementation, contract reflect.Type) bool {
	if implementation.NumIn() != contract.NumIn()+1 || implementation.NumOut() != contract.NumOut() || implementation.IsVariadic() != contract.IsVariadic() {
		return false
	}
	if implementation.In(0) != reflect.TypeOf((*Runtime)(nil)) {
		return false
	}
	for i := 0; i < contract.NumIn(); i++ {
		if implementation.In(i+1) != contract.In(i) {
			return false
		}
	}
	for i := 0; i < contract.NumOut(); i++ {
		if implementation.Out(i) != contract.Out(i) {
			return false
		}
	}
	return true
}

func TestDisposableCoreV2FileGroupSetAndCacheAcceptance(t *testing.T) {
	dsn := os.Getenv("TEST_CORE_PLATFORM_DATABASE_URL")
	migrationDSN := os.Getenv("TEST_CORE_PLATFORM_MIGRATION_DATABASE_URL")
	endpoint := os.Getenv("TEST_CORE_PLATFORM_S3_ENDPOINT")
	bucket := os.Getenv("TEST_CORE_PLATFORM_S3_BUCKET")
	accessKey := os.Getenv("TEST_CORE_PLATFORM_S3_ACCESS_KEY")
	secretKey := os.Getenv("TEST_CORE_PLATFORM_S3_SECRET_KEY")
	if dsn == "" || migrationDSN == "" {
		t.Skip("disposable Core PostgreSQL migration/runtime users are not configured")
	}
	if endpoint == "" && bucket == "" && accessKey == "" && secretKey == "" {
		server := s3mock.New()
		t.Cleanup(server.Close)
		endpoint, bucket, accessKey, secretKey = server.URL(), "zubrium-core-qa-platform-unit", "test-access", "test-secret"
	} else if endpoint == "" || bucket == "" || accessKey == "" || secretKey == "" {
		t.Fatal("configure all TEST_CORE_PLATFORM_S3_* values or leave them all unset to use the isolated S3 test server")
	}
	assertIsolatedCoreS3Endpoint(t, endpoint)
	parsed, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(parsed.ConnConfig.Database, "zubrium_core_qa_") || !strings.HasPrefix(bucket, "zubrium-core-qa-") {
		t.Fatal("refusing non-disposable database or bucket")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	probe, err := pgxpool.NewWithConfig(ctx, parsed)
	if err != nil {
		t.Fatal(err)
	}
	var sessions int
	err = probe.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid()`).Scan(&sessions)
	if err != nil || sessions != 0 {
		probe.Close()
		t.Fatalf("disposable database has other sessions: %d: %v", sessions, err)
	}
	var initialized bool
	if err := probe.QueryRow(ctx, `SELECT to_regclass('public.atlas_schema_revisions') IS NOT NULL OR to_regclass('core.atlas_schema_revisions') IS NOT NULL OR to_regclass('core.core_principals') IS NOT NULL OR EXISTS(SELECT 1 FROM pg_catalog.pg_namespace WHERE nspname='core')`).Scan(&initialized); err != nil {
		probe.Close()
		t.Fatal(err)
	}
	probe.Close()
	if initialized {
		t.Fatal("refusing a database that already contains a Core schema; provide a fresh disposable database")
	}
	cfg := newCoreV2TestConfig(t, dsn, migrationDSN, endpoint, bucket, accessKey, secretKey)
	cfg.Database.URL = withApplicationName(t, cfg.Database.URL, "core-v2-primary")
	first, managerSecret, err := Start(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	core, ok := any(first).(coreV2RuntimeContract)
	if !ok {
		t.Fatal("Runtime does not implement the frozen Core v2 acceptance contract")
	}
	manager, err := core.Authenticate(ctx, managerSecret)
	if err != nil {
		t.Fatal(err)
	}
	managerActor, err := postgres.Authenticate(ctx, first.database, managerSecret)
	if err != nil {
		t.Fatal(err)
	}
	principals, err := core.ListPrincipals(ctx, manager)
	if err != nil {
		t.Fatal(err)
	}
	bootstrapID := ""
	for _, principal := range principals {
		if principalCanManage(t, principal) {
			if bootstrapID != "" {
				t.Fatal("fresh bootstrap created more than one management account")
			}
			bootstrapID = principal.ID
		}
	}
	if bootstrapID == "" {
		t.Fatal("fresh bootstrap account lacks explicit administrator-management permission")
	}
	auditorID, auditorCredential, err := core.CreatePrincipal(ctx, manager, "permission auditor", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := core.SetGroupAccess(ctx, manager, auditorID, "content", access.LevelWrite); err != nil {
		t.Fatal(err)
	}
	if err := core.SetPrincipalManagement(ctx, manager, auditorID, true); err != nil {
		t.Fatal(err)
	}
	auditor, err := core.Authenticate(ctx, auditorCredential)
	if err != nil {
		t.Fatal(err)
	}
	if err := core.SetGroupAccess(ctx, manager, bootstrapID, "content", access.LevelRead); err != nil {
		t.Fatal(err)
	}
	secondConfig := cfg
	secondConfig.Database.URL = withApplicationName(t, cfg.Database.URL, "core-v2-secondary")
	second, _, err := Start(ctx, secondConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	core2, ok := any(second).(coreV2RuntimeContract)
	if !ok {
		t.Fatal("second Runtime does not implement the frozen Core v2 acceptance contract")
	}
	testCacheListenerReconnect(t, ctx, first, cfg, os.Getenv("TEST_CORE_PLATFORM_ADMIN_DATABASE_URL"))
	if level, err := core2.GroupAccess(ctx, auditor, bootstrapID, "content"); err != nil || level != access.LevelRead {
		t.Fatalf("restart overwrote an admin-managed group assignment: level=%d err=%v", level, err)
	}
	if err := core2.SetGroupAccess(ctx, auditor, bootstrapID, "content", access.LevelWrite); err != nil {
		t.Fatal(err)
	}
	stageBytes := []byte("opaque loader staging bytes")
	stageDigest := sha256.Sum256(stageBytes)
	stageID := uuid.NewString()
	if err := core.PutStagedObject(ctx, manager, stageID, []string{"content"}, int64(len(stageBytes)), hex.EncodeToString(stageDigest[:]), bytes.NewReader(stageBytes)); err != nil {
		t.Fatalf("write Core staging object: %v", err)
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if err := core.DeleteStagedObject(cleanup, manager, stageID); err != nil {
			t.Errorf("delete disposable staging object: %v", err)
		}
	})
	stagedReader, stagedSize, stagedChecksum, err := core.ReadStagedObject(ctx, manager, stageID)
	if err != nil {
		t.Fatalf("read Core staging object: %v", err)
	}
	stagedBytes, readErr := io.ReadAll(stagedReader)
	closeErr := stagedReader.Close()
	if readErr != nil || closeErr != nil || stagedSize != int64(len(stageBytes)) || stagedChecksum != hex.EncodeToString(stageDigest[:]) || !bytes.Equal(stagedBytes, stageBytes) {
		t.Fatalf("staging roundtrip: size=%d checksum=%q body=%q read=%v close=%v", stagedSize, stagedChecksum, stagedBytes, readErr, closeErr)
	}
	if err := first.SetGroupAccess(ctx, manager, bootstrapID, "content", access.LevelRead); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := core.ReadStagedObject(ctx, manager, stageID); !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("staged bytes remained readable after write access was revoked: %v", err)
	}
	if err := first.SetGroupAccess(ctx, auditor, bootstrapID, "content", access.LevelWrite); err != nil {
		t.Fatal(err)
	}
	if _, err := first.database.Exec(ctx, `UPDATE core_staged_objects SET updated_at=now()-interval '24 hours' WHERE id=$1`, stageID); err != nil {
		t.Fatal(err)
	}
	if err := postgres.RecoverPending(ctx, first.database, first.objects, time.Now().Add(-time.Hour), 100); err != nil {
		t.Fatalf("recover old pending Core objects: %v", err)
	}
	stagedReader, _, _, err = core.ReadStagedObject(ctx, manager, stageID)
	if err != nil {
		t.Fatalf("Core TTL cleanup deleted a ready Loader staging object: %v", err)
	}
	if _, err := io.Copy(io.Discard, stagedReader); err != nil {
		t.Fatalf("read staging object after recovery: %v", err)
	}
	if err := stagedReader.Close(); err != nil {
		t.Fatalf("close staging object after recovery: %v", err)
	}
	concurrentStageID := uuid.NewString()
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if err := core.DeleteStagedObject(cleanup, manager, concurrentStageID); err != nil {
			t.Errorf("delete disposable concurrent staging object: %v", err)
		}
	})
	stageStarted, releaseStage := make(chan struct{}), make(chan struct{})
	var releaseStageOnce sync.Once
	releaseStagePut := func() { releaseStageOnce.Do(func() { close(releaseStage) }) }
	defer releaseStagePut()
	stageWriter := &controlledObjectWriter{ObjectWriter: first.objects, putStarted: stageStarted, releasePut: releaseStage}
	stagePutResult := make(chan error, 1)
	go func() {
		stagePutResult <- postgres.PutStagedObject(ctx, first.database, stageWriter, managerActor, concurrentStageID,
			[]string{"content"}, first.maxObjectBytes, int64(len(stageBytes)), hex.EncodeToString(stageDigest[:]), bytes.NewReader(stageBytes))
	}()
	select {
	case <-stageStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("staged upload did not reach object storage")
	}
	if _, err := first.database.Exec(ctx, `UPDATE core_staged_objects SET updated_at=now()-interval '2 hours' WHERE id=$1`, concurrentStageID); err != nil {
		t.Fatal(err)
	}
	if err := postgres.RecoverPending(ctx, first.database, first.objects, time.Now().Add(-time.Hour), 100); err != nil {
		t.Fatalf("run cleanup while staging upload is active: %v", err)
	}
	var concurrentStageState string
	if err := first.database.QueryRow(ctx, `SELECT state FROM core_staged_objects WHERE id=$1`, concurrentStageID).Scan(&concurrentStageState); err != nil || concurrentStageState != "pending" {
		t.Fatalf("cleanup changed active staging reservation: state=%q err=%v", concurrentStageState, err)
	}
	releaseStagePut()
	select {
	case err := <-stagePutResult:
		if err != nil {
			t.Fatalf("finish active staging upload after cleanup pass: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("staged upload remained blocked after cleanup pass")
	}
	activeStageReader, activeStageSize, activeStageChecksum, err := core.ReadStagedObject(ctx, manager, concurrentStageID)
	if err != nil {
		t.Fatalf("active staging upload was deleted by cleanup: %v", err)
	}
	activeStageBytes, err := io.ReadAll(activeStageReader)
	closeErr = activeStageReader.Close()
	if err != nil || closeErr != nil || activeStageSize != int64(len(stageBytes)) || activeStageChecksum != hex.EncodeToString(stageDigest[:]) || !bytes.Equal(activeStageBytes, stageBytes) {
		t.Fatalf("active staging upload roundtrip: size=%d checksum=%q body=%q read=%v close=%v", activeStageSize, activeStageChecksum, activeStageBytes, err, closeErr)
	}
	if err := core.DeleteStagedObject(ctx, manager, concurrentStageID); err != nil {
		t.Fatalf("delete active staging test object: %v", err)
	}
	failedStageID := uuid.NewString()
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if err := core.DeleteStagedObject(cleanup, manager, failedStageID); err != nil {
			t.Errorf("delete disposable failed staging object: %v", err)
		}
	})
	if err := core.PutStagedObject(ctx, manager, failedStageID, []string{"content"}, int64(len(stageBytes)), strings.Repeat("0", 64), bytes.NewReader(stageBytes)); err == nil {
		t.Fatal("staging accepted a mismatched checksum")
	}
	fileIDs := make([]string, 0, 8)
	setIDs := make([]string, 0, 1)
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 20*time.Second)
		defer stop()
		for _, setID := range setIDs {
			_ = core.DeleteFileSet(cleanup, manager, setID)
		}
		for _, fileID := range fileIDs {
			file, err := core.StatFile(cleanup, manager, fileID)
			if errors.Is(err, ErrFileNotFound) {
				continue
			}
			if err != nil {
				t.Errorf("inspect disposable file %s during cleanup: %v", fileID, err)
				continue
			}
			if err := core.DeleteFile(cleanup, manager, fileID, file.Revision); err != nil {
				t.Errorf("delete disposable file %s: %v", fileID, err)
			}
		}
	})

	activeUploadKey := newRequestKey(t)
	activeUploadBody := []byte("protected active upload")
	uploadStarted, releaseUpload := make(chan struct{}), make(chan struct{})
	var releaseUploadOnce sync.Once
	releaseUploadPut := func() { releaseUploadOnce.Do(func() { close(releaseUpload) }) }
	defer releaseUploadPut()
	activeUploadWriter := &controlledObjectWriter{ObjectWriter: first.objects, putStarted: uploadStarted, releasePut: releaseUpload}
	activeUploadCreator := postgres.Creator{Pool: first.database, Store: activeUploadWriter, MaxMetadataBytes: cfg.MaxMetadataBytes}
	activeUploadResult := make(chan struct {
		id  string
		err error
	}, 1)
	go func() {
		id, err := activeUploadCreator.Create(ctx, managerActor, activeUploadKey, []string{"assets"},
			map[string]string{"name": "active-upload.bin"}, "application/octet-stream", int64(len(activeUploadBody)), bytes.NewReader(activeUploadBody))
		activeUploadResult <- struct {
			id  string
			err error
		}{id: id, err: err}
	}()
	select {
	case <-uploadStarted:
	case result := <-activeUploadResult:
		t.Fatalf("file upload failed before object storage: %v", result.err)
	case <-time.After(5 * time.Second):
		t.Fatal("file upload did not reach object storage")
	}
	var activeUploadID uuid.UUID
	if err := first.database.QueryRow(ctx, `SELECT object_id FROM core_upload_requests WHERE principal_id=$1 AND request_key=$2`, managerActor.ID, activeUploadKey).Scan(&activeUploadID); err != nil {
		t.Fatal(err)
	}
	if _, err := first.database.Exec(ctx, `UPDATE core_objects SET updated_at=now()-interval '2 hours' WHERE id=$1`, activeUploadID); err != nil {
		t.Fatal(err)
	}
	if err := postgres.RecoverPending(ctx, first.database, first.objects, time.Now().Add(-time.Hour), 100); err != nil {
		t.Fatalf("run cleanup while file upload is active: %v", err)
	}
	var activeUploadState string
	if err := first.database.QueryRow(ctx, `SELECT state FROM core_objects WHERE id=$1`, activeUploadID).Scan(&activeUploadState); err != nil || activeUploadState != "pending" {
		t.Fatalf("cleanup changed active file reservation: state=%q err=%v", activeUploadState, err)
	}
	releaseUploadPut()
	select {
	case result := <-activeUploadResult:
		if result.err != nil || result.id != activeUploadID.String() {
			t.Fatalf("active file upload did not complete after cleanup pass: id=%s want=%s err=%v", result.id, activeUploadID, result.err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("file upload remained blocked after cleanup pass")
	}
	fileIDs = append(fileIDs, activeUploadID.String())
	if _, err := core.StatFile(ctx, manager, activeUploadID.String()); err != nil {
		t.Fatalf("active file upload was deleted by cleanup: %v", err)
	}

	// A failed S3 write must never publish a file. The failed reservation stays
	// retryable with the same idempotency key and is removed by bounded recovery.
	retryKey := newRequestKey(t)
	retryMetadata := map[string]string{"name": "retry-after-storage-error.txt"}
	failedWriter := &controlledObjectWriter{ObjectWriter: first.objects, putFailure: errors.New("simulated S3 outage")}
	failedCreator := postgres.Creator{Pool: first.database, Store: failedWriter, MaxMetadataBytes: cfg.MaxMetadataBytes}
	if _, err := failedCreator.Create(ctx, managerActor, retryKey, []string{"assets"}, retryMetadata, "text/plain", 5, strings.NewReader("retry")); err == nil {
		t.Fatal("Core reported success after the object store rejected a write")
	}
	var failedState string
	if err := first.database.QueryRow(ctx, `SELECT o.state FROM core_upload_requests r JOIN core_objects o ON o.id=r.object_id
		WHERE r.principal_id=$1 AND r.request_key=$2`, managerActor.ID, retryKey).Scan(&failedState); err != nil || failedState != "failed" {
		t.Fatalf("failed upload reservation state=%q err=%v", failedState, err)
	}
	retriedID, err := (postgres.Creator{Pool: first.database, Store: first.objects, MaxMetadataBytes: cfg.MaxMetadataBytes}).Create(ctx, managerActor, retryKey, []string{"assets"}, retryMetadata, "text/plain", 5, strings.NewReader("retry"))
	if err != nil {
		t.Fatalf("retry failed upload with the same idempotency key: %v", err)
	}
	fileIDs = append(fileIDs, retriedID)
	if _, err := core.StatFile(ctx, manager, retriedID); err != nil {
		t.Fatalf("successful upload retry was not visible: %v", err)
	}
	if _, err := first.database.Exec(ctx, `UPDATE core_objects SET updated_at=now()-interval '2 hours' WHERE state='failed'`); err != nil {
		t.Fatal(err)
	}
	if err := postgres.RecoverPending(ctx, first.database, first.objects, time.Now().Add(-time.Hour), 100); err != nil {
		t.Fatalf("clean up failed upload reservation: %v", err)
	}

	// A failed S3 delete leaves an inaccessible, retryable deleting state. A
	// later cleanup safely repeats deletion and removes its metadata.
	retriedFile, err := core.StatFile(ctx, manager, retriedID)
	if err != nil {
		t.Fatal(err)
	}
	deleteFailureWriter := &controlledObjectWriter{ObjectWriter: first.objects, deleteFailure: errors.New("simulated S3 delete outage")}
	deleteFailureCreator := postgres.Creator{Pool: first.database, Store: deleteFailureWriter, MaxMetadataBytes: cfg.MaxMetadataBytes}
	if err := deleteFailureCreator.Delete(ctx, managerActor, retriedID, retriedFile.Revision); err == nil {
		t.Fatal("Core reported success after the object store rejected deletion")
	}
	if _, err := core.StatFile(ctx, manager, retriedID); !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("file stayed visible after delete entered recovery: %v", err)
	}
	if _, err := first.database.Exec(ctx, `UPDATE core_objects SET updated_at=now()-interval '2 hours' WHERE id=$1`, retriedID); err != nil {
		t.Fatal(err)
	}
	if err := postgres.RecoverPending(ctx, first.database, first.objects, time.Now().Add(-time.Hour), 100); err != nil {
		t.Fatalf("retry failed S3 deletion: %v", err)
	}
	var objectRemains bool
	if err := first.database.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM core_objects WHERE id=$1)`, retriedID).Scan(&objectRemains); err != nil || objectRemains {
		t.Fatalf("recovery retained a deleted file row: exists=%v err=%v", objectRemains, err)
	}

	// If S3 deletion succeeds but PostgreSQL finalization is canceled, recovery
	// must tolerate the already-absent S3 key and finish the database cleanup.
	databaseFailureID, err := core.CreateFile(ctx, manager, newRequestKey(t), []string{"assets"}, map[string]string{"name": "finalize-after-db-error.txt"}, "text/plain", 4, strings.NewReader("done"))
	if err != nil {
		t.Fatal(err)
	}
	fileIDs = append(fileIDs, databaseFailureID)
	databaseFailureFile, err := core.StatFile(ctx, manager, databaseFailureID)
	if err != nil {
		t.Fatal(err)
	}
	deleteCtx, cancelDelete := context.WithCancel(ctx)
	databaseFailureWriter := &controlledObjectWriter{ObjectWriter: first.objects, afterSuccessfulDelete: cancelDelete}
	databaseFailureCreator := postgres.Creator{Pool: first.database, Store: databaseFailureWriter, MaxMetadataBytes: cfg.MaxMetadataBytes}
	deleteErr := databaseFailureCreator.Delete(deleteCtx, managerActor, databaseFailureID, databaseFailureFile.Revision)
	cancelDelete()
	if deleteErr == nil {
		t.Fatal("Core reported a committed delete after PostgreSQL finalization was canceled")
	}
	if _, err := core.StatFile(ctx, manager, databaseFailureID); !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("file became visible after database finalization failed: %v", err)
	}
	if _, err := first.database.Exec(ctx, `UPDATE core_objects SET updated_at=now()-interval '2 hours' WHERE id=$1`, databaseFailureID); err != nil {
		t.Fatal(err)
	}
	if err := postgres.RecoverPending(ctx, first.database, first.objects, time.Now().Add(-time.Hour), 100); err != nil {
		t.Fatalf("recover failed PostgreSQL finalization after S3 deletion: %v", err)
	}
	if err := first.database.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM core_objects WHERE id=$1)`, databaseFailureID).Scan(&objectRemains); err != nil || objectRemains {
		t.Fatalf("post-failure recovery retained a deleted file row: exists=%v err=%v", objectRemains, err)
	}

	viewerID, viewerKey, err := core.CreatePrincipal(ctx, manager, "asset viewer", nil)
	if err != nil {
		t.Fatal(err)
	}
	viewer, err := core.Authenticate(ctx, viewerKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := core.ReadStagedObject(ctx, viewer, stageID); !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("another principal read a staged object: %v", err)
	}
	if err := core.DeleteStagedObject(ctx, viewer, stageID); !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("another principal deleted a staged object: %v", err)
	}
	principals, err = core.ListPrincipals(ctx, manager)
	if err != nil {
		t.Fatal(err)
	}
	foundViewer := false
	for _, principal := range principals {
		if principal.ID == viewerID {
			foundViewer = principal.Active && !principalCanManage(t, principal)
		}
	}
	if !foundViewer {
		t.Fatal("new single-type user was not listed as active without administrator-management permission")
	}
	if level, err := core.GroupAccess(ctx, manager, viewerID, "assets"); err != nil || level != access.LevelNone {
		t.Fatalf("new user default group access: level=%d err=%v", level, err)
	}
	if err := core.SetGroupAccess(ctx, manager, viewerID, "assets", access.LevelRead); err != nil {
		t.Fatal(err)
	}
	sharedRequest := newRequestKey(t)
	sharedMetadata := map[string]string{"name": "shared.txt", "caption": "original"}
	managerFile, err := core.CreateFile(ctx, manager, sharedRequest, []string{"assets", "features"}, sharedMetadata, "text/plain", 3, strings.NewReader("one"))
	if err != nil {
		t.Fatal(err)
	}
	fileIDs = append(fileIDs, managerFile)
	replayedID, err := core.CreateFile(ctx, manager, sharedRequest, []string{"assets", "features"}, sharedMetadata, "text/plain", 3, strings.NewReader("one"))
	if err != nil || replayedID != managerFile {
		t.Fatalf("idempotent retry: id=%s want=%s err=%v", replayedID, managerFile, err)
	}
	if _, err := core.CreateFile(ctx, manager, sharedRequest, []string{"assets"}, map[string]string{"name": "different.txt"}, "text/plain", 3, strings.NewReader("one")); !errors.Is(err, ErrRequestConflict) {
		t.Fatalf("idempotency key reused for a different request: %v", err)
	}
	if _, err := core.CreateFile(ctx, manager, newRequestKey(t), []string{"assets"}, map[string]string{"name": "short.txt"}, "text/plain", 4, strings.NewReader("one")); err == nil {
		t.Fatal("short upload was accepted")
	}
	if _, err := core.CreateFile(ctx, manager, newRequestKey(t), []string{"assets"}, map[string]string{"name": "oversized-metadata", "notes": strings.Repeat("x", 1<<20)}, "text/plain", 0, strings.NewReader("")); err == nil {
		t.Fatal("metadata beyond the configured limit was accepted")
	}
	listed, _, err := core.ListFiles(ctx, manager, "assets", "", 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range listed {
		if fileMetadataValue(t, item, "name") == "short.txt" || fileMetadataValue(t, item, "name") == "oversized-metadata" {
			t.Fatalf("failed upload became visible: %+v", item)
		}
	}
	if _, _, err := core.ReadFile(ctx, Session{}, managerFile); !errors.Is(err, access.ErrUnauthorized) {
		t.Fatalf("anonymous Core read: %v", err)
	}
	body, file, err := core2.ReadFile(ctx, viewer, managerFile)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(body)
	_ = body.Close()
	if err != nil || string(data) != "one" || fileMetadataValue(t, file, "name") != "shared.txt" || fileMetadataValue(t, file, "caption") != "original" {
		t.Fatalf("read through an assigned group: bytes=%q file=%+v err=%v", data, file, err)
	}
	largePayload := strings.Repeat("L", 16)
	largeFileID, err := core.CreateFile(ctx, manager, newRequestKey(t), []string{"assets"}, map[string]string{"name": "streamed.bin"}, "application/octet-stream", int64(len(largePayload)), strings.NewReader(largePayload))
	if err != nil {
		t.Fatal(err)
	}
	fileIDs = append(fileIDs, largeFileID)
	largeBody, _, err := core2.ReadFile(ctx, manager, largeFileID)
	if err != nil {
		t.Fatal(err)
	}
	largeBytes, readErr := io.ReadAll(largeBody)
	_ = largeBody.Close()
	if readErr != nil || string(largeBytes) != largePayload {
		t.Fatalf("streaming object larger than the cache threshold: bytes=%q err=%v", largeBytes, readErr)
	}
	if err := core.UpdateFile(ctx, viewer, managerFile, file.Revision, map[string]string{"name": "forbidden.txt"}); !errors.Is(err, access.ErrForbidden) {
		t.Fatalf("read permission allowed metadata write: %v", err)
	}

	writerID, writerKey, err := core.CreatePrincipal(ctx, manager, "asset editor", nil)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := core.Authenticate(ctx, writerKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := core.SetGroupAccess(ctx, manager, writerID, "assets", access.LevelWrite); err != nil {
		t.Fatal(err)
	}
	if err := core.SetPrincipalManagement(ctx, manager, writerID, true); err != nil {
		t.Fatal(err)
	}
	if err := core.SetGroupAccess(ctx, writer, viewerID, "assets", access.LevelWrite); err != nil {
		t.Fatalf("delegated manager with level 2 could not assign group access: %v", err)
	}
	if err := core.SetGroupAccess(ctx, writer, viewerID, "restricted", access.LevelRead); !errors.Is(err, access.ErrForbidden) {
		t.Fatalf("management right bypassed missing group level 2: %v", err)
	}
	if err := core.SetGroupAccess(ctx, manager, viewerID, "assets", access.LevelRead); err != nil {
		t.Fatal(err)
	}
	if err := core.SetPrincipalManagement(ctx, manager, writerID, false); err != nil {
		t.Fatal(err)
	}
	if err := core.SetGroupAccess(ctx, writer, viewerID, "assets", access.LevelWrite); !errors.Is(err, access.ErrForbidden) {
		t.Fatalf("revoked management permission remained effective: %v", err)
	}
	if err := core.AddFileToGroup(ctx, writer, managerFile, "private", file.Revision); !errors.Is(err, access.ErrForbidden) {
		t.Fatalf("group extension without target group access: %v", err)
	}
	privateReaderID, privateReaderKey, err := core.CreatePrincipal(ctx, manager, "private reader", nil)
	if err != nil {
		t.Fatal(err)
	}
	privateReader, err := core.Authenticate(ctx, privateReaderKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := core.SetGroupAccess(ctx, manager, privateReaderID, "private", access.LevelRead); err != nil {
		t.Fatal(err)
	}
	file, err = core.StatFile(ctx, manager, managerFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := core.AddFileToGroup(ctx, manager, managerFile, "private", file.Revision); err != nil {
		t.Fatal(err)
	}
	file, err = core.StatFile(ctx, manager, managerFile)
	if err != nil {
		t.Fatal(err)
	}
	if !hasFileGroup(t, file, "private") || !hasFileGroup(t, file, "assets") {
		t.Fatalf("file did not retain multiple groups: %+v", file)
	}
	if err := core.RemoveFileFromGroup(ctx, manager, managerFile, "assets", file.Revision); err != nil {
		t.Fatal(err)
	}
	file, err = core.StatFile(ctx, manager, managerFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := core.RemoveFileFromGroup(ctx, manager, managerFile, "features", file.Revision); err != nil {
		t.Fatal(err)
	}
	file, err = core.StatFile(ctx, manager, managerFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := core.RemoveFileFromGroup(ctx, manager, managerFile, "private", file.Revision); err == nil {
		t.Fatal("file lost its last access group")
	}
	if _, _, err := core.ReadFile(ctx, privateReader, managerFile); err != nil {
		t.Fatalf("read via its remaining group: %v", err)
	}

	engineID, engineKey, err := core.CreatePrincipal(ctx, manager, "website engine", nil)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := core.Authenticate(ctx, engineKey)
	if err != nil {
		t.Fatal(err)
	}
	for group, level := range map[string]access.Level{"assets": access.LevelRead, "private": access.LevelRead, "restricted": access.LevelRead} {
		if err := core.SetGroupAccess(ctx, manager, engineID, group, level); err != nil {
			t.Fatal(err)
		}
	}
	setID, err := core.CreateFileSet(ctx, engine, "website", "article-assets")
	if err != nil {
		t.Fatal(err)
	}
	setIDs = append(setIDs, setID)
	if err := core.AddFileToSet(ctx, manager, setID, managerFile); !errors.Is(err, ErrForbidden) {
		t.Fatalf("a different principal changed an engine-owned file set: %v", err)
	}
	if err := core.DeleteFileSet(ctx, manager, setID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("a different principal deleted an engine-owned file set: %v", err)
	}
	if err := core.AddFileToSet(ctx, engine, setID, managerFile); err != nil {
		t.Fatal(err)
	}
	hiddenFile, err := core.CreateFile(ctx, manager, newRequestKey(t), []string{"restricted"}, map[string]string{"name": "internal.txt"}, "text/plain", 6, strings.NewReader("secret"))
	if err != nil {
		t.Fatal(err)
	}
	fileIDs = append(fileIDs, hiddenFile)
	if err := core.AddFileToSet(ctx, engine, setID, hiddenFile); err != nil {
		t.Fatal(err)
	}
	if _, _, err := core.ReadFile(ctx, viewer, managerFile); !errors.Is(err, access.ErrForbidden) {
		t.Fatalf("set membership granted file access: %v", err)
	}
	visible, next, err := core.ListFileSet(ctx, viewer, setID, "", 20)
	if err != nil || len(visible) != 0 || next != "" {
		t.Fatalf("inaccessible files leaked through set: count=%d cursor=%q err=%v", len(visible), next, err)
	}
	visible, next, err = core.ListFileSet(ctx, privateReader, setID, "", 20)
	if err != nil || len(visible) != 1 || visible[0].ID != managerFile || next != "" {
		t.Fatalf("permitted set result: count=%d cursor=%q err=%v", len(visible), next, err)
	}
	search, searchNext, err := core.SearchFiles(ctx, privateReader, "", "", "", setID, 20)
	if err != nil || len(search) != 1 || search[0].ID != managerFile || searchNext != "" {
		t.Fatalf("filtered file search: count=%d cursor=%q err=%v", len(search), searchNext, err)
	}
	withoutPrivate := newCoreV2TestConfig(t, dsn, migrationDSN, endpoint, bucket, accessKey, secretKey)
	setRuntimeConfigField(t, &withoutPrivate, "AccessGroups", map[string]access.Level{
		"assets": access.LevelWrite, "features": access.LevelWrite, "restricted": access.LevelWrite, "content": access.LevelWrite,
	})
	third, _, err := Start(ctx, withoutPrivate)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = third.Close() })
	core3, ok := any(third).(coreV2RuntimeContract)
	if !ok {
		t.Fatal("third Runtime does not implement the frozen Core v2 acceptance contract")
	}
	if _, _, err := core2.ReadFile(ctx, privateReader, managerFile); !errors.Is(err, access.ErrForbidden) {
		t.Fatalf("a group removed from startup config still granted read: %v", err)
	}
	visibleWhileRemoved, nextWhileRemoved, listErr := core3.ListFileSet(ctx, privateReader, setID, "", 20)
	if listErr != nil || len(visibleWhileRemoved) != 0 || nextWhileRemoved != "" {
		t.Fatalf("removed group leaked through a set: count=%d cursor=%q err=%v", len(visibleWhileRemoved), nextWhileRemoved, listErr)
	}
	var privateMembershipRetained bool
	if err := first.database.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM core_file_groups WHERE object_id=$1 AND group_key='private')`, managerFile).Scan(&privateMembershipRetained); err != nil || !privateMembershipRetained {
		t.Fatalf("removing a configured group deleted file membership: retained=%t err=%v", privateMembershipRetained, err)
	}
	fourth, _, err := Start(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fourth.Close() })
	core4, ok := any(fourth).(coreV2RuntimeContract)
	if !ok {
		t.Fatal("fourth Runtime does not implement the frozen Core v2 acceptance contract")
	}
	if _, _, err := core4.ReadFile(ctx, privateReader, managerFile); err != nil {
		t.Fatalf("restoring a configured group did not restore its saved assignment: %v", err)
	}
	if _, _, err := core2.ReadFile(ctx, privateReader, managerFile); err != nil {
		t.Fatalf("existing instance did not observe restored group access: %v", err)
	}
	body, _, err = core.ReadFileRange(ctx, privateReader, managerFile, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	rangeBytes, readErr := io.ReadAll(body)
	_ = body.Close()
	if readErr != nil || string(rangeBytes) != "ne" {
		t.Fatalf("byte range: %q err=%v", rangeBytes, readErr)
	}
	if err := core.DeleteFileSet(ctx, engine, setID); err != nil {
		t.Fatal(err)
	}
	setIDs = setIDs[:len(setIDs)-1]
	if _, err := core.StatFile(ctx, manager, managerFile); err != nil {
		t.Fatalf("deleting a set deleted its file: %v", err)
	}
	deleteUserID, deleteUserKey, err := core.CreatePrincipal(ctx, manager, "asset deleter", nil)
	if err != nil {
		t.Fatal(err)
	}
	deleteUser, err := core.Authenticate(ctx, deleteUserKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := core.SetGroupAccess(ctx, manager, deleteUserID, "assets", access.LevelWrite); err != nil {
		t.Fatal(err)
	}
	deleteCandidate, err := core.CreateFile(ctx, manager, newRequestKey(t), []string{"assets", "restricted"}, map[string]string{"name": "delete-through-one-group.txt"}, "text/plain", 1, strings.NewReader("x"))
	if err != nil {
		t.Fatal(err)
	}
	fileIDs = append(fileIDs, deleteCandidate)
	if _, _, err := core.ReadFile(ctx, deleteUser, deleteCandidate); err != nil {
		t.Fatalf("level 2 in one file group did not grant read: %v", err)
	}
	deleteCandidateMetadata, err := core.StatFile(ctx, deleteUser, deleteCandidate)
	if err != nil {
		t.Fatal(err)
	}
	if err := core.DeleteFile(ctx, deleteUser, deleteCandidate, deleteCandidateMetadata.Revision); err != nil {
		t.Fatalf("level 2 in one file group did not permit permanent deletion: %v", err)
	}
	if _, err := core.StatFile(ctx, manager, deleteCandidate); !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("deleted file remained addressable: %v", err)
	}

	file, err = core.StatFile(ctx, manager, managerFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := core.UpdateFile(ctx, manager, managerFile, file.Revision, map[string]string{"name": "renamed.txt", "caption": "updated"}); err != nil {
		t.Fatal(err)
	}
	if err := core.UpdateFile(ctx, manager, managerFile, file.Revision, map[string]string{"name": "stale.txt"}); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale metadata update: %v", err)
	}
	body, renamed, err := core.ReadFile(ctx, manager, managerFile)
	if err != nil {
		t.Fatal(err)
	}
	data, err = io.ReadAll(body)
	_ = body.Close()
	if err != nil || string(data) != "one" || fileMetadataValue(t, renamed, "name") != "renamed.txt" || fileMetadataValue(t, renamed, "caption") != "updated" {
		t.Fatalf("metadata update changed immutable bytes: bytes=%q file=%+v err=%v", data, renamed, err)
	}
	newID, err := core.CreateFile(ctx, manager, newRequestKey(t), []string{"assets"}, map[string]string{"name": "renamed.txt"}, "text/plain", 3, strings.NewReader("two"))
	if err != nil {
		t.Fatal(err)
	}
	fileIDs = append(fileIDs, newID)
	if newID == managerFile {
		t.Fatal("new bytes reused the old file UUID")
	}

	reader2, err := core2.Authenticate(ctx, privateReaderKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := core2.ReadFile(ctx, reader2, managerFile); err != nil {
		t.Fatalf("second instance initial read: %v", err)
	}
	if err := core.SetGroupAccess(ctx, manager, privateReaderID, "private", access.LevelNone); err != nil {
		t.Fatal(err)
	}
	if _, _, err := core2.ReadFile(ctx, reader2, managerFile); !errors.Is(err, access.ErrForbidden) {
		t.Fatalf("second instance returned bytes after access revocation: %v", err)
	}
	if files, _, err := core.SearchFiles(ctx, reader2, "private", "", "", "", 20); err != nil || len(files) != 0 {
		t.Fatalf("search leaked revoked file: count=%d err=%v", len(files), err)
	}
	if err := core.SetGroupAccess(ctx, manager, privateReaderID, "private", access.LevelRead); err != nil {
		t.Fatal(err)
	}
	if _, _, err := core2.ReadFile(ctx, reader2, managerFile); err != nil {
		t.Fatalf("read after permission restoration: %v", err)
	}
	credentials, err := core.ListCredentials(ctx, manager, viewerID)
	if err != nil || len(credentials) != 1 {
		t.Fatalf("viewer credentials: count=%d err=%v", len(credentials), err)
	}
	newCredentialID, newCredential, err := core.IssueCredential(ctx, manager, viewerID, nil)
	if err != nil {
		t.Fatal(err)
	}
	newViewerSession, err := core2.Authenticate(ctx, newCredential)
	if err != nil {
		t.Fatalf("newly issued credential did not authenticate on another instance: %v", err)
	}
	if err := core.RevokeCredential(ctx, manager, credentials[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := core2.Authenticate(ctx, viewerKey); !errors.Is(err, access.ErrUnauthorized) {
		t.Fatalf("revoked credential remained valid on a second instance: %v", err)
	}
	if _, _, err := core2.ReadFile(ctx, viewer, managerFile); !errors.Is(err, access.ErrUnauthorized) {
		t.Fatalf("existing session survived credential revocation: %v", err)
	}
	if _, _, err := core2.ReadFile(ctx, newViewerSession, managerFile); !errors.Is(err, access.ErrForbidden) {
		t.Fatalf("newly issued credential has unexpected file access: %v", err)
	}
	if err := core.RevokeCredential(ctx, manager, newCredentialID); err != nil {
		t.Fatal(err)
	}
	if err := core.DeactivatePrincipal(ctx, manager, viewerID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := core2.ReadFile(ctx, newViewerSession, managerFile); !errors.Is(err, access.ErrUnauthorized) {
		t.Fatalf("deactivated account retained an existing session: %v", err)
	}
	authorizationFailureCtx, cancelAuthorizationFailure := context.WithCancel(ctx)
	cancelAuthorizationFailure()
	if _, _, err := core2.ReadFile(authorizationFailureCtx, reader2, managerFile); err == nil {
		t.Fatal("Core returned cached bytes without a successful database permission check")
	}

	if err := core.DeleteFile(ctx, manager, managerFile, renamed.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := core.StatFile(ctx, manager, managerFile); !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("permanently deleted file remained addressable: %v", err)
	}
}

func newCoreV2TestConfig(t *testing.T, dsn, migrationDSN, endpoint, bucket, accessKey, secretKey string) RuntimeConfig {
	t.Helper()
	cfg := RuntimeConfig{
		Database:             DatabaseConfig{URL: dsn, MinConnections: 1, MaxConnections: 5, ConnectTimeout: 5 * time.Second, MaxLifetime: time.Hour, MaxIdleTime: time.Minute, HealthInterval: time.Second},
		MigrationDatabaseURL: migrationDSN,
		S3:                   S3Config{Endpoint: endpoint, Region: "us-east-1", Bucket: bucket, AccessKey: accessKey, SecretKey: secretKey, PathStyle: true, AllowInsecureHTTP: true},
		AtlasBinary:          os.Getenv("TEST_ATLAS_BINARY"), MigrationLockTimeout: 30 * time.Second,
	}
	if cfg.AtlasBinary == "" {
		cfg.AtlasBinary = "atlas"
	}
	values := map[string]any{
		"AccessGroups":         map[string]access.Level{"assets": access.LevelWrite, "features": access.LevelWrite, "private": access.LevelWrite, "restricted": access.LevelWrite, "content": access.LevelWrite},
		"CacheMaxBytes":        int64(1 << 20),
		"CacheTTL":             time.Minute,
		"MaxCachedObjectBytes": int64(8),
		"MaxMetadataBytes":     int64(64 << 10),
		"MaxObjectBytes":       int64(16 << 20),
		"MaintenanceInterval":  time.Hour,
		"MaintenanceTimeout":   10 * time.Second,
		"PendingTTL":           time.Hour,
		"MaintenanceBatchSize": 100,
	}
	for name, data := range values {
		setRuntimeConfigField(t, &cfg, name, data)
	}
	return cfg
}

func withApplicationName(t *testing.T, dsn, name string) string {
	t.Helper()
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("application_name", name)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func testCacheListenerReconnect(t *testing.T, ctx context.Context, runtime *Runtime, cfg RuntimeConfig, adminDSN string) {
	t.Helper()
	if adminDSN == "" {
		t.Fatal("TEST_CORE_PLATFORM_ADMIN_DATABASE_URL must be a disposable database administrator login for listener recovery acceptance")
	}
	adminConfig, err := pgxpool.ParseConfig(adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	runtimeConfig, err := pgxpool.ParseConfig(cfg.Database.URL)
	if err != nil {
		t.Fatal(err)
	}
	if adminConfig.ConnConfig.Database != runtimeConfig.ConnConfig.Database ||
		adminConfig.ConnConfig.Host != runtimeConfig.ConnConfig.Host || adminConfig.ConnConfig.Port != runtimeConfig.ConnConfig.Port ||
		adminConfig.ConnConfig.User == runtimeConfig.ConnConfig.User ||
		!strings.HasPrefix(adminConfig.ConnConfig.Database, "zubrium_core_qa_") {
		t.Fatal("cache-listener administrator DSN must use a separate login on the same disposable Core database")
	}
	adminConfig.MaxConns = 2
	adminPool, err := pgxpool.NewWithConfig(ctx, adminConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(adminPool.Close)

	listenerPID := waitForCacheListener(t, ctx, adminPool, "core-v2-primary", 0)
	probeKey := "listener-reconnect-probe"
	runtime.byteCache.Put(probeKey, []byte("cached"), time.Minute)
	var terminated bool
	if err := adminPool.QueryRow(ctx, `SELECT pg_terminate_backend($1)`, listenerPID).Scan(&terminated); err != nil || !terminated {
		t.Fatalf("terminate cache-listener connection %d: terminated=%v err=%v", listenerPID, terminated, err)
	}
	waitForCacheProbe(t, ctx, runtime, probeKey, true)
	waitForCacheListener(t, ctx, adminPool, "core-v2-primary", listenerPID)

	runtime.byteCache.Put(probeKey, []byte("cached after reconnect"), time.Minute)
	if _, err := adminPool.Exec(ctx, `SELECT pg_notify('core_cache_invalidation','*')`); err != nil {
		t.Fatalf("publish invalidation after listener reconnect: %v", err)
	}
	waitForCacheProbe(t, ctx, runtime, probeKey, true)
}

func waitForCacheListener(t *testing.T, ctx context.Context, pool *pgxpool.Pool, application string, previousPID int32) int32 {
	t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		var pid int32
		err := pool.QueryRow(ctx, `SELECT pid FROM pg_stat_activity
			WHERE datname=current_database() AND application_name=$1 AND state='idle'
			AND upper(query) LIKE 'LISTEN CORE_CACHE_INVALIDATION%' ORDER BY backend_start LIMIT 1`, application).Scan(&pid)
		if err == nil && pid != previousPID {
			return pid
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for cache listener %q: %v", application, ctx.Err())
		case <-deadline.C:
			t.Fatalf("cache listener %q did not reconnect after pid %d (last query error: %v)", application, previousPID, err)
		case <-ticker.C:
		}
	}
}

func waitForCacheProbe(t *testing.T, ctx context.Context, runtime *Runtime, key string, expectMissing bool) {
	t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		_, exists := runtime.byteCache.Get(key)
		if exists != expectMissing {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for cache invalidation: %v", ctx.Err())
		case <-deadline.C:
			t.Fatal("cache invalidation listener did not clear the local cache")
		case <-ticker.C:
		}
	}
}

func assertIsolatedCoreS3Endpoint(t *testing.T, endpoint string) {
	t.Helper()
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Hostname() == "" {
		t.Fatal("invalid disposable Core S3 endpoint")
	}
	if parsed.Scheme == "https" {
		return
	}
	ip := net.ParseIP(parsed.Hostname())
	if parsed.Scheme != "http" || (parsed.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback())) {
		t.Fatal("refusing non-loopback HTTP S3 endpoint for a test carrying credentials")
	}
}

func setRuntimeConfigField(t *testing.T, cfg *RuntimeConfig, name string, data any) {
	t.Helper()
	field := reflect.ValueOf(cfg).Elem().FieldByName(name)
	if !field.IsValid() || !field.CanSet() || !reflect.ValueOf(data).Type().AssignableTo(field.Type()) {
		t.Fatalf("RuntimeConfig.%s does not match the frozen Core v2 config contract", name)
	}
	field.Set(reflect.ValueOf(data))
}

func newRequestKey(t *testing.T) string {
	t.Helper()
	id, err := uuid.NewRandom()
	if err != nil {
		t.Fatal(err)
	}
	return id.String()
}

type controlledObjectWriter struct {
	postgres.ObjectWriter
	putFailure            error
	deleteFailure         error
	afterSuccessfulDelete func()
	putStarted            chan struct{}
	releasePut            <-chan struct{}
}

func (w *controlledObjectWriter) Put(ctx context.Context, key string, body io.Reader, size int64, contentType string) error {
	if w.putFailure != nil {
		return w.putFailure
	}
	if w.putStarted != nil {
		close(w.putStarted)
	}
	if w.releasePut != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-w.releasePut:
		}
	}
	return w.ObjectWriter.Put(ctx, key, body, size, contentType)
}

func (w *controlledObjectWriter) Delete(ctx context.Context, key string) error {
	if w.deleteFailure != nil {
		return w.deleteFailure
	}
	if err := w.ObjectWriter.Delete(ctx, key); err != nil {
		return err
	}
	if w.afterSuccessfulDelete != nil {
		w.afterSuccessfulDelete()
	}
	return nil
}

func principalCanManage(t *testing.T, principal Principal) bool {
	t.Helper()
	field := reflect.ValueOf(principal).FieldByName("CanManageAdministrators")
	if !field.IsValid() || field.Kind() != reflect.Bool {
		t.Fatal("principal contract is missing CanManageAdministrators")
	}
	return field.Bool()
}

func fileMetadataValue(t *testing.T, file File, key string) string {
	t.Helper()
	metadata := reflect.ValueOf(file).FieldByName("Metadata")
	if !metadata.IsValid() || metadata.Kind() != reflect.Map || metadata.Type().Key().Kind() != reflect.String || metadata.Type().Elem().Kind() != reflect.String {
		t.Fatal("File.Metadata must be a string-to-string map")
	}
	value := metadata.MapIndex(reflect.ValueOf(key))
	if !value.IsValid() {
		return ""
	}
	return value.String()
}

func hasFileGroup(t *testing.T, file File, want string) bool {
	t.Helper()
	field := reflect.ValueOf(file).FieldByName("Groups")
	if !field.IsValid() || field.Kind() != reflect.Slice {
		t.Fatal("File contract does not contain a Groups slice")
	}
	for i := 0; i < field.Len(); i++ {
		if field.Index(i).String() == want {
			return true
		}
	}
	return false
}
