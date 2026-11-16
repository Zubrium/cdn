package objectstore

import (
	"context"
	"io"
	"net"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Zubrium/cdn/core/internal/testsupport/s3mock"
	"github.com/google/uuid"
)

func TestDisposableS3RoundTrip(t *testing.T) {
	endpoint := os.Getenv("TEST_CORE_S3_ENDPOINT")
	bucket := os.Getenv("TEST_CORE_S3_BUCKET")
	accessKey := os.Getenv("TEST_CORE_S3_ACCESS_KEY")
	secretKey := os.Getenv("TEST_CORE_S3_SECRET_KEY")
	if endpoint == "" && bucket == "" && accessKey == "" && secretKey == "" {
		server := s3mock.New()
		t.Cleanup(server.Close)
		endpoint, bucket, accessKey, secretKey = server.URL(), "zubrium-core-qa-objectstore-unit", "test-access", "test-secret"
	} else if endpoint == "" || bucket == "" || accessKey == "" || secretKey == "" {
		t.Fatal("configure all TEST_CORE_S3_* values or leave them all unset to use the isolated S3 test server")
	} else if !strings.HasPrefix(bucket, "zubrium-core-qa-") {
		t.Fatal("refusing bucket without disposable zubrium-core-qa- prefix")
	}
	assertIsolatedEndpoint(t, endpoint)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s, err := NewS3(ctx, S3Config{Endpoint: endpoint, Region: "us-east-1", Bucket: bucket, AccessKey: accessKey, SecretKey: secretKey, PathStyle: true, AllowInsecureHTTP: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RequirePrivate(ctx); err != nil {
		t.Fatal(err)
	}
	id, err := uuid.NewRandom()
	if err != nil {
		t.Fatal(err)
	}
	key := id.String()
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := s.Delete(cleanup, key); err != nil {
			t.Errorf("remove disposable object: %v", err)
		}
	})
	if err := s.Put(ctx, key, strings.NewReader("hello"), 5, "text/plain"); err != nil {
		t.Fatal(err)
	}
	body, err := s.Read(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	contents, err := io.ReadAll(body)
	_ = body.Close()
	if err != nil || string(contents) != "hello" {
		t.Fatalf("round trip: %q %v", contents, err)
	}
	if err := s.Put(ctx, key, strings.NewReader("too long"), 3, "text/plain"); err == nil {
		t.Fatal("overlong input accepted")
	}
	if err := s.Put(ctx, key, strings.NewReader("x"), 3, "text/plain"); err == nil {
		t.Fatal("short input accepted")
	}
}

func assertIsolatedEndpoint(t *testing.T, endpoint string) {
	t.Helper()
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Hostname() == "" {
		t.Fatalf("invalid disposable S3 endpoint")
	}
	if parsed.Scheme == "https" {
		return
	}
	ip := net.ParseIP(parsed.Hostname())
	if parsed.Scheme != "http" || (parsed.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback())) {
		t.Fatal("refusing non-loopback HTTP S3 endpoint for a test carrying credentials")
	}
}
