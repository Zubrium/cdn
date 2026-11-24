//go:build website_acceptance

package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	core "github.com/Zubrium/cdn/core/presentation"
	websiteengine "github.com/Zubrium/cdn/engines/website"
	websiteapp "github.com/Zubrium/cdn/engines/website/application"
	websiteapi "github.com/Zubrium/cdn/engines/website/presentation"
	websiterepo "github.com/Zubrium/cdn/engines/website/repository"
	"github.com/Zubrium/cdn/main/internal/composition"
	"github.com/Zubrium/cdn/pkg/contracts"
	"github.com/Zubrium/cdn/workers/archive"
	"github.com/google/uuid"
)

// websiteRunBootstrapSecret keeps one run-scoped bootstrap identity across
// disposable integration scenarios that share the same PostgreSQL instance.
func websiteRunBootstrapSecret(runID string) string {
	digest := sha256.Sum256([]byte("web012-bootstrap-" + runID))
	return "ct_live_" + base64.RawURLEncoding.EncodeToString(digest[:])
}

// TestWebsiteFeatureImportAcceptance is the executable WEB-012 check. It uses
// only disposable PostgreSQL/S3 inputs and never skips when they are missing.
func TestWebsiteFeatureImportAcceptance(t *testing.T) {
	t.Run("WEB-012", func(t *testing.T) {
		env := websitePersistenceEnvironment(t)
		mock := httptest.NewServer(newWeb012S3Handler(env.s3Bucket))
		t.Cleanup(mock.Close)
		env.s3Endpoint = mock.URL
		env.s3AccessKey = "test-access"
		env.s3SecretKey = "test-secret"
		env.insecureS3HTTP = true

		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()
		// The disposable PostgreSQL instance may be reused between RED/GREEN
		// runs. A deterministic run-scoped bootstrap credential keeps the same
		// Core identity valid without touching existing records.
		bootstrap := websiteRunBootstrapSecret(env.runID)
		config := websiteRuntimeConfig(env, bootstrap)
		config.AccessGroups["features"] = core.AccessLevelWrite
		runtime, secret, err := core.Start(ctx, config)
		if err != nil {
			t.Fatalf("start disposable Core: %v", err)
		}
		credential := secret
		if credential == "" {
			credential = bootstrap
		}
		t.Cleanup(func() {
			closeCtx, closeCancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer closeCancel()
			if err := runtime.CloseContext(closeCtx); err != nil {
				t.Errorf("close disposable Core: %v", err)
			}
		})

		if err := runtime.Registry.Register("coordinator"); err != nil {
			t.Fatalf("register disposable Coordinator: %v", err)
		}
		if err := runtime.Registry.Register(websiteengine.RecordDescriptor().OwnerID); err != nil {
			t.Fatalf("register Website module: %v", err)
		}
		if err := runtime.Registry.Register(archive.DataModuleID); err != nil {
			t.Fatalf("register Archive module: %v", err)
		}
		websiteData, err := websiteAcceptanceRecordStore(t, runtime, websiteengine.RecordDescriptor())
		if err != nil {
			t.Fatalf("register Website module data: %v", err)
		}
		archiveData, err := websiteAcceptanceRecordStore(t, runtime, archive.DataDescriptor())
		if err != nil {
			t.Fatalf("register Archive module data: %v", err)
		}

		identity := runtime.Identity()
		serviceSession, _, err := identity.Authenticate(ctx, credential)
		if err != nil {
			t.Fatalf("authenticate service identity: %v", err)
		}
		files := runtime.Files()
		fileSet, err := files.CreateManagedFileSet(ctx, serviceSession, "web012", "archive", "features")
		if err != nil {
			t.Fatalf("create run-owned target FileSet: %v", err)
		}
		t.Cleanup(func() {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cleanupCancel()
			current, err := files.GetManagedFileSet(cleanupCtx, serviceSession, fileSet.ID)
			if err != nil {
				return
			}
			if err := files.DeleteManagedFileSet(cleanupCtx, serviceSession, fileSet.ID, current.Revision); err != nil {
				t.Errorf("delete run-owned FileSet: %v", err)
			}
		})

		staging := t.TempDir()
		if err := os.Chmod(staging, 0o700); err != nil {
			t.Fatalf("secure run-owned archive staging: %v", err)
		}
		failures := &web012FailureInjector{sourceDeleteFailures: map[string]int{}}
		coreFiles := &web012CoreFiles{testCoreFilesAdapter: testCoreFilesAdapter{files: files, session: serviceSession}, failures: failures}
		handler, err := archive.NewHandler(coreFiles, archive.Config{
			Limits: archive.Limits{
				MaxArchiveBytes: 1 << 20, MaxEntries: 16, MaxEntryBytes: 1 << 16,
				MaxExpandedBytes: 1 << 18, MaxCompressionRatio: 100,
			},
			StagingDir:      staging,
			TargetFileSetID: fileSet.ID,
			OutputGroups:    []string{"features"},
			Progress:        archiveData,
			Index:           coreFiles,
		})
		if err != nil {
			t.Fatalf("construct real Archive handler: %v", err)
		}
		operationKey := contracts.Key{ID: archive.OperationID, Version: 1}
		bindings := map[contracts.Key]composition.TaskOperationBinding{operationKey: {
			TaskClass: archive.DataModuleID, Handler: handler.WorkerHandler,
		}}
		dispatcher := websiteAcceptanceExecutor(t, bindings)
		fingerprint := sha256.Sum256([]byte("web012-" + env.runID))
		if err := runtime.StartCoordinator(ctx, core.CoordinatorRuntimeConfig{
			Fingerprint: hex.EncodeToString(fingerprint[:]), InstanceLease: 30 * time.Second,
			HeartbeatInterval: 5 * time.Second,
			RetryBase:         50 * time.Millisecond, RetryMaximum: 250 * time.Millisecond,
			MaxAttempts: 2, MaxWorkflowTasks: 16, MaxChildrenPerTask: 4,
			WorkflowRetention: 24 * time.Hour, ShutdownTimeout: 15 * time.Second,
			TaskClasses: []string{archive.DataModuleID},
			Pools: websiteAcceptancePools(t, dispatcher, bindings,
				composition.TaskPoolSpec{ClassID: archive.DataModuleID, Concurrency: 2, LeaseDuration: 10 * time.Second, TaskTimeout: 30 * time.Second, PollInterval: 50 * time.Millisecond}),
		}); err != nil {
			t.Fatalf("start disposable Coordinator: %v", err)
		}
		t.Cleanup(func() {
			stopCtx, stopCancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer stopCancel()
			if err := runtime.StopCoordinator(stopCtx); err != nil {
				t.Errorf("stop disposable Coordinator: %v", err)
			}
		})
		if err := runtime.ActivateCoordinator(ctx); err != nil {
			t.Fatalf("activate disposable Coordinator: %v", err)
		}
		ports := composition.NewWebsiteCorePorts(runtime)
		workflow := composition.NewWebsiteWorkflowClient(runtime, archive.DataModuleID)
		service, err := websiteapp.NewService(websiteapp.Config{
			AccessGroup: "features", CoordinatorAccessGroup: "features",
			Files: ports.Files, Workflow: workflow, Sessions: ports.Sessions,
			FeatureImportFileSetID: fileSet.ID,
		}, websiteData, ports.Authority, ports.Sessions)
		if err != nil {
			t.Fatalf("construct Website service: %v", err)
		}
		writeCtx, err := service.Authenticate(ctx, credential)
		if err != nil {
			t.Fatalf("authenticate Website service: %v", err)
		}

		validZIP := web012FeatureZIP(t, "web012-valid", "1.2.3")
		validSource := web012CreateSource(t, ctx, files, serviceSession, env.runID+"-valid", validZIP)
		failures.sourceDeleteFailures[validSource] = 1
		validKey := env.runID + "-valid-key"
		job, err := service.BeginFeatureImport(writeCtx, websiteapi.ImportFeatureRequest{
			SourceFileID: validSource, IdempotencyKey: validKey, ExpectedSHA256: web012Digest(validZIP),
		})
		if err != nil || job.ID == "" {
			t.Fatalf("BeginFeatureImport = %+v, %v", job, err)
		}
		ready := web012AwaitImport(t, ctx, service, writeCtx, job.ID)
		if ready.State != "ready" || !ready.SourceDeleted {
			t.Fatalf("feature import did not become ready: %+v", ready)
		}
		if failures.deleteAttempts(validSource) < 2 {
			t.Fatalf("source cleanup was not retried after partial progress: attempts=%d", failures.deleteAttempts(validSource))
		}
		if creates := coreFiles.createCount(validSource); creates != 2 {
			t.Fatalf("recovery created %d output files, want exactly 2", creates)
		}

		replay, err := service.BeginFeatureImport(writeCtx, websiteapi.ImportFeatureRequest{
			SourceFileID: validSource, IdempotencyKey: validKey, ExpectedSHA256: web012Digest(validZIP),
		})
		if err != nil || replay.ID != job.ID {
			t.Fatalf("same-key replay = %+v, %v; want job %s", replay, err, job.ID)
		}
		conflictSource := web012CreateSource(t, ctx, files, serviceSession, env.runID+"-conflict", validZIP)
		if _, err := service.BeginFeatureImport(writeCtx, websiteapi.ImportFeatureRequest{
			SourceFileID: conflictSource, IdempotencyKey: validKey, ExpectedSHA256: web012Digest(validZIP),
		}); err == nil || !isWebsiteConflict(err) {
			t.Fatalf("conflicting fingerprint = %v, want Website ErrConflict", err)
		}

		setFiles, _, err := files.ListFileSet(ctx, serviceSession, fileSet.ID, "", 100)
		if err != nil || len(setFiles) != 2 {
			t.Fatalf("target FileSet has %d files after recovery, want 2: %v", len(setFiles), err)
		}
		for _, file := range setFiles {
			if file.ID == validSource {
				t.Fatal("source archive remained in target FileSet after recovery")
			}
		}
		if _, err := websiteData.Get(ctx, websiterepo.FeatureVersionKey("web012-valid", "1.2.3")); err != nil {
			t.Fatalf("ready feature version is missing: %v", err)
		}

		invalidZIP := []byte("not a ZIP archive")
		invalidSource := web012CreateSource(t, ctx, files, serviceSession, env.runID+"-invalid", invalidZIP)
		invalidJob, err := service.BeginFeatureImport(writeCtx, websiteapi.ImportFeatureRequest{
			SourceFileID: invalidSource, IdempotencyKey: env.runID + "-invalid-key",
		})
		if err != nil || invalidJob.ID == "" {
			t.Fatalf("BeginFeatureImport(invalid) = %+v, %v", invalidJob, err)
		}
		rejected := web012AwaitFailure(t, ctx, service, writeCtx, invalidJob.ID)
		if rejected.State != "failed" {
			t.Fatalf("invalid archive state = %q, want failed", rejected.State)
		}
		if _, err := websiteData.Get(ctx, websiterepo.FeatureVersionKey("web012-valid", "9.9.9")); err == nil {
			t.Fatal("invalid archive produced a ready feature version")
		}
		if creates := coreFiles.createCount(invalidSource); creates != 0 {
			t.Fatalf("invalid archive created %d output files", creates)
		}

		keys, _, _, err := websiteData.List(ctx, websiterepo.ImportNamespace, "job", websiterepo.ImportScopeID, "", nil, 100)
		if err != nil {
			t.Errorf("list run-owned import records for cleanup: %v", err)
		} else {
			for _, key := range keys {
				record, err := websiteData.Get(ctx, key)
				if err != nil {
					continue
				}
				if err := websiteData.Delete(ctx, key, record.Revision); err != nil {
					t.Errorf("delete run-owned import record %+v: %v", key, err)
				}
			}
		}
		for _, key := range []contracts.RecordKey{
			websiterepo.FeatureVersionKey("web012-valid", "1.2.3"),
		} {
			record, err := websiteData.Get(ctx, key)
			if err != nil {
				continue
			}
			if err := websiteData.Delete(ctx, key, record.Revision); err != nil {
				t.Errorf("delete run-owned catalog record %+v: %v", key, err)
			}
		}
		for _, file := range setFiles {
			current, err := files.StatFile(ctx, serviceSession, file.ID)
			if err == nil {
				if err := files.DeleteFile(ctx, serviceSession, file.ID, current.Revision); err != nil {
					t.Errorf("delete run-owned output file: %v", err)
				}
			}
		}
	})
}

type web012FailureInjector struct {
	mu                   sync.Mutex
	sourceDeleteFailures map[string]int
	deleteAttemptsCount  map[string]int
}

func (i *web012FailureInjector) failDelete(sourceID string) bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.deleteAttemptsCount == nil {
		i.deleteAttemptsCount = make(map[string]int)
	}
	i.deleteAttemptsCount[sourceID]++
	if i.sourceDeleteFailures[sourceID] > 0 {
		i.sourceDeleteFailures[sourceID]--
		return true
	}
	return false
}

func (i *web012FailureInjector) deleteAttempts(sourceID string) int {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.deleteAttemptsCount[sourceID]
}

type web012CoreFiles struct {
	testCoreFilesAdapter
	failures *web012FailureInjector

	mu      sync.Mutex
	creates map[string]int
}

func (f *web012CoreFiles) StatFile(ctx context.Context, id string) (contracts.File, error) {
	return f.testCoreFilesAdapter.StatFile(ctx, id)
}

func (f *web012CoreFiles) ReadFile(ctx context.Context, id string) (io.ReadCloser, contracts.File, error) {
	return f.testCoreFilesAdapter.ReadFile(ctx, id)
}

func (f *web012CoreFiles) CreateFileVerified(ctx context.Context, requestKey string, groups []string, metadata map[string]string, contentType string, size int64, checksum string, body io.Reader) (string, error) {
	id, err := f.testCoreFilesAdapter.CreateFileVerified(ctx, requestKey, groups, metadata, contentType, size, checksum, body)
	if err == nil {
		f.mu.Lock()
		if f.creates == nil {
			f.creates = make(map[string]int)
		}
		f.creates[metadata["archive_source_id"]]++
		f.mu.Unlock()
	}
	return id, err
}

func (f *web012CoreFiles) createCount(sourceID string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.creates[sourceID]
}

func (f *web012CoreFiles) DeleteFile(ctx context.Context, id string, expected int64) error {
	if f.failures.failDelete(id) {
		return fmt.Errorf("web012 injected source cleanup failure")
	}
	return f.testCoreFilesAdapter.DeleteFile(ctx, id, expected)
}

func web012CreateSource(t *testing.T, ctx context.Context, files core.FilesCapability, session core.Session, name string, payload []byte) string {
	t.Helper()
	id, err := files.CreateFile(ctx, session, uuid.NewString(), []string{"features"},
		map[string]string{"web012_source": name}, "application/zip", int64(len(payload)), bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("create run-owned source file %s: %v", name, err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		current, err := files.StatFile(cleanupCtx, session, id)
		if err == nil {
			_ = files.DeleteFile(cleanupCtx, session, id, current.Revision)
		}
	})
	return id
}

func web012FeatureZIP(t *testing.T, featureID, version string) []byte {
	t.Helper()
	manifest := fmt.Sprintf(`{"formatVersion":2,"key":%q,"name":"WEB-012 Feature","version":%q,"engineApi":"v1","slots":["main"],"arguments":{},"dataInputs":[],"runtime":{"abi":"static-lifecycle","entry":"assets/app.js","styles":[]}}`, featureID, version)
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	writeEntry := func(name, body string) {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatalf("create ZIP entry %s: %v", name, err)
		}
		if _, err := io.WriteString(entry, body); err != nil {
			t.Fatalf("write ZIP entry %s: %v", name, err)
		}
	}
	writeEntry("manifest.json", manifest)
	writeEntry("assets/app.js", "export function mount() {}")
	if err := writer.Close(); err != nil {
		t.Fatalf("close feature ZIP: %v", err)
	}
	return buffer.Bytes()
}

func web012Digest(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func web012AwaitImport(t *testing.T, ctx context.Context, service *websiteapp.Service, authCtx context.Context, jobID string) websiteapi.FeatureImport {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for {
		job, err := service.GetFeatureImport(authCtx, jobID)
		if err != nil {
			t.Fatalf("GetFeatureImport(%s): %v", jobID, err)
		}
		if job.State == "ready" {
			return job
		}
		if job.State == "failed" || job.State == "rejected" {
			t.Fatalf("import %s reached %s: %s", jobID, job.State, job.ErrorCode)
		}
		if time.Now().After(deadline) {
			t.Fatalf("import %s did not reach a terminal state: %+v", jobID, job)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func web012AwaitFailure(t *testing.T, ctx context.Context, service *websiteapp.Service, authCtx context.Context, jobID string) websiteapi.FeatureImport {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for {
		job, err := service.GetFeatureImport(authCtx, jobID)
		if err != nil {
			t.Fatalf("GetFeatureImport(%s): %v", jobID, err)
		}
		if job.State == "failed" || job.State == "rejected" {
			return job
		}
		if time.Now().After(deadline) {
			t.Fatalf("invalid import %s did not reach a terminal state: %+v", jobID, job)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func isWebsiteConflict(err error) bool {
	return err == websiteapi.ErrConflict || strings.Contains(err.Error(), "conflict")
}

func newWeb012S3Handler(bucket string) http.HandlerFunc {
	mu := &sync.Mutex{}
	objects := make(map[string][]byte)
	contentTypes := make(map[string]string)
	const xmlns = `http://s3.amazonaws.com/doc/2006-03-01/`
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		switch {
		case r.URL.Query().Has("publicAccessBlock"):
			_, _ = fmt.Fprintf(w, `<PublicAccessBlockConfiguration xmlns=%q><BlockPublicAcls>true</BlockPublicAcls><IgnorePublicAcls>true</IgnorePublicAcls><BlockPublicPolicy>true</BlockPublicPolicy><RestrictPublicBuckets>true</RestrictPublicBuckets></PublicAccessBlockConfiguration>`, xmlns)
			return
		case r.URL.Query().Has("versioning"):
			_, _ = fmt.Fprintf(w, `<VersioningConfiguration xmlns=%q></VersioningConfiguration>`, xmlns)
			return
		case r.URL.Query().Has("acl"):
			_, _ = fmt.Fprintf(w, `<AccessControlPolicy xmlns=%q><Owner><ID>test-owner</ID></Owner><AccessControlList><Grant><Grantee><ID>test-owner</ID></Grantee><Permission>FULL_CONTROL</Permission></Grant></AccessControlList></AccessControlPolicy>`, xmlns)
			return
		case r.URL.Query().Has("policyStatus"):
			_, _ = fmt.Fprintf(w, `<PolicyStatus xmlns=%q><IsPublic>false</IsPublic></PolicyStatus>`, xmlns)
			return
		}
		prefix := "/" + bucket + "/"
		if !strings.HasPrefix(r.URL.Path, prefix) {
			http.Error(w, `<Error><Code>NoSuchKey</Code></Error>`, http.StatusNotFound)
			return
		}
		key := strings.TrimPrefix(r.URL.Path, prefix)
		mu.Lock()
		defer mu.Unlock()
		switch r.Method {
		case http.MethodPut:
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, `<Error><Code>InvalidRequest</Code></Error>`, http.StatusBadRequest)
				return
			}
			objects[key] = body
			contentTypes[key] = r.Header.Get("Content-Type")
			w.Header().Set("ETag", `"`+web012Digest(body)+`"`)
			w.WriteHeader(http.StatusOK)
		case http.MethodGet, http.MethodHead:
			body, ok := objects[key]
			if !ok {
				http.Error(w, `<Error><Code>NoSuchKey</Code></Error>`, http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", contentTypes[key])
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			w.Header().Set("ETag", `"`+web012Digest(body)+`"`)
			w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
			if r.Method == http.MethodHead {
				w.WriteHeader(http.StatusOK)
				return
			}
			if r.Header.Get("Range") == "" {
				_, _ = w.Write(body)
				return
			}
			var start, end int
			if _, err := fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &start, &end); err != nil || start < 0 || end < start || end >= len(body) {
				w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", len(body)))
				http.Error(w, `<Error><Code>InvalidRange</Code></Error>`, http.StatusRequestedRangeNotSatisfiable)
				return
			}
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(body)))
			w.Header().Set("Content-Length", strconv.Itoa(end-start+1))
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(body[start : end+1])
		case http.MethodDelete:
			delete(objects, key)
			delete(contentTypes, key)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, `<Error><Code>MethodNotAllowed</Code></Error>`, http.StatusMethodNotAllowed)
		}
	})
}
