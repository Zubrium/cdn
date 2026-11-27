//go:build website_acceptance || website_orchestration_acceptance || publication_delivery_acceptance

package main

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	core "github.com/Zubrium/cdn/core/presentation"
)

type websitePersistenceEnv struct {
	runID          string
	runtimeDSN     string
	migrationDSN   string
	s3Endpoint     string
	s3Bucket       string
	s3AccessKey    string
	s3SecretKey    string
	insecureS3HTTP bool
}

func websitePersistenceEnvironment(t *testing.T) websitePersistenceEnv {
	t.Helper()
	// Core Start deliberately separates runtime DML and Atlas DDL identities on
	// the same disposable database, so both DSNs are explicit run inputs.
	names := []string{
		"WEBSITE_TEST_DISPOSABLE",
		"WEBSITE_TEST_RUN_ID",
		"WEBSITE_TEST_POSTGRES_DSN",
		"WEBSITE_TEST_POSTGRES_MIGRATION_DSN",
		"WEBSITE_TEST_S3_BUCKET",
	}
	if os.Getenv("WEBSITE_TEST_S3_MOCK") != "1" {
		names = append(names,
			"WEBSITE_TEST_S3_ENDPOINT",
			"WEBSITE_TEST_S3_ACCESS_KEY",
			"WEBSITE_TEST_S3_SECRET_KEY",
		)
	}
	missing := make([]string, 0, len(names))
	values := make(map[string]string, len(names))
	for _, name := range names {
		value := strings.TrimSpace(os.Getenv(name))
		if value == "" {
			missing = append(missing, name)
			continue
		}
		values[name] = value
	}
	if len(missing) != 0 {
		t.Fatalf("missing required disposable WEB-018 environment: %s", strings.Join(missing, ", "))
	}
	if values["WEBSITE_TEST_DISPOSABLE"] != "1" {
		t.Fatal("WEBSITE_TEST_DISPOSABLE must equal 1 for WEB-018")
	}
	runID := values["WEBSITE_TEST_RUN_ID"]
	if !websiteRunIDPattern.MatchString(runID) {
		t.Fatal("WEBSITE_TEST_RUN_ID must use only A-Za-z0-9._-")
	}

	runtimeDSN := assertDisposableDatabase(t, "WEBSITE_TEST_POSTGRES_DSN", values["WEBSITE_TEST_POSTGRES_DSN"])
	migrationDSN := assertDisposableDatabase(t, "WEBSITE_TEST_POSTGRES_MIGRATION_DSN", values["WEBSITE_TEST_POSTGRES_MIGRATION_DSN"])
	if err := core.ValidateDatabaseURLs(values["WEBSITE_TEST_POSTGRES_DSN"], values["WEBSITE_TEST_POSTGRES_MIGRATION_DSN"]); err != nil {
		t.Fatalf("Core runtime and Atlas migration DSNs must use separate users on the same disposable database: %v", err)
	}
	if runtimeDSN.Hostname() != migrationDSN.Hostname() || runtimeDSN.Port() != migrationDSN.Port() {
		t.Fatal("Core runtime and Atlas migration DSNs must target the same PostgreSQL server")
	}

	bucket := values["WEBSITE_TEST_S3_BUCKET"]
	if !hasWebsitePrefix(bucket, "zubrium-core-qa-", "zubrium-e2e-") {
		t.Fatalf("refusing S3 bucket without a disposable WEB-018 prefix: %q", bucket)
	}
	insecureS3HTTP := false
	if os.Getenv("WEBSITE_TEST_S3_MOCK") != "1" {
		endpoint, err := url.Parse(values["WEBSITE_TEST_S3_ENDPOINT"])
		if err != nil || endpoint.Hostname() == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") {
			t.Fatalf("invalid disposable S3 endpoint: %q", values["WEBSITE_TEST_S3_ENDPOINT"])
		}
		host := endpoint.Hostname()
		ip := net.ParseIP(host)
		if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			t.Fatalf("refusing non-loopback S3 endpoint for WEB-018: %q", host)
		}
		insecureS3HTTP = endpoint.Scheme == "http"
	}

	return websitePersistenceEnv{
		runID:          runID,
		runtimeDSN:     values["WEBSITE_TEST_POSTGRES_DSN"],
		migrationDSN:   values["WEBSITE_TEST_POSTGRES_MIGRATION_DSN"],
		s3Endpoint:     values["WEBSITE_TEST_S3_ENDPOINT"],
		s3Bucket:       bucket,
		s3AccessKey:    values["WEBSITE_TEST_S3_ACCESS_KEY"],
		s3SecretKey:    values["WEBSITE_TEST_S3_SECRET_KEY"],
		insecureS3HTTP: insecureS3HTTP,
	}
}

var websiteRunIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func assertDisposableDatabase(t *testing.T, name, dsn string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(dsn)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") || parsed.Hostname() == "" {
		t.Fatalf("%s must be a URL-form PostgreSQL DSN", name)
	}
	database := strings.TrimPrefix(parsed.Path, "/")
	if !hasWebsitePrefix(database, "zubrium_core_qa_", "zubrium_e2e_") {
		t.Fatalf("refusing non-disposable database from %s: %q", name, database)
	}
	return parsed
}

func hasWebsitePrefix(value string, prefixes ...string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func newWebsitePersistenceBootstrapSecret(t *testing.T) string {
	t.Helper()
	runID := strings.TrimSpace(os.Getenv("WEBSITE_TEST_RUN_ID"))
	if runID == "" {
		t.Fatal("WEBSITE_TEST_RUN_ID is required for the disposable Core bootstrap identity")
	}
	return websiteRunBootstrapSecret(runID)
}

func websiteRuntimeConfig(env websitePersistenceEnv, bootstrapSecret string) core.RuntimeConfig {
	atlas := strings.TrimSpace(os.Getenv("TEST_ATLAS_BINARY"))
	if atlas == "" {
		atlas = "atlas"
	}
	return core.RuntimeConfig{
		Database: core.DatabaseConfig{
			URL: env.runtimeDSN, MinConnections: 1, MaxConnections: 5,
			ConnectTimeout: 5 * time.Second, MaxLifetime: time.Hour,
			MaxIdleTime: time.Minute, HealthInterval: time.Second,
		},
		MigrationDatabaseURL: env.migrationDSN,
		S3: core.S3Config{
			Endpoint: env.s3Endpoint, Region: "us-east-1", Bucket: env.s3Bucket,
			AccessKey: env.s3AccessKey, SecretKey: env.s3SecretKey,
			PathStyle: true, AllowInsecureHTTP: env.insecureS3HTTP,
		},
		BootstrapSecret:      bootstrapSecret,
		AtlasBinary:          atlas,
		MigrationLockTimeout: 30 * time.Second,
		AccessGroups: map[string]core.AccessLevel{
			"features": core.AccessLevelWrite,
		},
		CacheMaxBytes:        1 << 20,
		CacheTTL:             time.Minute,
		MaxMetadataBytes:     64 << 10,
		MaxObjectBytes:       16 << 20,
		MaxCachedObjectBytes: 8,
		MaintenanceInterval:  time.Hour,
		MaintenanceTimeout:   10 * time.Second,
		PendingTTL:           time.Hour,
		MaintenanceBatchSize: 100,
		SessionIdleTTL:       8 * time.Hour,
		SessionAbsoluteTTL:   7 * 24 * time.Hour,
		ModuleData: core.ModuleDataLimits{
			MaxPayloadBytes: 1 << 20, MaxIndexBytes: 1 << 16, MaxBatchBytes: 8 << 20,
			MaxPageBytes: 3 << 20, MaxSelectors: 16, MaxSelectorValues: 64,
			MaxPageSize: 100, MaxBatchOperations: 64,
		},
	}
}

func websitePrivateS3Handler(w http.ResponseWriter, r *http.Request) {
	const xmlns = `http://s3.amazonaws.com/doc/2006-03-01/`
	w.Header().Set("Content-Type", "application/xml")
	switch {
	case r.URL.Query().Has("publicAccessBlock"):
		_, _ = fmt.Fprintf(w, `<PublicAccessBlockConfiguration xmlns=%q><BlockPublicAcls>true</BlockPublicAcls><IgnorePublicAcls>true</IgnorePublicAcls><BlockPublicPolicy>true</BlockPublicPolicy><RestrictPublicBuckets>true</RestrictPublicBuckets></PublicAccessBlockConfiguration>`, xmlns)
	case r.URL.Query().Has("versioning"):
		_, _ = fmt.Fprintf(w, `<VersioningConfiguration xmlns=%q></VersioningConfiguration>`, xmlns)
	case r.URL.Query().Has("acl"):
		_, _ = fmt.Fprintf(w, `<AccessControlPolicy xmlns=%q><Owner><ID>test-owner</ID></Owner><AccessControlList><Grant><Grantee><ID>test-owner</ID></Grantee><Permission>FULL_CONTROL</Permission></Grant></AccessControlList></AccessControlPolicy>`, xmlns)
	case r.URL.Query().Has("policyStatus"):
		_, _ = fmt.Fprintf(w, `<PolicyStatus xmlns=%q><IsPublic>false</IsPublic></PolicyStatus>`, xmlns)
	default:
		http.Error(w, `<Error><Code>NotImplemented</Code></Error>`, http.StatusNotImplemented)
	}
}

// websiteProxyPrivateS3Checks supplies the immutable bucket-policy answers
// Core requires at startup while forwarding all object operations to the real
// disposable S3 stack. It is run-scoped and never changes bucket contents.
func websiteProxyPrivateS3Checks(t *testing.T, env *websitePersistenceEnv) {
	t.Helper()
	target, err := url.Parse(env.s3Endpoint)
	if err != nil || target.Scheme == "" || target.Host == "" {
		t.Fatalf("invalid disposable S3 endpoint: %q", env.s3Endpoint)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	privateCheck := func(r *http.Request) bool {
		if r.URL.Path != "/"+env.s3Bucket {
			return false
		}
		for _, key := range []string{"publicAccessBlock", "versioning", "acl", "policyStatus"} {
			if r.URL.Query().Has(key) {
				return true
			}
		}
		return false
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if privateCheck(r) {
			websitePrivateS3Handler(w, r)
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	env.s3Endpoint = server.URL
	env.insecureS3HTTP = true
}

// websiteRunBootstrapSecret keeps one run-scoped bootstrap identity across
// disposable integration scenarios that share the same PostgreSQL instance.
func websiteRunBootstrapSecret(runID string) string {
	digest := sha256.Sum256([]byte("web012-bootstrap-" + runID))
	return "ct_live_" + base64.RawURLEncoding.EncodeToString(digest[:])
}
