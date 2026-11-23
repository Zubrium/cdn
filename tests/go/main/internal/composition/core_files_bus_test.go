package composition

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/Zubrium/cdn/main/internal/bus"
	"github.com/Zubrium/cdn/pkg/contracts"
	"github.com/Zubrium/cdn/workers/prerender"
)

type verifiedFileCreatorFunc func(context.Context, string, []string, map[string]string, string, int64, string, io.Reader) (string, error)

func (creator verifiedFileCreatorFunc) CreateFileVerified(ctx context.Context, requestKey string, groups []string, metadata map[string]string, contentType string, size int64, checksum string, body io.Reader) (string, error) {
	return creator(ctx, requestKey, groups, metadata, contentType, size, checksum, body)
}

func (verifiedFileCreatorFunc) StatFile(context.Context, string) (contracts.File, error) {
	return contracts.File{}, nil
}
func (verifiedFileCreatorFunc) ReadFile(context.Context, string) (io.ReadCloser, contracts.File, error) {
	return nil, contracts.File{}, nil
}
func (verifiedFileCreatorFunc) DeleteFile(context.Context, string, int64) error { return nil }
func (verifiedFileCreatorFunc) GetManagedFileSet(context.Context, string) (contracts.FileSet, error) {
	return contracts.FileSet{}, nil
}
func (verifiedFileCreatorFunc) ListManagedFileSets(context.Context, string, string, string, int) ([]contracts.FileSet, string, error) {
	return nil, "", nil
}
func (verifiedFileCreatorFunc) ListFileSet(context.Context, string, string, int) ([]contracts.File, string, error) {
	return nil, "", nil
}
func (verifiedFileCreatorFunc) AddFileToManagedSet(context.Context, string, string, int64) (contracts.FileSet, error) {
	return contracts.FileSet{}, nil
}
func (verifiedFileCreatorFunc) RemoveFileFromManagedSet(context.Context, string, string, int64) (contracts.FileSet, error) {
	return contracts.FileSet{}, nil
}

func TestPrerenderArtifactWriterCallsCoreThroughScopedDataBus(t *testing.T) {
	dispatcher, err := bus.NewAllowlisted(contracts.FileCreateVerifiedOperation().Key)
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.EnableScopedStreams(); err != nil {
		t.Fatal(err)
	}
	core := &coreComponent{fileServices: map[string]contracts.CoreFiles{
		prerender.DataModuleID: verifiedFileCreatorFunc(func(ctx context.Context, key string, groups []string, metadata map[string]string, contentType string, size int64, checksum string, body io.Reader) (string, error) {
			if bus.ModuleIDFromContext(ctx) != prerender.DataModuleID {
				t.Fatalf("module scope = %q", bus.ModuleIDFromContext(ctx))
			}
			if key == "" || len(groups) != 1 || groups[0] != "features" || metadata["artifact_path"] != "index.html" || metadata["artifact_kind"] != "website_prerender" || contentType != "text/html" || size != 4 || checksum != "sha256" {
				t.Fatalf("unexpected verified file request: %q %#v %#v %q %d %q", key, groups, metadata, contentType, size, checksum)
			}
			data, err := io.ReadAll(body)
			if err != nil {
				return "", err
			}
			if string(data) != "page" {
				t.Fatalf("artifact body = %q", data)
			}
			return "artifact-file", nil
		}),
	}}
	if err := bus.RegisterFileCreateVerified(dispatcher, core.coreFileCreator); err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Seal(); err != nil {
		t.Fatal(err)
	}
	writer := corePrerenderArtifactWriter{dispatcher: bus.NewModuleDispatcher(dispatcher, prerender.DataModuleID)}
	fileID, err := writer.WriteArtifact(context.Background(), "task-1", "index.html", "text/html", 4, "sha256", strings.NewReader("page"))
	if err != nil {
		t.Fatal(err)
	}
	if fileID != "artifact-file" {
		t.Fatalf("artifact file ID = %q", fileID)
	}
}
