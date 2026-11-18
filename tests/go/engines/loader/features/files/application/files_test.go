package application

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	core "github.com/Zubrium/cdn/core/presentation"
	api "github.com/Zubrium/cdn/engines/loader/presentation"
)

type fileOperationsSpy struct {
	Files
	file               core.File
	set                core.FileSet
	searchQuery        [5]any
	setListQuery       [5]any
	setFileQuery       [4]any
	updatedMetadata    map[string]string
	updatedContentType string
	deletedFile        bool
	deletedSet         bool
	setAdds            int
	setRemoves         int
	failure            error
}

func TestLoaderModuleDataDescriptorDeclaresBoundedIndexFields(t *testing.T) {
	descriptor := ModuleDataDescriptor()
	if descriptor.ModuleID != "loader" || len(descriptor.Namespaces) != 1 {
		t.Fatalf("unexpected Loader data descriptor: %+v", descriptor)
	}
	namespace := descriptor.Namespaces[0]
	if namespace.Name != "loader" || !reflect.DeepEqual(namespace.ScopeTypes, []string{"global"}) {
		t.Fatalf("unexpected Loader module-data scope: %+v", namespace)
	}
	for _, field := range []string{"kind", "upload_id", "principal_id", "state"} {
		if _, ok := namespace.Fields[field]; !ok {
			t.Errorf("Loader index descriptor omitted %q", field)
		}
	}
}

func TestCleanupWorkerStopsAfterContextCancellation(t *testing.T) {
	service, parent, _, _, _ := newTestService(t)
	ctx, cancel := context.WithCancel(parent)
	workerErrors := service.RunCleanup(ctx)
	cancel()
	for err := range workerErrors {
		if err != nil {
			t.Errorf("empty cleanup pass returned an error during shutdown: %v", err)
		}
	}
}

func newFileOperationsSpy() *fileOperationsSpy {
	return &fileOperationsSpy{
		file: core.File{ID: "file-1", Groups: []string{"assets"}, Metadata: map[string]string{"name": "before.txt", "keep": "yes"},
			ContentType: "text/plain", SHA256: strings.Repeat("a", 64), Revision: 3, Size: 4},
		set: core.FileSet{ID: "set-1", Namespace: "features", Name: "initial", ManagementGroup: "assets", Revision: 1,
			CreatedAt: time.Unix(100, 0).UTC(), UpdatedAt: time.Unix(100, 0).UTC()},
	}
}

func (s *fileOperationsSpy) SearchFiles(_ context.Context, _ core.Session, query, group, cursor, setID string, limit int) ([]core.File, string, error) {
	s.searchQuery = [5]any{query, group, cursor, setID, limit}
	if s.failure != nil {
		return nil, "", s.failure
	}
	return []core.File{s.file}, "next-file", nil
}

func (s *fileOperationsSpy) StatFile(context.Context, core.Session, string) (core.File, error) {
	if s.failure != nil {
		return core.File{}, s.failure
	}
	return s.file, nil
}

func (s *fileOperationsSpy) ReadFile(context.Context, core.Session, string) (io.ReadCloser, core.File, error) {
	if s.failure != nil {
		return nil, core.File{}, s.failure
	}
	return io.NopCloser(strings.NewReader("data")), s.file, nil
}

func (s *fileOperationsSpy) ReadFileRange(context.Context, core.Session, string, int64, int64) (io.ReadCloser, core.File, error) {
	if s.failure != nil {
		return nil, core.File{}, s.failure
	}
	return io.NopCloser(strings.NewReader("at")), s.file, nil
}

func (s *fileOperationsSpy) UpdateFileWithContentType(_ context.Context, _ core.Session, _ string, expected int64, metadata map[string]string, contentType string) error {
	if s.failure != nil {
		return s.failure
	}
	if expected != s.file.Revision {
		return core.ErrRevisionConflict
	}
	s.updatedMetadata, s.updatedContentType = clone(metadata), contentType
	s.file.Metadata, s.file.ContentType = clone(metadata), contentType
	s.file.Revision++
	return nil
}

func (s *fileOperationsSpy) AddFileToGroup(_ context.Context, _ core.Session, _ string, group string, expected int64) error {
	if s.failure != nil {
		return s.failure
	}
	if expected != s.file.Revision {
		return core.ErrRevisionConflict
	}
	s.file.Groups = append(s.file.Groups, group)
	s.file.Revision++
	return nil
}

func (s *fileOperationsSpy) RemoveFileFromGroup(_ context.Context, _ core.Session, _ string, group string, expected int64) error {
	if s.failure != nil {
		return s.failure
	}
	if expected != s.file.Revision {
		return core.ErrRevisionConflict
	}
	for i, current := range s.file.Groups {
		if current == group {
			s.file.Groups = append(s.file.Groups[:i], s.file.Groups[i+1:]...)
			break
		}
	}
	s.file.Revision++
	return nil
}

func (s *fileOperationsSpy) DeleteFile(_ context.Context, _ core.Session, _ string, expected int64) error {
	if s.failure != nil {
		return s.failure
	}
	if expected != s.file.Revision {
		return core.ErrRevisionConflict
	}
	s.deletedFile = true
	return nil
}

func (s *fileOperationsSpy) CreateManagedFileSet(_ context.Context, _ core.Session, namespace, name, group string) (core.FileSet, error) {
	if s.failure != nil {
		return core.FileSet{}, s.failure
	}
	s.set.Namespace, s.set.Name, s.set.ManagementGroup = namespace, name, group
	s.set.Revision = 1
	return s.set, nil
}

func (s *fileOperationsSpy) GetManagedFileSet(context.Context, core.Session, string) (core.FileSet, error) {
	if s.failure != nil {
		return core.FileSet{}, s.failure
	}
	return s.set, nil
}

func (s *fileOperationsSpy) UpdateManagedFileSet(_ context.Context, _ core.Session, _ string, expected int64, name string) (core.FileSet, error) {
	if s.failure != nil {
		return core.FileSet{}, s.failure
	}
	if expected != s.set.Revision {
		return core.FileSet{}, core.ErrRevisionConflict
	}
	s.set.Name = name
	s.set.Revision++
	return s.set, nil
}

func (s *fileOperationsSpy) DeleteManagedFileSet(_ context.Context, _ core.Session, _ string, expected int64) error {
	if s.failure != nil {
		return s.failure
	}
	if expected != s.set.Revision {
		return core.ErrRevisionConflict
	}
	s.deletedSet = true
	return nil
}

func (s *fileOperationsSpy) AddFileToManagedSet(_ context.Context, _ core.Session, _, _ string, expected int64) (core.FileSet, error) {
	if s.failure != nil {
		return core.FileSet{}, s.failure
	}
	if expected != s.set.Revision {
		return core.FileSet{}, core.ErrRevisionConflict
	}
	s.setAdds++
	s.set.Revision++
	return s.set, nil
}

func (s *fileOperationsSpy) RemoveFileFromManagedSet(_ context.Context, _ core.Session, _, _ string, expected int64) (core.FileSet, error) {
	if s.failure != nil {
		return core.FileSet{}, s.failure
	}
	if expected != s.set.Revision {
		return core.FileSet{}, core.ErrRevisionConflict
	}
	s.setRemoves++
	s.set.Revision++
	return s.set, nil
}

func (s *fileOperationsSpy) ListManagedFileSets(_ context.Context, _ core.Session, namespace, query, cursor string, limit int) ([]core.FileSet, string, error) {
	s.setListQuery = [5]any{namespace, query, cursor, limit, ""}
	if s.failure != nil {
		return nil, "", s.failure
	}
	return []core.FileSet{s.set}, "next-set", nil
}

func (s *fileOperationsSpy) ListFileSet(_ context.Context, _ core.Session, setID, cursor string, limit int) ([]core.File, string, error) {
	s.setFileQuery = [4]any{setID, cursor, limit, ""}
	if s.failure != nil {
		return nil, "", s.failure
	}
	return []core.File{s.file}, "next-set-file", nil
}

func TestFileOperationsDelegateThroughAuthenticatedCoreSession(t *testing.T) {
	files := newFileOperationsSpy()
	service, ctx, _ := newTestServiceWithFiles(t, files, newTestData())

	page, err := service.ListFiles(ctx, api.ListQuery{Query: "logo", Group: "assets", Cursor: "cursor-1", Limit: 7})
	if err != nil || !reflect.DeepEqual(files.searchQuery, [5]any{"logo", "assets", "cursor-1", "", 7}) || page.NextCursor != "next-file" || len(page.Items) != 1 {
		t.Fatalf("file search delegation: query=%v page=%+v err=%v", files.searchQuery, page, err)
	}
	stat, err := service.StatFile(ctx, files.file.ID)
	if err != nil || stat.Name != "before.txt" || stat.Revision != files.file.Revision {
		t.Fatalf("file stat mapping: file=%+v err=%v", stat, err)
	}
	reader, readFile, err := service.ReadFile(ctx, files.file.ID)
	if err != nil {
		t.Fatal(err)
	}
	readBytes, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil || string(readBytes) != "data" || readFile.ID != files.file.ID {
		t.Fatalf("file stream mapping: bytes=%q file=%+v read=%v close=%v", readBytes, readFile, readErr, closeErr)
	}
	rangeReader, _, err := service.ReadFileRange(ctx, files.file.ID, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	rangeBytes, readErr := io.ReadAll(rangeReader)
	closeErr = rangeReader.Close()
	if readErr != nil || closeErr != nil || string(rangeBytes) != "at" {
		t.Fatalf("file range mapping: bytes=%q read=%v close=%v", rangeBytes, readErr, closeErr)
	}
	oldName, contentType, attributes := "after.txt", "text/markdown", map[string]string{"caption": "draft"}
	updated, err := service.UpdateFile(ctx, files.file.ID, files.file.Revision, api.FileMetadataPatch{Name: &oldName, ContentType: &contentType, Attributes: &attributes})
	if err != nil || updated.Name != oldName || updated.ContentType != contentType || updated.Attributes["caption"] != "draft" || updated.Attributes["keep"] != "" || files.updatedMetadata["name"] != oldName {
		t.Fatalf("metadata replacement semantics: file=%+v stored=%v err=%v", updated, files.updatedMetadata, err)
	}
	if _, err := service.UpdateFile(ctx, files.file.ID, 1, api.FileMetadataPatch{Name: &oldName}); !errors.Is(err, api.ErrConflict) {
		t.Fatalf("stale file metadata revision was not rejected: %v", err)
	}
	added, err := service.AddFileToGroup(ctx, files.file.ID, "content", updated.Revision)
	if err != nil || len(added.Groups) != 2 || added.Groups[1] != "content" {
		t.Fatalf("file group addition: file=%+v err=%v", added, err)
	}
	removed, err := service.RemoveFileFromGroup(ctx, files.file.ID, "content", added.Revision)
	if err != nil || len(removed.Groups) != 1 || removed.Groups[0] != "assets" {
		t.Fatalf("file group removal: file=%+v err=%v", removed, err)
	}
	if err := service.DeleteFile(ctx, files.file.ID, removed.Revision); err != nil || !files.deletedFile {
		t.Fatalf("file deletion delegation: deleted=%v err=%v", files.deletedFile, err)
	}

	sets, err := service.ListFileSets(ctx, api.SetListQuery{Namespace: "features", Query: "asset", Cursor: "set-cursor", Limit: 4})
	if err != nil || !reflect.DeepEqual(files.setListQuery, [5]any{"features", "asset", "set-cursor", 4, ""}) || len(sets.Items) != 1 || sets.NextCursor != "next-set" {
		t.Fatalf("set listing delegation: query=%v page=%+v err=%v", files.setListQuery, sets, err)
	}
	created, err := service.CreateFileSet(ctx, api.CreateFileSetInput{Namespace: "features", Name: "hero", ManagementGroup: "assets"})
	if err != nil || created.Namespace != "features" || created.ManagementGroup != "assets" {
		t.Fatalf("set creation mapping: set=%+v err=%v", created, err)
	}
	loaded, err := service.GetFileSet(ctx, created.ID)
	if err != nil || loaded.ID != created.ID {
		t.Fatalf("set get mapping: set=%+v err=%v", loaded, err)
	}
	newSetName := "hero-v2"
	updatedSet, err := service.UpdateFileSet(ctx, created.ID, created.Revision, api.FileSetMetadataPatch{Name: &newSetName})
	if err != nil || updatedSet.Name != newSetName || updatedSet.Revision != created.Revision+1 {
		t.Fatalf("set update mapping: set=%+v err=%v", updatedSet, err)
	}
	setFiles, err := service.ListSetFiles(ctx, created.ID, api.ListQuery{Group: "assets", Query: "hero", Cursor: "file-cursor", Limit: 3})
	if err != nil || !reflect.DeepEqual(files.searchQuery, [5]any{"hero", "assets", "file-cursor", created.ID, 3}) || len(setFiles.Items) != 1 || setFiles.NextCursor != "next-file" {
		t.Fatalf("filtered set file search delegation: query=%v page=%+v err=%v", files.searchQuery, setFiles, err)
	}
	plainSetFiles, err := service.ListSetFiles(ctx, created.ID, api.ListQuery{Cursor: "plain-cursor", Limit: 2})
	if err != nil || !reflect.DeepEqual(files.setFileQuery, [4]any{created.ID, "plain-cursor", 2, ""}) || len(plainSetFiles.Items) != 1 || plainSetFiles.NextCursor != "next-set-file" {
		t.Fatalf("unfiltered set listing did not use the optimized Core operation: query=%v page=%+v err=%v", files.setFileQuery, plainSetFiles, err)
	}
	addedSet, err := service.AddFileToSet(ctx, created.ID, files.file.ID, updatedSet.Revision)
	if err != nil || files.setAdds != 1 || addedSet.Revision != updatedSet.Revision+1 {
		t.Fatalf("set membership addition: set=%+v calls=%d err=%v", addedSet, files.setAdds, err)
	}
	removedSet, err := service.RemoveFileFromSet(ctx, created.ID, files.file.ID, addedSet.Revision)
	if err != nil || files.setRemoves != 1 || removedSet.Revision != addedSet.Revision+1 {
		t.Fatalf("set membership removal: set=%+v calls=%d err=%v", removedSet, files.setRemoves, err)
	}
	if err := service.DeleteFileSet(ctx, created.ID, removedSet.Revision); err != nil || !files.deletedSet {
		t.Fatalf("set deletion delegation: deleted=%v err=%v", files.deletedSet, err)
	}

	files.failure = core.ErrForbidden
	if _, err := service.ListFiles(ctx, api.ListQuery{Limit: 1}); !errors.Is(err, api.ErrForbidden) {
		t.Fatalf("Core authorization failure was not mapped to the Loader contract: %v", err)
	}
	if _, err := service.ListFiles(context.Background(), api.ListQuery{Limit: 1}); !errors.Is(err, api.ErrUnauthorized) {
		t.Fatalf("unauthenticated file operation was not rejected: %v", err)
	}
}

var _ Files = (*fileOperationsSpy)(nil)
