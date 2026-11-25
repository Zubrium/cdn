package composition

import (
	"context"
	"errors"
	"io"
	"os"
	"sync"

	"github.com/Zubrium/cdn/main/internal/bus"
	"github.com/Zubrium/cdn/pkg/contracts"
)

// adminFilesAdapter is the only Admin Files dependency on Loader. Large part
// and download bodies travel as scoped stream capabilities; request payloads
// carry opaque refs and cannot name an engine, module, class, or credential.
type adminFilesAdapter struct {
	dispatcher contracts.Dispatcher
}

func (adapter *adminFilesAdapter) subject(ctx context.Context) (contracts.AuthorizationSubject, error) {
	subject, ok := contracts.AdminSubjectFromContext(ctx)
	if !ok {
		return contracts.AuthorizationSubject{}, contracts.ErrAdminFilesUnauthorized
	}
	return subject, nil
}

func (adapter *adminFilesAdapter) Authenticate(ctx context.Context, credential string) (context.Context, error) {
	if adapter == nil || adapter.dispatcher == nil {
		return nil, contracts.ErrAdminFilesUnavailable
	}
	response, err := contracts.Call(ctx, adapter.dispatcher, contracts.AdminFilesAuthenticateOperation(), contracts.AdminAuthenticateRequest{Credential: credential})
	if err != nil {
		return nil, err
	}
	return contracts.WithAdminSubject(ctx, response.Subject), nil
}

func (adapter *adminFilesAdapter) AuthenticateSession(ctx context.Context, token string) (context.Context, error) {
	if adapter == nil || adapter.dispatcher == nil {
		return nil, contracts.ErrAdminFilesUnavailable
	}
	response, err := contracts.Call(ctx, adapter.dispatcher, contracts.AdminFilesAuthenticateSessionOperation(), contracts.AdminAuthenticateSessionRequest{Token: token})
	if err != nil {
		return nil, err
	}
	return contracts.WithAdminSubject(ctx, response.Subject), nil
}

func (adapter *adminFilesAdapter) ListFileSets(ctx context.Context, query contracts.AdminFileSetListQuery) (contracts.AdminFileSetPage, error) {
	subject, err := adapter.subject(ctx)
	if err != nil {
		return contracts.AdminFileSetPage{}, err
	}
	response, err := contracts.Call(ctx, adapter.dispatcher, contracts.AdminFilesListFileSetsOperation(), contracts.AdminFilesSetListRequest{Subject: subject, Query: query})
	if err != nil {
		return contracts.AdminFileSetPage{}, err
	}
	return response.Page, nil
}

func (adapter *adminFilesAdapter) CreateFileSet(ctx context.Context, input contracts.AdminFileCreateFileSetInput) (contracts.AdminFileSet, error) {
	subject, err := adapter.subject(ctx)
	if err != nil {
		return contracts.AdminFileSet{}, err
	}
	response, err := contracts.Call(ctx, adapter.dispatcher, contracts.AdminFilesCreateFileSetOperation(), contracts.AdminFilesCreateSetRequest{Subject: subject, Input: input})
	if err != nil {
		return contracts.AdminFileSet{}, err
	}
	return response.Set, nil
}

func (adapter *adminFilesAdapter) GetFileSet(ctx context.Context, id string) (contracts.AdminFileSet, error) {
	subject, err := adapter.subject(ctx)
	if err != nil {
		return contracts.AdminFileSet{}, err
	}
	response, err := contracts.Call(ctx, adapter.dispatcher, contracts.AdminFilesGetFileSetOperation(), contracts.AdminFilesIDRequest{Subject: subject, ID: id})
	if err != nil {
		return contracts.AdminFileSet{}, err
	}
	return response.Set, nil
}

func (adapter *adminFilesAdapter) UpdateFileSet(ctx context.Context, id string, expectedRevision int64, patch contracts.AdminFileSetMetadataPatch) (contracts.AdminFileSet, error) {
	subject, err := adapter.subject(ctx)
	if err != nil {
		return contracts.AdminFileSet{}, err
	}
	response, err := contracts.Call(ctx, adapter.dispatcher, contracts.AdminFilesUpdateFileSetOperation(), contracts.AdminFilesSetUpdateRequest{Subject: subject, ID: id, ExpectedRevision: expectedRevision, Patch: patch})
	if err != nil {
		return contracts.AdminFileSet{}, err
	}
	return response.Set, nil
}

func (adapter *adminFilesAdapter) DeleteFileSet(ctx context.Context, id string, expectedRevision int64) error {
	subject, err := adapter.subject(ctx)
	if err != nil {
		return err
	}
	_, err = contracts.Call(ctx, adapter.dispatcher, contracts.AdminFilesDeleteFileSetOperation(), contracts.AdminFilesRevisionRequest{Subject: subject, ID: id, ExpectedRevision: expectedRevision})
	return err
}

func (adapter *adminFilesAdapter) ListSetFiles(ctx context.Context, setID string, query contracts.AdminFileListQuery) (contracts.AdminFilePage, error) {
	subject, err := adapter.subject(ctx)
	if err != nil {
		return contracts.AdminFilePage{}, err
	}
	response, err := contracts.Call(ctx, adapter.dispatcher, contracts.AdminFilesListSetFilesOperation(), contracts.AdminFilesListRequest{Subject: subject, SetID: setID, Query: query})
	if err != nil {
		return contracts.AdminFilePage{}, err
	}
	return response.Page, nil
}

func (adapter *adminFilesAdapter) AddFileToSet(ctx context.Context, setID, fileID string, expectedRevision int64) (contracts.AdminFileSet, error) {
	subject, err := adapter.subject(ctx)
	if err != nil {
		return contracts.AdminFileSet{}, err
	}
	response, err := contracts.Call(ctx, adapter.dispatcher, contracts.AdminFilesAddToSetOperation(), contracts.AdminFilesNamedRevisionRequest{Subject: subject, ID: setID, Name: fileID, ExpectedRevision: expectedRevision})
	if err != nil {
		return contracts.AdminFileSet{}, err
	}
	return response.Set, nil
}

func (adapter *adminFilesAdapter) RemoveFileFromSet(ctx context.Context, setID, fileID string, expectedRevision int64) (contracts.AdminFileSet, error) {
	subject, err := adapter.subject(ctx)
	if err != nil {
		return contracts.AdminFileSet{}, err
	}
	response, err := contracts.Call(ctx, adapter.dispatcher, contracts.AdminFilesRemoveFromSetOperation(), contracts.AdminFilesNamedRevisionRequest{Subject: subject, ID: setID, Name: fileID, ExpectedRevision: expectedRevision})
	if err != nil {
		return contracts.AdminFileSet{}, err
	}
	return response.Set, nil
}

func (adapter *adminFilesAdapter) BeginUpload(ctx context.Context, input contracts.AdminFileBeginUploadInput) (contracts.AdminFileUploadSession, error) {
	subject, err := adapter.subject(ctx)
	if err != nil {
		return contracts.AdminFileUploadSession{}, err
	}
	response, err := contracts.Call(ctx, adapter.dispatcher, contracts.AdminFilesBeginUploadOperation(), contracts.AdminFilesBeginUploadRequest{Subject: subject, Input: input, IdempotencyKey: input.IdempotencyKey})
	if err != nil {
		return contracts.AdminFileUploadSession{}, err
	}
	return response.Session, nil
}

func (adapter *adminFilesAdapter) UploadStatus(ctx context.Context, id string) (contracts.AdminFileUploadSession, error) {
	subject, err := adapter.subject(ctx)
	if err != nil {
		return contracts.AdminFileUploadSession{}, err
	}
	response, err := contracts.Call(ctx, adapter.dispatcher, contracts.AdminFilesUploadStatusOperation(), contracts.AdminFilesIDRequest{Subject: subject, ID: id})
	if err != nil {
		return contracts.AdminFileUploadSession{}, err
	}
	return response.Session, nil
}

func (adapter *adminFilesAdapter) PutUploadPart(ctx context.Context, id string, number int, size int64, sha256 string, body io.Reader) (contracts.AdminFileUploadSession, error) {
	subject, err := adapter.subject(ctx)
	if err != nil {
		return contracts.AdminFileUploadSession{}, err
	}
	reader, ok := body.(io.ReadCloser)
	if !ok {
		reader = io.NopCloser(body)
	}
	streams := bus.NewScopedStreams()
	ref, err := streams.AddReader(reader)
	if err != nil {
		_ = reader.Close()
		return contracts.AdminFileUploadSession{}, err
	}
	response, err := contracts.CallWithStreams(ctx, adapter.dispatcher, contracts.AdminFilesPutUploadPartOperation(), contracts.AdminFilesUploadPartRequest{
		Subject: subject, ID: id, Number: number, Size: size, SHA256: sha256, Body: ref,
	}, streams)
	if err != nil {
		return contracts.AdminFileUploadSession{}, err
	}
	return response.Session, nil
}

func (adapter *adminFilesAdapter) CommitUpload(ctx context.Context, id string) (contracts.AdminFile, error) {
	subject, err := adapter.subject(ctx)
	if err != nil {
		return contracts.AdminFile{}, err
	}
	response, err := contracts.Call(ctx, adapter.dispatcher, contracts.AdminFilesCommitUploadOperation(), contracts.AdminFilesIDRequest{Subject: subject, ID: id})
	if err != nil {
		return contracts.AdminFile{}, err
	}
	return response.File, nil
}

func (adapter *adminFilesAdapter) AbortUpload(ctx context.Context, id string) error {
	subject, err := adapter.subject(ctx)
	if err != nil {
		return err
	}
	_, err = contracts.Call(ctx, adapter.dispatcher, contracts.AdminFilesAbortUploadOperation(), contracts.AdminFilesIDRequest{Subject: subject, ID: id})
	return err
}

func (adapter *adminFilesAdapter) ListFiles(ctx context.Context, query contracts.AdminFileListQuery) (contracts.AdminFilePage, error) {
	subject, err := adapter.subject(ctx)
	if err != nil {
		return contracts.AdminFilePage{}, err
	}
	response, err := contracts.Call(ctx, adapter.dispatcher, contracts.AdminFilesListFilesOperation(), contracts.AdminFilesListRequest{Subject: subject, Query: query})
	if err != nil {
		return contracts.AdminFilePage{}, err
	}
	return response.Page, nil
}

func (adapter *adminFilesAdapter) StatFile(ctx context.Context, id string) (contracts.AdminFile, error) {
	subject, err := adapter.subject(ctx)
	if err != nil {
		return contracts.AdminFile{}, err
	}
	response, err := contracts.Call(ctx, adapter.dispatcher, contracts.AdminFilesStatFileOperation(), contracts.AdminFilesIDRequest{Subject: subject, ID: id})
	if err != nil {
		return contracts.AdminFile{}, err
	}
	return response.File, nil
}

func (adapter *adminFilesAdapter) ReadFile(ctx context.Context, id string) (io.ReadCloser, contracts.AdminFile, error) {
	return adapter.readFile(ctx, contracts.AdminFilesReadRequest{ID: id}, false)
}

func (adapter *adminFilesAdapter) ReadFileRange(ctx context.Context, id string, start, end int64) (io.ReadCloser, contracts.AdminFile, error) {
	return adapter.readFile(ctx, contracts.AdminFilesReadRequest{ID: id, Start: start, End: end, Range: true}, true)
}

func (adapter *adminFilesAdapter) readFile(ctx context.Context, request contracts.AdminFilesReadRequest, ranged bool) (io.ReadCloser, contracts.AdminFile, error) {
	subject, err := adapter.subject(ctx)
	if err != nil {
		return nil, contracts.AdminFile{}, err
	}
	streams := bus.NewScopedStreams()
	file, err := os.CreateTemp("", "admin-file-read-*")
	if err != nil {
		return nil, contracts.AdminFile{}, err
	}
	ref, err := streams.AddWriter(adminFileNoCloseWriter{Writer: file})
	if err != nil {
		_ = file.Close()
		_ = os.Remove(file.Name())
		return nil, contracts.AdminFile{}, err
	}
	request.Subject = subject
	request.Body = ref
	request.Range = ranged
	response, callErr := contracts.CallWithStreams(ctx, adapter.dispatcher, contracts.AdminFilesReadOperation(), request, streams)
	if callErr != nil {
		_ = file.Close()
		_ = os.Remove(file.Name())
		return nil, contracts.AdminFile{}, callErr
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		_ = file.Close()
		_ = os.Remove(file.Name())
		return nil, contracts.AdminFile{}, err
	}
	return &adminFileReadCloser{File: file, path: file.Name()}, response.File, nil
}

func (adapter *adminFilesAdapter) UpdateFile(ctx context.Context, id string, expectedRevision int64, patch contracts.AdminFileMetadataPatch) (contracts.AdminFile, error) {
	subject, err := adapter.subject(ctx)
	if err != nil {
		return contracts.AdminFile{}, err
	}
	response, err := contracts.Call(ctx, adapter.dispatcher, contracts.AdminFilesUpdateFileOperation(), contracts.AdminFilesFileUpdateRequest{Subject: subject, ID: id, ExpectedRevision: expectedRevision, Patch: patch})
	if err != nil {
		return contracts.AdminFile{}, err
	}
	return response.File, nil
}

func (adapter *adminFilesAdapter) DeleteFile(ctx context.Context, id string, expectedRevision int64) error {
	subject, err := adapter.subject(ctx)
	if err != nil {
		return err
	}
	_, err = contracts.Call(ctx, adapter.dispatcher, contracts.AdminFilesDeleteFileOperation(), contracts.AdminFilesRevisionRequest{Subject: subject, ID: id, ExpectedRevision: expectedRevision})
	return err
}

func (adapter *adminFilesAdapter) AddFileToGroup(ctx context.Context, id, name string, expectedRevision int64) (contracts.AdminFile, error) {
	subject, err := adapter.subject(ctx)
	if err != nil {
		return contracts.AdminFile{}, err
	}
	response, err := contracts.Call(ctx, adapter.dispatcher, contracts.AdminFilesAddToGroupOperation(), contracts.AdminFilesNamedRevisionRequest{Subject: subject, ID: id, Name: name, ExpectedRevision: expectedRevision})
	if err != nil {
		return contracts.AdminFile{}, err
	}
	return response.File, nil
}

func (adapter *adminFilesAdapter) RemoveFileFromGroup(ctx context.Context, id, name string, expectedRevision int64) (contracts.AdminFile, error) {
	subject, err := adapter.subject(ctx)
	if err != nil {
		return contracts.AdminFile{}, err
	}
	response, err := contracts.Call(ctx, adapter.dispatcher, contracts.AdminFilesRemoveFromGroupOperation(), contracts.AdminFilesNamedRevisionRequest{Subject: subject, ID: id, Name: name, ExpectedRevision: expectedRevision})
	if err != nil {
		return contracts.AdminFile{}, err
	}
	return response.File, nil
}

// adminFileNoCloseWriter keeps the backing temp file open after the bus closes
// the scoped writer, so the caller can read the same bytes as a stream.
type adminFileNoCloseWriter struct{ io.Writer }

func (adminFileNoCloseWriter) Close() error { return nil }

type adminFileReadCloser struct {
	*os.File
	path      string
	closeOnce sync.Once
	closeErr  error
}

func (reader *adminFileReadCloser) Close() error {
	reader.closeOnce.Do(func() {
		reader.closeErr = errors.Join(reader.File.Close(), os.Remove(reader.path))
	})
	return reader.closeErr
}

var _ contracts.AdminFilesAPI = (*adminFilesAdapter)(nil)
