package objectstore

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRejectsIncompleteOrUnsafeConfiguration(t *testing.T) {
	valid := S3Config{Endpoint: "http://localhost:9000", Region: "test", Bucket: "test", AccessKey: "test", SecretKey: "test", AllowInsecureHTTP: true}
	cases := []S3Config{
		{},
		{Endpoint: "http://localhost:9000", Region: "test", Bucket: "test", AccessKey: "test", SecretKey: "test"},
		{Endpoint: "ftp://localhost", Region: "test", Bucket: "test", AccessKey: "test", SecretKey: "test"},
		{Endpoint: "https://user:password@example.test", Region: "test", Bucket: "test", AccessKey: "test", SecretKey: "test"},
		{Endpoint: "http://storage.internal:9000", Region: "test", Bucket: "test", AccessKey: "test", SecretKey: "test", AllowInsecureHTTP: true},
	}
	for _, cfg := range cases {
		if _, err := NewS3(context.Background(), cfg); err == nil {
			t.Fatalf("accepted invalid configuration: %+v", cfg.Endpoint)
		}
	}
	client, err := NewS3(context.Background(), valid)
	if err != nil || client == nil {
		t.Fatalf("valid config: %v", err)
	}
	if _, err := client.Read(context.Background(), ""); err == nil {
		t.Fatal("accepted empty key")
	}
	if err := client.Delete(context.Background(), ""); err == nil {
		t.Fatal("accepted empty key")
	}
	if err := client.Put(context.Background(), "", nil, -1, ""); err == nil {
		t.Fatal("accepted invalid upload")
	}
}

func TestObjectKeysMustBeInternalUUIDsBeforeNetworkAccess(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusNotFound)
		_, _ = fmt.Fprint(w, `<Error><Code>NoSuchKey</Code><Message>missing</Message></Error>`)
	}))
	defer server.Close()
	client, err := NewS3(context.Background(), S3Config{Endpoint: server.URL, Region: "test", Bucket: "private", AccessKey: "test", SecretKey: "test", PathStyle: true, AllowInsecureHTTP: true})
	if err != nil {
		t.Fatal(err)
	}
	var accepted []string
	for _, key := range []string{"../../outside", "not-a-uuid", "00000000-0000-0000-0000-000000000000", "AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA"} {
		if _, err := client.Read(context.Background(), key); err == nil {
			accepted = append(accepted, "read "+key)
		}
		if _, err := client.ReadRange(context.Background(), key, 0, 0); err == nil {
			accepted = append(accepted, "range read "+key)
		}
		if err := client.Put(context.Background(), key, strings.NewReader("x"), 1, "text/plain"); err == nil {
			accepted = append(accepted, "put "+key)
		}
		if err := client.Delete(context.Background(), key); err == nil {
			accepted = append(accepted, "delete "+key)
		}
	}
	if got := requests.Load(); got != 0 || len(accepted) != 0 {
		t.Fatalf("invalid internal keys reached object storage %d times or were accepted: %v", got, accepted)
	}
}

func TestPutObjectRejectsPayloadAboveSingleRequestLimitBeforeNetwork(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	client, err := NewS3(context.Background(), S3Config{Endpoint: server.URL, Region: "test", Bucket: "private", AccessKey: "test", SecretKey: "test", PathStyle: true, AllowInsecureHTTP: true})
	if err != nil {
		t.Fatal(err)
	}
	err = client.Put(context.Background(), "00000000-0000-4000-8000-000000000001", strings.NewReader(""), MaxPutObjectBytes+1, "application/octet-stream")
	if err == nil || requests.Load() != 0 {
		t.Fatalf("oversized single-request upload reached S3: requests=%d err=%v", requests.Load(), err)
	}
}

func TestRequirePrivateRejectsVersionedBucketPublicACLPolicyAndUnverifiablePolicy(t *testing.T) {
	var versionedBucket, publicACL, publicPolicy, unavailablePolicy, publicAccessBlockDisabled atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		switch {
		case r.URL.Query().Has("publicAccessBlock"):
			if publicAccessBlockDisabled.Load() {
				_, _ = fmt.Fprint(w, `<PublicAccessBlockConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><BlockPublicAcls>true</BlockPublicAcls><IgnorePublicAcls>false</IgnorePublicAcls><BlockPublicPolicy>true</BlockPublicPolicy><RestrictPublicBuckets>true</RestrictPublicBuckets></PublicAccessBlockConfiguration>`)
				return
			}
			_, _ = fmt.Fprint(w, `<PublicAccessBlockConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><BlockPublicAcls>true</BlockPublicAcls><IgnorePublicAcls>true</IgnorePublicAcls><BlockPublicPolicy>true</BlockPublicPolicy><RestrictPublicBuckets>true</RestrictPublicBuckets></PublicAccessBlockConfiguration>`)
		case r.URL.Query().Has("versioning"):
			if versionedBucket.Load() {
				_, _ = fmt.Fprint(w, `<VersioningConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Status>Enabled</Status></VersioningConfiguration>`)
				return
			}
			_, _ = fmt.Fprint(w, `<VersioningConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/"></VersioningConfiguration>`)
		case r.URL.Query().Has("acl"):
			if publicACL.Load() {
				_, _ = fmt.Fprint(w, `<AccessControlPolicy xmlns="http://s3.amazonaws.com/doc/2006-03-01/" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"><Owner><ID>owner</ID></Owner><AccessControlList><Grant><Grantee xsi:type="Group"><URI>http://acs.amazonaws.com/groups/global/AllUsers</URI></Grantee><Permission>READ</Permission></Grant></AccessControlList></AccessControlPolicy>`)
				return
			}
			_, _ = fmt.Fprint(w, `<AccessControlPolicy xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Owner><ID>owner</ID></Owner><AccessControlList><Grant><Grantee><ID>owner</ID></Grantee><Permission>FULL_CONTROL</Permission></Grant></AccessControlList></AccessControlPolicy>`)
		case r.URL.Query().Has("policyStatus"):
			if unavailablePolicy.Load() {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = fmt.Fprint(w, `<Error><Code>InternalError</Code><Message>not verifiable</Message></Error>`)
				return
			}
			_, _ = fmt.Fprintf(w, `<PolicyStatus xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><IsPublic>%t</IsPublic></PolicyStatus>`, publicPolicy.Load())
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer server.Close()
	client, err := NewS3(context.Background(), S3Config{Endpoint: server.URL, Region: "test", Bucket: "private", AccessKey: "test", SecretKey: "test", PathStyle: true, AllowInsecureHTTP: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.RequirePrivate(context.Background()); err != nil {
		t.Fatalf("private ACL and policy rejected: %v", err)
	}
	publicAccessBlockDisabled.Store(true)
	if err := client.RequirePrivate(context.Background()); err == nil {
		t.Fatal("bucket without complete public access blocking was accepted")
	}
	publicAccessBlockDisabled.Store(false)
	versionedBucket.Store(true)
	if err := client.RequirePrivate(context.Background()); err == nil {
		t.Fatal("versioned bucket accepted despite permanent delete contract")
	}
	versionedBucket.Store(false)
	publicACL.Store(true)
	if err := client.RequirePrivate(context.Background()); err == nil {
		t.Fatal("public ACL accepted")
	}
	publicACL.Store(false)
	publicPolicy.Store(true)
	if err := client.RequirePrivate(context.Background()); err == nil {
		t.Fatal("public bucket policy accepted")
	}
	publicPolicy.Store(false)
	unavailablePolicy.Store(true)
	if err := client.RequirePrivate(context.Background()); err == nil {
		t.Fatal("bucket access passed when policy could not be verified")
	}
}
