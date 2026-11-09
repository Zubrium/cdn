package integration

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"

	"testing"
	"time"

	"websiteengine/features/sitebuilder"

	"websiteengine/pkg/environment"
	publicruntime "websiteengine/pkg/runtime"
	httptransport "websiteengine/presentation/http"
	hmedia "websiteengine/presentation/http/handlers/media"
	hsitebuilder "websiteengine/presentation/http/handlers/sitebuilder"
)

func seoPublicServer(t *testing.T, e *testEnv, branch sitebuilder.BranchRecord, options *publicruntime.PrerenderOptions) *httptest.Server {
	server, _ := seoPublicServerWithWorker(t, e, branch, options)
	return server
}

func seoPublicServerWithWorker(t *testing.T, e *testEnv, branch sitebuilder.BranchRecord, options *publicruntime.PrerenderOptions) (*httptest.Server, *publicruntime.Prerenderer) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	env := environment.Environment(branch.ID.String())
	server := httptest.NewUnstartedServer(http.NotFoundHandler())
	workers := map[environment.Environment]*publicruntime.Prerenderer{}
	var worker *publicruntime.Prerenderer
	if options != nil {
		var err error
		worker, err = publicruntime.NewPrerenderer("http://"+server.Listener.Addr().String(), *options)
		if err != nil {
			t.Fatal(err)
		}
		workers[env] = worker
	}
	// Public feature asset requests must use the same object store that received
	// uploaded builds. e.store is the in-memory media fake used by tests that do
	// not inject an external object store.
	builds := configuredBuildService(t, e.pool, e.objectStore, 8<<20)
	mediaHandler := hmedia.NewHandler(e.media, log, 20<<20)
	if err := mediaHandler.SetRedirectCacheTTL(time.Hour); err != nil {
		t.Fatal(err)
	}
	mediaHandler.SetProfileProvider(e.snapshots)
	snapshotHandler := hsitebuilder.NewSnapshotsHandler(e.snapshots, e.forms, log, 1<<20)
	if err := snapshotHandler.SetPagination(50, 200); err != nil {
		t.Fatal(err)
	}
	server.Config.Handler = httptransport.NewPublicRouter(httptransport.Dependencies{Log: log, Media: mediaHandler, SiteSnapshots: snapshotHandler, FeatureBuilds: hsitebuilder.NewBuildsHandler(builds, log, 8<<20), BranchIndexable: map[environment.Environment]bool{env: branch.Indexable}, Prerender: workers, MaxJSONBytes: 1 << 20, FormRateLimit: 100, FormRateWindow: time.Minute, FormRateMaxClients: 1000, FormRateSweepInterval: time.Minute, ReadinessTimeout: 3 * time.Second, RuntimeAssetCacheTTL: time.Hour}, env)
	server.Start()
	t.Cleanup(server.Close)
	return server, worker
}

func seoGet(t *testing.T, server *httptest.Server, path string) (int, string, http.Header) {
	t.Helper()
	response, err := http.Get(server.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, string(data), response.Header
}
