package integration

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/google/uuid"

	"websiteengine/features/sitebuilder"

	"websiteengine/pkg/storage"
)

type sitepackFailAfterPutStore struct {
	*fakeStore
	failPrefix string
}

func (s *sitepackFailAfterPutStore) Put(ctx context.Context, key string, body io.Reader, size int64, options storage.PutOptions) error {
	if err := s.fakeStore.Put(ctx, key, body, size, options); err != nil {
		return err
	}
	if strings.HasPrefix(key, s.failPrefix) {
		return errors.New("forced sitepack storage confirmation failure")
	}
	return nil
}

func rewriteSitepackIndex(t *testing.T, source []byte, mutate func(map[string]any)) []byte {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(source), int64(len(source)))
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	for _, file := range reader.File {
		stream, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		contents, err := io.ReadAll(stream)
		_ = stream.Close()
		if err != nil {
			t.Fatal(err)
		}
		if file.Name == "sitepack.json" {
			var index map[string]any
			if err := json.Unmarshal(contents, &index); err != nil {
				t.Fatal(err)
			}
			mutate(index)
			contents, err = json.Marshal(index)
			if err != nil {
				t.Fatal(err)
			}
		}
		entry, err := writer.Create(file.Name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(contents); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func rewriteSitepackEntryName(t *testing.T, source []byte, oldName, newName string) []byte {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(source), int64(len(source)))
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	for _, file := range reader.File {
		stream, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		contents, err := io.ReadAll(stream)
		_ = stream.Close()
		if err != nil {
			t.Fatal(err)
		}
		name := file.Name
		if name == oldName {
			name = newName
		}
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(contents); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func rewriteSitepackBuildABI(t *testing.T, source []byte, abi string) []byte {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(source), int64(len(source)))
	if err != nil {
		t.Fatal(err)
	}
	contents := make(map[string][]byte, len(reader.File))
	var index map[string]any
	for _, file := range reader.File {
		stream, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(stream)
		_ = stream.Close()
		if err != nil {
			t.Fatal(err)
		}
		contents[file.Name] = data
		if file.Name == "sitepack.json" {
			if err := json.Unmarshal(data, &index); err != nil {
				t.Fatal(err)
			}
		}
	}
	if index == nil {
		t.Fatal("sitepack index is missing")
	}
	build := index["builds"].([]any)[0].(map[string]any)
	oldPath := build["path"].(string)
	archiveReader, err := zip.NewReader(bytes.NewReader(contents[oldPath]), int64(len(contents[oldPath])))
	if err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	archiveWriter := zip.NewWriter(&archive)
	for _, file := range archiveReader.File {
		stream, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(stream)
		_ = stream.Close()
		if err != nil {
			t.Fatal(err)
		}
		if file.Name == "manifest.json" {
			var manifest map[string]any
			if err := json.Unmarshal(data, &manifest); err != nil {
				t.Fatal(err)
			}
			manifest["engineApi"] = abi
			data, err = json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
		}
		entry, err := archiveWriter.Create(file.Name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := archiveWriter.Close(); err != nil {
		t.Fatal(err)
	}
	archiveSum := sha256.Sum256(archive.Bytes())
	newDigest := hex.EncodeToString(archiveSum[:])
	newPath := "builds/" + newDigest + ".zip"
	build["path"], build["size"], build["sha256"] = newPath, float64(archive.Len()), newDigest
	indexBytes, err := json.Marshal(index)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	for _, file := range reader.File {
		name := file.Name
		data := contents[name]
		if name == "sitepack.json" {
			data = indexBytes
		} else if name == oldPath {
			name, data = newPath, archive.Bytes()
		}
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func TestSitepackRejectsConflictingFeatureVersionBeforeStorageWrites(t *testing.T) {
	source := newTestEnv(t)
	ctx := context.Background()
	sourceActor := testActor(t, source)
	manifest := testFeatureManifestFor("sitepack-version-conflict", "1.0.0")
	uploadFeatureBuild(t, source, manifest, "export default function Feature() { return 'source'; }")
	raw, _ := json.Marshal(singlePageConfig("sitepack-version-conflict", "1.0.0"))
	config, err := sitebuilder.ParseSiteConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := source.snapshots.CreateSnapshot(ctx, sourceActor, sitebuilder.CreateSnapshotInput{Name: "Version conflict source", Config: &config})
	if err != nil {
		t.Fatal(err)
	}
	options := sitebuilder.SitepackOptions{TempDir: t.TempDir(), MaxBytes: 16 << 20, MaxExpandedBytes: 32 << 20, MaxEntries: 100, MaxIndexBytes: 1 << 20, MaxSVGBytes: 1 << 20, MaxBrowserBlobBytes: 1 << 20}
	var output bytes.Buffer
	if err := source.snapshots.ExportSitepack(ctx, snapshot.ID, &output, options); err != nil {
		t.Fatal(err)
	}

	// Reuse this test's one disposable database sequentially. Close every source
	// connection before newTestEnv resets it for the target-side rejection
	// assertions; production cross-install coverage uses a separate test.
	source.server.Close()
	source.devServer.Close()
	source.prodServer.Close()
	source.pool.Close()
	target := newTestEnv(t)
	targetActor := testActor(t, target)
	uploadFeatureBuild(t, target, manifest, "export default function Feature() { return 'different bytes'; }")
	objectsBefore := len(target.store.objects)
	if _, err := target.snapshots.ImportSitepack(ctx, targetActor, bytes.NewReader(output.Bytes()), options); err == nil {
		t.Fatal("same feature version with a different ZIP digest was accepted")
	}
	if len(target.store.objects) != objectsBefore {
		t.Fatal("conflicting feature version wrote archive objects to storage")
	}
	var snapshots, builds int
	if err := target.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM site_snapshots),(SELECT count(*) FROM feature_builds)`).Scan(&snapshots, &builds); err != nil {
		t.Fatal(err)
	}
	if snapshots != 0 || builds != 1 {
		t.Fatalf("version conflict was not atomic: snapshots=%d builds=%d", snapshots, builds)
	}
}

func TestSitepackDoesNotExportPrivateServiceFiles(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	actor := testActor(t, e)
	manifest := testFeatureManifestFor("sitepack-private-file", "1.0.0")
	manifest["arguments"] = map[string]any{
		"type":                 "object",
		"properties":           map[string]any{"image": map[string]any{"type": "string", "format": "media"}},
		"additionalProperties": false,
	}
	uploadFeatureBuild(t, e, manifest, "export default function Feature() {}")
	ownerID, fileID := uuid.New(), uuid.New()
	if _, err := e.pool.Exec(ctx, `INSERT INTO file_owners(id,kind,name,enabled) VALUES($1,'service',$2,true)`, ownerID, "private-file-test-"+ownerID.String()); err != nil {
		t.Fatal(err)
	}
	data := []byte("private service asset")
	sum := sha256.Sum256(data)
	key := "service-private/" + fileID.String()
	e.store.objects[key] = data
	e.store.types[key] = "application/octet-stream"
	if _, err := e.pool.Exec(ctx, `INSERT INTO media_objects(id,owner_id,visibility,object_key,original_filename,content_type,size_bytes,status,variants,sha256) VALUES($1,$2,'private',$3,'private.bin','application/octet-stream',$4,'ready','{}',$5)`, fileID, ownerID, key, len(data), hex.EncodeToString(sum[:])); err != nil {
		t.Fatal(err)
	}
	configMap := singlePageConfig("sitepack-private-file", "1.0.0")
	page := configMap["pages"].([]any)[0].(map[string]any)
	placement := page["placements"].([]any)[0].(map[string]any)
	placement["arguments"] = map[string]any{"image": map[string]any{"source": "literal", "value": fileID.String()}}
	raw, _ := json.Marshal(configMap)
	config, err := sitebuilder.ParseSiteConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := e.snapshots.CreateSnapshot(ctx, actor, sitebuilder.CreateSnapshotInput{Name: "Private file source", Config: &config})
	if err != nil {
		t.Fatal(err)
	}
	options := sitebuilder.SitepackOptions{TempDir: t.TempDir(), MaxBytes: 16 << 20, MaxExpandedBytes: 32 << 20, MaxEntries: 100, MaxIndexBytes: 1 << 20, MaxSVGBytes: 1 << 20, MaxBrowserBlobBytes: 1 << 20}
	var output bytes.Buffer
	if err := e.snapshots.ExportSitepack(ctx, snapshot.ID, &output, options); err == nil || !errors.Is(err, sitebuilder.ErrInvalid) {
		t.Fatalf("private service file should not be exportable by admin: %v", err)
	}
	if _, err := e.pool.Exec(ctx, `UPDATE media_objects SET visibility='public' WHERE id=$1`, fileID); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if err := e.snapshots.ExportSitepack(ctx, snapshot.ID, &output, options); err != nil {
		t.Fatalf("public service file should be exportable: %v", err)
	}
}
