package composition

import (
	"context"
	"errors"
	"io"

	platform "github.com/Zubrium/cdn/core/presentation"
	"github.com/Zubrium/cdn/pkg/contracts"
)

type coreSessionFileAdapter struct {
	files   platform.FilesCapability
	session platform.Session
}

type coreSubjectFileStat struct {
	files   platform.FilesCapability
	session platform.Session
}

func (adapter coreSubjectFileStat) StatFile(ctx context.Context, fileID string) (contracts.File, error) {
	file, err := adapter.files.StatFile(ctx, adapter.session, fileID)
	return contractFile(file), mapCoreFileError(err)
}

func hasCoreFileConsumers(selected map[string]struct{}) bool {
	for _, moduleID := range []string{
		"engine.website", "engine.website.import", "engine.content", "engine.forms", "engine.router",
		"worker.archive", "worker.media", "worker.prerender", "main.publication", "admin.api",
	} {
		if _, ok := selected[moduleID]; ok {
			return true
		}
	}
	if _, ok := selected["engine.publication"]; ok {
		return true
	}
	if _, ok := selected["admin.coordinator"]; ok {
		return true
	}
	if _, ok := selected["admin.identity"]; ok {
		return true
	}
	return false
}

func hasCoreAccessConsumers(selected map[string]struct{}) bool {
	for moduleID := range selected {
		if moduleUsesCoreAccess(moduleID, selected) {
			return true
		}
	}
	return false
}

func moduleUsesCoreAccess(moduleID string, selected map[string]struct{}) bool {
	if moduleID == "admin.api" || moduleID == "main.publication" || moduleID == "composition.retirement" {
		return true
	}
	if _, ok := selected[moduleID]; !ok {
		return false
	}
	switch moduleID {
	case "engine.website", "engine.website.import", "engine.content", "engine.forms", "engine.router", "admin.coordinator", "admin.identity":
		return true
	default:
		return false
	}
}

func isCoreFileConsumer(moduleID string) bool {
	switch moduleID {
	case "engine.website", "engine.website.import", "engine.content", "engine.forms", "engine.router",
		"worker.archive", "worker.media", "worker.prerender", "main.publication", "admin.api":
		return true
	default:
		return false
	}
}

func (adapter coreSessionFileAdapter) StatFile(ctx context.Context, fileID string) (contracts.File, error) {
	file, err := adapter.files.StatFile(ctx, adapter.session, fileID)
	return contractFile(file), mapCoreFileError(err)
}

func (adapter coreSessionFileAdapter) ReadFile(ctx context.Context, fileID string) (io.ReadCloser, contracts.File, error) {
	body, file, err := adapter.files.ReadFile(ctx, adapter.session, fileID)
	if err != nil {
		return nil, contracts.File{}, mapCoreFileError(err)
	}
	return body, contractFile(file), nil
}

func (adapter coreSessionFileAdapter) CreateFileVerified(ctx context.Context, requestKey string, groups []string, metadata map[string]string, contentType string, size int64, checksum string, body io.Reader) (string, error) {
	fileID, err := adapter.files.CreateFileVerified(ctx, adapter.session, requestKey, groups, metadata, contentType, size, checksum, body)
	return fileID, mapCoreFileError(err)
}

func (adapter coreSessionFileAdapter) DeleteFile(ctx context.Context, fileID string, expectedRevision int64) error {
	return mapCoreFileError(adapter.files.DeleteFile(ctx, adapter.session, fileID, expectedRevision))
}

func (adapter coreSessionFileAdapter) GetManagedFileSet(ctx context.Context, setID string) (contracts.FileSet, error) {
	set, err := adapter.files.GetManagedFileSet(ctx, adapter.session, setID)
	return contractFileSet(set), mapCoreFileError(err)
}

func (adapter coreSessionFileAdapter) ListManagedFileSets(ctx context.Context, namespace, query, cursor string, limit int) ([]contracts.FileSet, string, error) {
	sets, next, err := adapter.files.ListManagedFileSets(ctx, adapter.session, namespace, query, cursor, limit)
	result := make([]contracts.FileSet, len(sets))
	for i, set := range sets {
		result[i] = contractFileSet(set)
	}
	return result, next, mapCoreFileError(err)
}

func (adapter coreSessionFileAdapter) ListFileSet(ctx context.Context, setID, cursor string, limit int) ([]contracts.File, string, error) {
	files, next, err := adapter.files.ListFileSet(ctx, adapter.session, setID, cursor, limit)
	result := make([]contracts.File, len(files))
	for i, file := range files {
		result[i] = contractFile(file)
	}
	return result, next, mapCoreFileError(err)
}

func (adapter coreSessionFileAdapter) AddFileToManagedSet(ctx context.Context, setID, fileID string, expectedRevision int64) (contracts.FileSet, error) {
	set, err := adapter.files.AddFileToManagedSet(ctx, adapter.session, setID, fileID, expectedRevision)
	return contractFileSet(set), mapCoreFileError(err)
}

func (adapter coreSessionFileAdapter) RemoveFileFromManagedSet(ctx context.Context, setID, fileID string, expectedRevision int64) (contracts.FileSet, error) {
	set, err := adapter.files.RemoveFileFromManagedSet(ctx, adapter.session, setID, fileID, expectedRevision)
	return contractFileSet(set), mapCoreFileError(err)
}

func contractFile(file platform.File) contracts.File {
	metadata := make(map[string]string, len(file.Metadata))
	for key, value := range file.Metadata {
		metadata[key] = value
	}
	return contracts.File{ID: file.ID, Groups: append([]string(nil), file.Groups...), Metadata: metadata,
		ContentType: file.ContentType, SHA256: file.SHA256, Revision: file.Revision, Size: file.Size}
}

func contractFileSet(set platform.FileSet) contracts.FileSet {
	return contracts.FileSet{ID: set.ID, Namespace: set.Namespace, Name: set.Name, ManagementGroup: set.ManagementGroup, Revision: set.Revision}
}

func mapCoreFileError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, platform.ErrForbidden):
		return errors.Join(contracts.ErrFileForbidden, err)
	case errors.Is(err, platform.ErrFileNotFound):
		return errors.Join(contracts.ErrFileNotFound, err)
	case errors.Is(err, platform.ErrRevisionConflict):
		return errors.Join(contracts.ErrFileRevisionConflict, err)
	case errors.Is(err, platform.ErrFileSetConflict):
		return errors.Join(contracts.ErrFileSetConflict, err)
	case errors.Is(err, platform.ErrChecksumMismatch):
		return errors.Join(contracts.ErrFileChecksumMismatch, err)
	case errors.Is(err, platform.ErrRequestConflict):
		return errors.Join(contracts.ErrFileRequestConflict, err)
	case errors.Is(err, platform.ErrRequestDeleted):
		return errors.Join(contracts.ErrFileRequestDeleted, err)
	case errors.Is(err, platform.ErrUploadInProgress):
		return errors.Join(contracts.ErrFileUploadInProgress, err)
	default:
		return err
	}
}

var _ contracts.CoreFiles = coreSessionFileAdapter{}
