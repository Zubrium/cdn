package application

import (
	"bytes"
	"context"
	"errors"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	api "github.com/Zubrium/cdn/engines/loader/presentation"
	"github.com/Zubrium/cdn/pkg/observability"
	"github.com/google/uuid"
)

func admissionLoaderObserver(t *testing.T) *observability.Observer {
	t.Helper()
	observer, err := observability.New(observability.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = observer.Close(context.Background()) })
	return observer
}

func admissionLoaderMetricValue(t *testing.T, observer *observability.Observer, name, label string) float64 {
	t.Helper()
	recorder := httptest.NewRecorder()
	observer.MetricsHandler().ServeHTTP(recorder, httptest.NewRequest("GET", "/metrics", nil))
	for _, line := range strings.Split(recorder.Body.String(), "\n") {
		if !strings.HasPrefix(line, name+"{") && !strings.HasPrefix(line, name+" ") {
			continue
		}
		if !strings.Contains(line, label) {
			continue
		}
		fields := strings.Fields(line)
		value, err := strconv.ParseFloat(fields[len(fields)-1], 64)
		if err != nil {
			t.Fatalf("metric line %q: %v", line, err)
		}
		return value
	}
	t.Fatalf("metric %s with %q is absent; snapshot:\n%s", name, label, recorder.Body.String())
	return 0
}

func admissionLoaderService(t *testing.T, observer *observability.Observer) (*Service, context.Context, *testFiles) {
	t.Helper()
	identity := &testIdentity{principalID: uuid.NewString(), writable: true, active: true}
	files := newTestFiles()
	data := newTestData()
	config := Config{
		PartSize: 4, MaxFileBytes: 128, MaxPartBytes: 4, MaxParts: 32,
		MaxConcurrentCommits: 1, MaxConcurrentSessionLookups: 1,
		SessionTTL: time.Hour, CommitTimeout: time.Minute, Mode: ModeAuto,
		BufferThreshold: 8, MaxBufferedBytes: 16,
		SessionCacheEntries: 8, SessionCacheMaxBytes: 1 << 20, SessionCacheTTL: time.Minute,
		CleanupInterval: time.Minute, CleanupBatchSize: 16,
	}
	service, err := New(identity, files, data, config)
	if err != nil {
		t.Fatal(err)
	}
	service.WithObserver(observer)
	ctx, err := service.Authenticate(context.Background(), "admission-key")
	if err != nil {
		t.Fatal(err)
	}
	return service, ctx, files
}

func admissionReadyUpload(t *testing.T, service *Service, ctx context.Context, key string) string {
	t.Helper()
	body := []byte("data")
	upload, err := service.BeginUpload(ctx, uploadInput(key, body))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.PutUploadPart(ctx, upload.ID, 1, int64(len(body)), digest(body), bytes.NewReader(body)); err != nil {
		t.Fatal(err)
	}
	return upload.ID
}

func TestConcurrencyAdmissionScenario(t *testing.T) {
	t.Run("CONC-009", func(t *testing.T) {
		observer := admissionLoaderObserver(t)
		service, ctx, files := admissionLoaderService(t, observer)
		first := admissionReadyUpload(t, service, ctx, "admission-commit-first")
		second := admissionReadyUpload(t, service, ctx, "admission-commit-second")
		entered := make(chan struct{})
		release := make(chan struct{})
		files.beforeCreate = func() {
			close(entered)
			<-release
		}

		firstDone := make(chan error, 1)
		go func() {
			_, err := service.CommitUpload(ctx, first)
			firstDone <- err
		}()
		<-entered
		overloadStarted := time.Now()
		if _, err := service.CommitUpload(ctx, second); !errors.Is(err, api.ErrUnavailable) {
			t.Fatalf("commit above concurrency limit = %v, want ErrUnavailable", err)
		}
		if elapsed := time.Since(overloadStarted); elapsed > 100*time.Millisecond {
			t.Fatalf("commit overload waited %s, want fail-fast", elapsed)
		}
		if len(files.files) != 0 {
			t.Fatalf("rejected commit created %d files", len(files.files))
		}
		if got := admissionLoaderMetricValue(t, observer, "zubrium_concurrency_rejected_total", `reason="GLOBAL"`); got != 1 {
			t.Fatalf("rejected{GLOBAL} = %v, want 1", got)
		}
		if got := admissionLoaderMetricValue(t, observer, "zubrium_concurrency_in_flight", `scope="LOADER"`); got != 1 {
			recorder := httptest.NewRecorder()
			observer.MetricsHandler().ServeHTTP(recorder, httptest.NewRequest("GET", "/metrics", nil))
			t.Fatalf("loader in-flight = %v, want 1; snapshot:\n%s", got, recorder.Body.String())
		}
		close(release)
		if err := <-firstDone; err != nil {
			t.Fatalf("admitted commit failed: %v", err)
		}

		lookupRelease, err := service.acquireSessionLookupSlot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		data := service.data.(*testData)
		dataRecords := len(data.records)
		if _, err := service.UploadStatus(ctx, first); !errors.Is(err, api.ErrUnavailable) {
			t.Fatalf("session lookup above limit = %v, want ErrUnavailable", err)
		}
		if len(files.files) != 1 || len(data.records) != dataRecords {
			t.Fatal("rejected session lookup changed loader or Core state")
		}
		lookupRelease()
		if _, err := service.UploadStatus(ctx, first); err != nil {
			t.Fatalf("session lookup after release = %v", err)
		}
	})

	t.Run("CONC-011", func(t *testing.T) {
		observer := admissionLoaderObserver(t)
		service, ctx, _ := admissionLoaderService(t, observer)
		commitRelease, err := service.acquireCommitSlot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if got := admissionLoaderMetricValue(t, observer, "zubrium_concurrency_in_flight", `scope="LOADER"`); got != 1 {
			t.Fatalf("loader in-flight after commit acquisition = %v, want 1", got)
		}
		commitRelease()
		commitRelease()
		if got := admissionLoaderMetricValue(t, observer, "zubrium_concurrency_in_flight", `scope="LOADER"`); got != 0 {
			t.Fatalf("loader in-flight after repeated commit release = %v, want 0", got)
		}
		lookupRelease, err := service.acquireSessionLookupSlot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if got := admissionLoaderMetricValue(t, observer, "zubrium_concurrency_in_flight", `scope="LOADER"`); got != 1 {
			t.Fatalf("loader in-flight after lookup acquisition = %v, want 1", got)
		}
		lookupRelease()
		lookupRelease()
		if got := admissionLoaderMetricValue(t, observer, "zubrium_concurrency_in_flight", `scope="LOADER"`); got != 0 {
			t.Fatalf("loader in-flight after repeated lookup release = %v, want 0", got)
		}
	})
}
