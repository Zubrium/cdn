package composition

import (
	"context"
	"io"

	platform "github.com/Zubrium/cdn/core/presentation"
	"github.com/Zubrium/cdn/pkg/contracts"
)

type loaderIdentity struct{ runtime *platform.Runtime }

func (a loaderIdentity) session(ctx context.Context, subject contracts.AuthorizationSubject) (platform.Session, error) {
	if subject.Validate() != nil {
		return platform.Session{}, contracts.ErrAccessInvalid
	}
	if subject.Credential != "" {
		session, _, err := a.runtime.Identity().Authenticate(ctx, subject.Credential)
		return session, err
	}
	session, _, err := a.runtime.Sessions().AuthenticateSession(ctx, subject.SessionToken)
	return session, err
}

func (a loaderIdentity) Authenticate(ctx context.Context, credential string) (contracts.AuthorizationSubject, contracts.Principal, error) {
	session, principal, err := a.runtime.Identity().Authenticate(ctx, credential)
	if err != nil {
		return contracts.AuthorizationSubject{}, contracts.Principal{}, mapCoreAccessError(err)
	}
	_ = session
	return contracts.AuthorizationSubject{Credential: credential}, contracts.Principal{
		ID: principal.ID, Name: principal.Name, Active: principal.Active, CanManageAdministrators: principal.CanManageAdministrators,
	}, nil
}

func (a loaderIdentity) AuthenticateSession(ctx context.Context, token string) (contracts.AuthorizationSubject, contracts.Principal, error) {
	session, principal, err := a.runtime.Sessions().AuthenticateSession(ctx, token)
	if err != nil {
		return contracts.AuthorizationSubject{}, contracts.Principal{}, mapCoreAccessError(err)
	}
	_ = session
	return contracts.AuthorizationSubject{SessionToken: token}, contracts.Principal{
		ID: principal.ID, Name: principal.Name, Active: principal.Active, CanManageAdministrators: principal.CanManageAdministrators,
	}, nil
}

func (a loaderIdentity) RequireGroupLevel(ctx context.Context, subject contracts.AuthorizationSubject, groups []string, level contracts.AccessLevel) error {
	session, err := a.session(ctx, subject)
	if err != nil {
		return mapCoreAccessError(err)
	}
	var required platform.AccessLevel
	switch level {
	case contracts.AccessLevelRead:
		required = platform.AccessLevelRead
	case contracts.AccessLevelWrite:
		required = platform.AccessLevelWrite
	default:
		return contracts.ErrAccessInvalid
	}
	return mapCoreAccessError(a.runtime.GroupAuthorization().RequireGroupLevel(ctx, session, groups, required))
}

type loaderFiles struct{ runtime *platform.Runtime }

func (a loaderFiles) session(ctx context.Context, subject contracts.AuthorizationSubject) (platform.Session, error) {
	if subject.Validate() != nil {
		return platform.Session{}, contracts.ErrAccessInvalid
	}
	if subject.Credential != "" {
		session, _, err := a.runtime.Identity().Authenticate(ctx, subject.Credential)
		return session, err
	}
	session, _, err := a.runtime.Sessions().AuthenticateSession(ctx, subject.SessionToken)
	return session, err
}

func (a loaderFiles) PutStagedObject(ctx context.Context, subject contracts.AuthorizationSubject, id string, groups []string, size int64, checksum string, body io.Reader) error {
	session, err := a.session(ctx, subject)
	if err != nil {
		return mapCoreFileError(err)
	}
	return mapCoreFileError(a.runtime.StagedFiles().PutStagedObject(ctx, session, id, groups, size, checksum, body))
}

func (a loaderFiles) ReadStagedObject(ctx context.Context, subject contracts.AuthorizationSubject, id string) (io.ReadCloser, int64, string, error) {
	session, err := a.session(ctx, subject)
	if err != nil {
		return nil, 0, "", mapCoreFileError(err)
	}
	body, size, checksum, err := a.runtime.StagedFiles().ReadStagedObject(ctx, session, id)
	return body, size, checksum, mapCoreFileError(err)
}

func (a loaderFiles) PurgeStagedObject(ctx context.Context, id string) error {
	return mapCoreFileError(a.runtime.StagedFiles().PurgeStagedObject(ctx, id))
}

func (a loaderFiles) ResolveUploadRequest(ctx context.Context, principalID, requestKey string) (contracts.UploadRequestStatus, error) {
	status, err := a.runtime.StagedFiles().ResolveUploadRequest(ctx, principalID, requestKey)
	return contracts.UploadRequestStatus{State: status.State, FileID: status.FileID}, mapCoreFileError(err)
}

func (a loaderFiles) CreateFileVerified(ctx context.Context, subject contracts.AuthorizationSubject, requestKey string, groups []string, metadata map[string]string, contentType string, size int64, checksum string, body io.Reader) (string, error) {
	session, err := a.session(ctx, subject)
	if err != nil {
		return "", mapCoreFileError(err)
	}
	id, err := a.runtime.Files().CreateFileVerified(ctx, session, requestKey, groups, metadata, contentType, size, checksum, body)
	return id, mapCoreFileError(err)
}

func (a loaderFiles) ReadFile(ctx context.Context, subject contracts.AuthorizationSubject, id string) (io.ReadCloser, contracts.File, error) {
	session, err := a.session(ctx, subject)
	if err != nil {
		return nil, contracts.File{}, mapCoreFileError(err)
	}
	body, file, err := a.runtime.Files().ReadFile(ctx, session, id)
	return body, contractFile(file), mapCoreFileError(err)
}

func (a loaderFiles) ReadFileRange(ctx context.Context, subject contracts.AuthorizationSubject, id string, start, end int64) (io.ReadCloser, contracts.File, error) {
	session, err := a.session(ctx, subject)
	if err != nil {
		return nil, contracts.File{}, mapCoreFileError(err)
	}
	body, file, err := a.runtime.Files().ReadFileRange(ctx, session, id, start, end)
	return body, contractFile(file), mapCoreFileError(err)
}

func (a loaderFiles) StatFile(ctx context.Context, subject contracts.AuthorizationSubject, id string) (contracts.File, error) {
	session, err := a.session(ctx, subject)
	if err != nil {
		return contracts.File{}, mapCoreFileError(err)
	}
	file, err := a.runtime.Files().StatFile(ctx, session, id)
	return contractFile(file), mapCoreFileError(err)
}

func (a loaderFiles) SearchFiles(ctx context.Context, subject contracts.AuthorizationSubject, query, group, cursor, setID string, limit int) ([]contracts.File, string, error) {
	session, err := a.session(ctx, subject)
	if err != nil {
		return nil, "", mapCoreFileError(err)
	}
	files, next, err := a.runtime.Files().SearchFiles(ctx, session, query, group, cursor, setID, limit)
	result := make([]contracts.File, len(files))
	for i, file := range files {
		result[i] = contractFile(file)
	}
	return result, next, mapCoreFileError(err)
}

func (a loaderFiles) UpdateFileWithContentType(ctx context.Context, subject contracts.AuthorizationSubject, id string, revision int64, metadata map[string]string, contentType string) error {
	session, err := a.session(ctx, subject)
	if err != nil {
		return mapCoreFileError(err)
	}
	return mapCoreFileError(a.runtime.Files().UpdateFileWithContentType(ctx, session, id, revision, metadata, contentType))
}

func (a loaderFiles) AddFileToGroup(ctx context.Context, subject contracts.AuthorizationSubject, id, group string, revision int64) error {
	session, err := a.session(ctx, subject)
	if err != nil {
		return mapCoreFileError(err)
	}
	return mapCoreFileError(a.runtime.Files().AddFileToGroup(ctx, session, id, group, revision))
}

func (a loaderFiles) RemoveFileFromGroup(ctx context.Context, subject contracts.AuthorizationSubject, id, group string, revision int64) error {
	session, err := a.session(ctx, subject)
	if err != nil {
		return mapCoreFileError(err)
	}
	return mapCoreFileError(a.runtime.Files().RemoveFileFromGroup(ctx, session, id, group, revision))
}

func (a loaderFiles) DeleteFile(ctx context.Context, subject contracts.AuthorizationSubject, id string, revision int64) error {
	session, err := a.session(ctx, subject)
	if err != nil {
		return mapCoreFileError(err)
	}
	return mapCoreFileError(a.runtime.Files().DeleteFile(ctx, session, id, revision))
}

func (a loaderFiles) CreateManagedFileSet(ctx context.Context, subject contracts.AuthorizationSubject, namespace, name, managementGroup string) (contracts.FileSet, error) {
	session, err := a.session(ctx, subject)
	if err != nil {
		return contracts.FileSet{}, mapCoreFileError(err)
	}
	set, err := a.runtime.Files().CreateManagedFileSet(ctx, session, namespace, name, managementGroup)
	return contractFileSet(set), mapCoreFileError(err)
}

func (a loaderFiles) GetManagedFileSet(ctx context.Context, subject contracts.AuthorizationSubject, id string) (contracts.FileSet, error) {
	session, err := a.session(ctx, subject)
	if err != nil {
		return contracts.FileSet{}, mapCoreFileError(err)
	}
	set, err := a.runtime.Files().GetManagedFileSet(ctx, session, id)
	return contractFileSet(set), mapCoreFileError(err)
}

func (a loaderFiles) UpdateManagedFileSet(ctx context.Context, subject contracts.AuthorizationSubject, id string, revision int64, name string) (contracts.FileSet, error) {
	session, err := a.session(ctx, subject)
	if err != nil {
		return contracts.FileSet{}, mapCoreFileError(err)
	}
	set, err := a.runtime.Files().UpdateManagedFileSet(ctx, session, id, revision, name)
	return contractFileSet(set), mapCoreFileError(err)
}

func (a loaderFiles) DeleteManagedFileSet(ctx context.Context, subject contracts.AuthorizationSubject, id string, revision int64) error {
	session, err := a.session(ctx, subject)
	if err != nil {
		return mapCoreFileError(err)
	}
	return mapCoreFileError(a.runtime.Files().DeleteManagedFileSet(ctx, session, id, revision))
}

func (a loaderFiles) AddFileToManagedSet(ctx context.Context, subject contracts.AuthorizationSubject, setID, fileID string, revision int64) (contracts.FileSet, error) {
	session, err := a.session(ctx, subject)
	if err != nil {
		return contracts.FileSet{}, mapCoreFileError(err)
	}
	set, err := a.runtime.Files().AddFileToManagedSet(ctx, session, setID, fileID, revision)
	return contractFileSet(set), mapCoreFileError(err)
}

func (a loaderFiles) RemoveFileFromManagedSet(ctx context.Context, subject contracts.AuthorizationSubject, setID, fileID string, revision int64) (contracts.FileSet, error) {
	session, err := a.session(ctx, subject)
	if err != nil {
		return contracts.FileSet{}, mapCoreFileError(err)
	}
	set, err := a.runtime.Files().RemoveFileFromManagedSet(ctx, session, setID, fileID, revision)
	return contractFileSet(set), mapCoreFileError(err)
}

func (a loaderFiles) ListManagedFileSets(ctx context.Context, subject contracts.AuthorizationSubject, namespace, query, cursor string, limit int) ([]contracts.FileSet, string, error) {
	session, err := a.session(ctx, subject)
	if err != nil {
		return nil, "", mapCoreFileError(err)
	}
	sets, next, err := a.runtime.Files().ListManagedFileSets(ctx, session, namespace, query, cursor, limit)
	result := make([]contracts.FileSet, len(sets))
	for i, set := range sets {
		result[i] = contractFileSet(set)
	}
	return result, next, mapCoreFileError(err)
}

func (a loaderFiles) ListFileSet(ctx context.Context, subject contracts.AuthorizationSubject, setID, cursor string, limit int) ([]contracts.File, string, error) {
	session, err := a.session(ctx, subject)
	if err != nil {
		return nil, "", mapCoreFileError(err)
	}
	files, next, err := a.runtime.Files().ListFileSet(ctx, session, setID, cursor, limit)
	result := make([]contracts.File, len(files))
	for i, file := range files {
		result[i] = contractFile(file)
	}
	return result, next, mapCoreFileError(err)
}

