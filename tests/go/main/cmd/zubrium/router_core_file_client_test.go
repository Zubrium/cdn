package main

import (
	"context"
	"io"

	platform "github.com/Zubrium/cdn/core/presentation"
	"github.com/Zubrium/cdn/pkg/contracts"
)

type testCoreFilesAdapter struct {
	files   platform.FilesCapability
	session platform.Session
}

func (client testCoreFilesAdapter) StatFile(ctx context.Context, fileID string) (contracts.File, error) {
	file, err := client.files.StatFile(ctx, client.session, fileID)
	return contracts.File{ID: file.ID, Groups: append([]string(nil), file.Groups...), Metadata: file.Metadata,
		ContentType: file.ContentType, SHA256: file.SHA256, Revision: file.Revision, Size: file.Size}, err
}

func (client testCoreFilesAdapter) ReadFile(ctx context.Context, fileID string) (io.ReadCloser, contracts.File, error) {
	body, file, err := client.files.ReadFile(ctx, client.session, fileID)
	return body, contracts.File{ID: file.ID, Groups: append([]string(nil), file.Groups...), Metadata: file.Metadata,
		ContentType: file.ContentType, SHA256: file.SHA256, Revision: file.Revision, Size: file.Size}, err
}

func (client testCoreFilesAdapter) CreateFileVerified(ctx context.Context, key string, groups []string, metadata map[string]string, contentType string, size int64, checksum string, body io.Reader) (string, error) {
	return client.files.CreateFileVerified(ctx, client.session, key, groups, metadata, contentType, size, checksum, body)
}

func (client testCoreFilesAdapter) DeleteFile(ctx context.Context, fileID string, revision int64) error {
	return client.files.DeleteFile(ctx, client.session, fileID, revision)
}

func (client testCoreFilesAdapter) GetManagedFileSet(ctx context.Context, setID string) (contracts.FileSet, error) {
	set, err := client.files.GetManagedFileSet(ctx, client.session, setID)
	return contracts.FileSet{ID: set.ID, Namespace: set.Namespace, Name: set.Name, ManagementGroup: set.ManagementGroup, Revision: set.Revision}, err
}

func (client testCoreFilesAdapter) ListManagedFileSets(ctx context.Context, namespace, query, cursor string, limit int) ([]contracts.FileSet, string, error) {
	sets, next, err := client.files.ListManagedFileSets(ctx, client.session, namespace, query, cursor, limit)
	result := make([]contracts.FileSet, len(sets))
	for i, set := range sets {
		result[i] = contracts.FileSet{ID: set.ID, Namespace: set.Namespace, Name: set.Name, ManagementGroup: set.ManagementGroup, Revision: set.Revision}
	}
	return result, next, err
}

func (client testCoreFilesAdapter) ListFileSet(ctx context.Context, setID, cursor string, limit int) ([]contracts.File, string, error) {
	files, next, err := client.files.ListFileSet(ctx, client.session, setID, cursor, limit)
	result := make([]contracts.File, len(files))
	for i, file := range files {
		result[i] = contracts.File{ID: file.ID, Groups: append([]string(nil), file.Groups...), Metadata: file.Metadata,
			ContentType: file.ContentType, SHA256: file.SHA256, Revision: file.Revision, Size: file.Size}
	}
	return result, next, err
}

func (client testCoreFilesAdapter) AddFileToManagedSet(ctx context.Context, setID, fileID string, revision int64) (contracts.FileSet, error) {
	set, err := client.files.AddFileToManagedSet(ctx, client.session, setID, fileID, revision)
	return contracts.FileSet{ID: set.ID, Namespace: set.Namespace, Name: set.Name, ManagementGroup: set.ManagementGroup, Revision: set.Revision}, err
}

func (client testCoreFilesAdapter) RemoveFileFromManagedSet(ctx context.Context, setID, fileID string, revision int64) (contracts.FileSet, error) {
	set, err := client.files.RemoveFileFromManagedSet(ctx, client.session, setID, fileID, revision)
	return contracts.FileSet{ID: set.ID, Namespace: set.Namespace, Name: set.Name, ManagementGroup: set.ManagementGroup, Revision: set.Revision}, err
}

var _ contracts.CoreFiles = testCoreFilesAdapter{}
