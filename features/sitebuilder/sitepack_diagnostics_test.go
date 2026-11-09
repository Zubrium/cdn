package sitebuilder

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/google/uuid"
	"websiteengine/pkg/storage"
)

type sitepackWriteCounter struct{ puts int }

func (s *sitepackWriteCounter) Put(context.Context, string, io.Reader, int64, storage.PutOptions) error {
	s.puts++
	return nil
}
func (*sitepackWriteCounter) Get(context.Context, string) (io.ReadCloser, error) {
	return nil, errors.New("unused")
}
func (*sitepackWriteCounter) Delete(context.Context, string) error { return nil }
func (*sitepackWriteCounter) PublicURL(string) (string, error)     { return "", nil }
func (*sitepackWriteCounter) Ready(context.Context) error          { return nil }

func TestSitepackPreflightFailureCreatesNoSnapshotOrObjects(t *testing.T) {
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	entry, err := writer.Create("sitepack.json")
	if err != nil {
		t.Fatal(err)
	}
	// Malformed JSON contains a canary that must never be copied to the API
	// diagnostic. Rejection happens before build registration, SQL, or storage.
	if _, err := io.WriteString(entry, `{"formatVersion":1,"name":"ok","apiKey":"TEST_SECRET_CANARY"`); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	store := &sitepackWriteCounter{}
	service := NewSnapshotService(nil, &BuildService{store: store, maxArchiveBytes: 1024})
	options := SitepackOptions{TempDir: t.TempDir(), MaxBytes: 4096, MaxExpandedBytes: 8192, MaxEntries: 8, MaxIndexBytes: 4096, MaxSVGBytes: 1024, MaxBrowserBlobBytes: 1024}

	report, err := service.ImportSitepack(context.Background(), uuid.New(), bytes.NewReader(archive.Bytes()), options)
	var failure *SitepackImportFailure
	if !errors.As(err, &failure) {
		t.Fatalf("expected controlled preflight rejection, got %v", err)
	}
	if failure.Diagnostic.Path != "sitepack.json" || failure.Diagnostic.Code != "unsupported_index" {
		t.Fatalf("unexpected safe diagnostic: %#v", failure.Diagnostic)
	}
	encoded, marshalErr := json.Marshal(struct {
		Report SitepackReport
		Error  SitepackImportDiagnostic
	}{report, failure.Diagnostic})
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if strings.Contains(string(encoded), "TEST_SECRET_CANARY") {
		t.Fatal("diagnostic leaked archive content")
	}
	if report.Snapshot.ID != uuid.Nil || report.NewBuilds != 0 || report.ReusedBuilds != 0 || report.NewFiles != 0 || report.ReusedFiles != 0 || len(report.Items) != 0 {
		t.Fatalf("rejected preflight reported a partial import: %#v", report)
	}
	if store.puts != 0 {
		t.Fatalf("preflight failure wrote %d object(s)", store.puts)
	}
}

func TestSitepackReportItemsDescribeModelIdentityRemapsWithoutValues(t *testing.T) {
	remap := sitepackModelIDRemap{
		entities:  map[string]string{uuid.NewString(): uuid.NewString()},
		fields:    map[string]string{uuid.NewString(): uuid.NewString(), uuid.NewString(): uuid.NewString()},
		relations: map[string]string{uuid.NewString(): uuid.NewString()},
		records:   map[string]string{uuid.NewString(): uuid.NewString(), uuid.NewString(): uuid.NewString()},
	}
	report := SitepackReport{}
	addSitepackModelReportItems(&report, remap)
	if len(report.Items) != 4 || report.Items[0].Kind != "model_entity" || report.Items[0].Count != 1 ||
		report.Items[1].Kind != "model_field" || report.Items[1].Count != 2 ||
		report.Items[2].Kind != "model_relation" || report.Items[2].Count != 1 ||
		report.Items[3].Kind != "model_record" || report.Items[3].Count != 2 {
		t.Fatalf("model identity remaps missing: %#v", report.Items)
	}
	encoded, err := json.Marshal(report.Items)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "SourceID") || strings.Contains(string(encoded), "TargetID") {
		t.Fatal("success report exposed internal IDs")
	}
}

func TestSitepackReportItemListIsBounded(t *testing.T) {
	report := SitepackReport{}
	report.FileIDs = map[string]string{}
	for i := 0; i < maxSitepackReportItems+5; i++ {
		addSitepackReportItem(&report, SitepackReportItem{Kind: "file", Path: "files[0]", Action: "imported", Message: "ok"})
		addSitepackReportFileID(&report, uuid.NewString(), uuid.NewString())
	}
	if len(report.Items) != maxSitepackReportItems || !report.ItemsTruncated || len(report.FileIDs) != maxSitepackReportItems || !report.FileIDsTruncated {
		t.Fatalf("report is not bounded: items=%d itemsTruncated=%v files=%d filesTruncated=%v", len(report.Items), report.ItemsTruncated, len(report.FileIDs), report.FileIDsTruncated)
	}
}
