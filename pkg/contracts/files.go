package contracts

import (
	"context"
	"errors"
	"io"
	"sync"
)

var (
	ErrInvalidFileOperation = errors.New("invalid Core file operation")
	ErrFileForbidden        = errors.New("file access forbidden")
	ErrFileNotFound         = errors.New("file not found")
	ErrFileRevisionConflict = errors.New("file revision conflict")
	ErrFileSetConflict      = errors.New("file set conflict")
	ErrFileChecksumMismatch = errors.New("file checksum mismatch")
	ErrFileRequestConflict  = errors.New("file request conflict")
	ErrFileRequestDeleted   = errors.New("file request references deleted file")
	ErrFileUploadInProgress = errors.New("file upload in progress")
	ErrFileInvalidSearch    = errors.New("invalid file search")
)

// FileCreateVerifiedRequest carries only a scoped stream reference. File bytes
// never enter the JSON operation payload.
type FileCreateVerifiedRequest struct {
	RequestKey  string            `json:"request_key"`
	Groups      []string          `json:"groups"`
	Metadata    map[string]string `json:"metadata"`
	ContentType string            `json:"content_type"`
	Size        int64             `json:"size"`
	SHA256      string            `json:"sha256"`
	Body        StreamRef         `json:"body"`
}

type FileCreateResponse struct {
	FileID string `json:"file_id"`
}

type File struct {
	ID          string            `json:"id"`
	Groups      []string          `json:"groups"`
	Metadata    map[string]string `json:"metadata"`
	ContentType string            `json:"content_type"`
	SHA256      string            `json:"sha256"`
	Revision    int64             `json:"revision"`
	Size        int64             `json:"size"`
}

type FileSet struct {
	ID              string `json:"id"`
	Namespace       string `json:"namespace"`
	Name            string `json:"name"`
	ManagementGroup string `json:"management_group"`
	Revision        int64  `json:"revision"`
}

type FileReadRequest struct {
	FileID string    `json:"file_id"`
	Body   StreamRef `json:"body"`
}

type FileReadResponse struct {
	File File `json:"file"`
}

type FileStatRequest struct {
	FileID  string                `json:"file_id"`
	Subject AuthorizationSubject `json:"subject,omitempty"`
}

type FileStatResponse struct {
	File File `json:"file"`
}

type FileDeleteRequest struct {
	FileID           string `json:"file_id"`
	ExpectedRevision int64  `json:"expected_revision"`
}

type FileDeleteResponse struct{}

type FileSetGetRequest struct {
	SetID string `json:"set_id"`
}

type FileSetGetResponse struct {
	Set FileSet `json:"set"`
}

type FileSetListRequest struct {
	Namespace string `json:"namespace"`
	Query     string `json:"query"`
	Cursor    string `json:"cursor"`
	Limit     int    `json:"limit"`
}

type FileSetListResponse struct {
	Sets       []FileSet `json:"sets"`
	NextCursor string    `json:"next_cursor"`
}

type FileSetMembersRequest struct {
	SetID  string `json:"set_id"`
	Cursor string `json:"cursor"`
	Limit  int    `json:"limit"`
}

type FileSetMembersResponse struct {
	Files      []File `json:"files"`
	NextCursor string `json:"next_cursor"`
}

type FileSetMutationRequest struct {
	SetID            string `json:"set_id"`
	FileID           string `json:"file_id"`
	ExpectedRevision int64  `json:"expected_revision"`
}

type FileSetMutationResponse struct {
	Set FileSet `json:"set"`
}

type UploadRequestStatus struct {
	State  string `json:"state"`
	FileID string `json:"file_id,omitempty"`
}

type FileCreateVerifiedInput struct {
	RequestKey  string
	Groups      []string
	Metadata    map[string]string
	ContentType string
	Size        int64
	SHA256      string
}

type StreamRegistrar interface {
	ScopedStreamCapability
	AddReader(io.ReadCloser) (StreamRef, error)
	AddWriter(io.WriteCloser) (StreamRef, error)
}

type VerifiedFileCreator interface {
	CreateFileVerified(context.Context, string, []string, map[string]string, string, int64, string, io.Reader) (string, error)
	DeleteFile(context.Context, string, int64) error
}

type CoreFileReader interface {
	ReadFile(context.Context, string) (io.ReadCloser, File, error)
}

type CoreFileStat interface {
	StatFile(context.Context, string) (File, error)
}

type CoreFileDeleter interface {
	DeleteFile(context.Context, string, int64) error
}

type CoreFiles interface {
	StatFile(context.Context, string) (File, error)
	ReadFile(context.Context, string) (io.ReadCloser, File, error)
	CreateFileVerified(context.Context, string, []string, map[string]string, string, int64, string, io.Reader) (string, error)
	DeleteFile(context.Context, string, int64) error
	GetManagedFileSet(context.Context, string) (FileSet, error)
	ListManagedFileSets(context.Context, string, string, string, int) ([]FileSet, string, error)
	ListFileSet(context.Context, string, string, int) ([]File, string, error)
	AddFileToManagedSet(context.Context, string, string, int64) (FileSet, error)
	RemoveFileFromManagedSet(context.Context, string, string, int64) (FileSet, error)
}

type StreamRegistrarFactory func() StreamRegistrar

func FileCreateVerifiedOperation() Operation[FileCreateVerifiedRequest, FileCreateResponse] {
	return Operation[FileCreateVerifiedRequest, FileCreateResponse]{
		Key: Key{ID: "core.files.create-verified", Version: 1},
		Validate: func(request FileCreateVerifiedRequest) error {
			if request.RequestKey == "" || len(request.Groups) == 0 || request.ContentType == "" || request.Size < 0 || request.SHA256 == "" || request.Body == "" {
				return ErrInvalidFileOperation
			}
			return nil
		},
	}
}

func FileReadOperation() Operation[FileReadRequest, FileReadResponse] {
	return Operation[FileReadRequest, FileReadResponse]{
		Key: Key{ID: "core.files.read", Version: 1},
		Validate: func(request FileReadRequest) error {
			if request.FileID == "" || request.Body == "" {
				return ErrInvalidFileOperation
			}
			return nil
		},
	}
}

func FileStatOperation() Operation[FileStatRequest, FileStatResponse] {
	return Operation[FileStatRequest, FileStatResponse]{
		Key: Key{ID: "core.files.stat", Version: 1},
		Validate: func(request FileStatRequest) error {
			if request.FileID == "" ||
				(request.Subject.Credential != "" || request.Subject.SessionToken != "") && request.Subject.Validate() != nil {
				return ErrInvalidFileOperation
			}
			return nil
		},
	}
}

func FileDeleteOperation() Operation[FileDeleteRequest, FileDeleteResponse] {
	return Operation[FileDeleteRequest, FileDeleteResponse]{Key: Key{ID: "core.files.delete", Version: 1}, Validate: func(request FileDeleteRequest) error {
		if request.FileID == "" || request.ExpectedRevision < 0 {
			return ErrInvalidFileOperation
		}
		return nil
	}}
}

func FileSetGetOperation() Operation[FileSetGetRequest, FileSetGetResponse] {
	return Operation[FileSetGetRequest, FileSetGetResponse]{Key: Key{ID: "core.files.set.get", Version: 1}, Validate: func(request FileSetGetRequest) error {
		if request.SetID == "" {
			return ErrInvalidFileOperation
		}
		return nil
	}}
}

func FileSetListOperation() Operation[FileSetListRequest, FileSetListResponse] {
	return Operation[FileSetListRequest, FileSetListResponse]{Key: Key{ID: "core.files.set.list", Version: 1}, Validate: func(request FileSetListRequest) error {
		if request.Limit <= 0 {
			return ErrInvalidFileOperation
		}
		return nil
	}}
}

func FileSetMembersOperation() Operation[FileSetMembersRequest, FileSetMembersResponse] {
	return Operation[FileSetMembersRequest, FileSetMembersResponse]{Key: Key{ID: "core.files.set.members", Version: 1}, Validate: func(request FileSetMembersRequest) error {
		if request.SetID == "" || request.Limit <= 0 {
			return ErrInvalidFileOperation
		}
		return nil
	}}
}

func FileSetAddOperation() Operation[FileSetMutationRequest, FileSetMutationResponse] {
	return Operation[FileSetMutationRequest, FileSetMutationResponse]{Key: Key{ID: "core.files.set.add", Version: 1}, Validate: validFileSetMutation}
}

func FileSetRemoveOperation() Operation[FileSetMutationRequest, FileSetMutationResponse] {
	return Operation[FileSetMutationRequest, FileSetMutationResponse]{Key: Key{ID: "core.files.set.remove", Version: 1}, Validate: validFileSetMutation}
}

func validFileSetMutation(request FileSetMutationRequest) error {
	if request.SetID == "" || request.FileID == "" || request.ExpectedRevision < 0 {
		return ErrInvalidFileOperation
	}
	return nil
}

type coreFilesClient struct {
	dispatcher    Dispatcher
	streamFactory StreamRegistrarFactory
}

func NewCoreFilesClient(dispatcher Dispatcher, streams StreamRegistrarFactory) (CoreFiles, error) {
	if dispatcher == nil || streams == nil {
		return nil, ErrInvalidFileOperation
	}
	return &coreFilesClient{dispatcher: dispatcher, streamFactory: streams}, nil
}

func (client *coreFilesClient) StatFile(ctx context.Context, fileID string) (File, error) {
	response, err := Call(ctx, client.dispatcher, FileStatOperation(), FileStatRequest{FileID: fileID})
	return response.File, err
}

func CallFileStatForSubject(ctx context.Context, dispatcher Dispatcher, subject AuthorizationSubject, fileID string) (File, error) {
	if subject.Validate() != nil {
		return File{}, ErrInvalidFileOperation
	}
	response, err := Call(ctx, dispatcher, FileStatOperation(), FileStatRequest{FileID: fileID, Subject: subject})
	return response.File, err
}

func (client *coreFilesClient) ReadFile(ctx context.Context, fileID string) (io.ReadCloser, File, error) {
	streams := client.streams()
	if streams == nil {
		return nil, File{}, ErrStreamUnavailable
	}
	return CallFileRead(ctx, client.dispatcher, streams, fileID)
}

func (client *coreFilesClient) CreateFileVerified(ctx context.Context, requestKey string, groups []string, metadata map[string]string, contentType string, size int64, checksum string, body io.Reader) (string, error) {
	streams := client.streams()
	if streams == nil {
		return "", ErrStreamUnavailable
	}
	response, err := CallFileCreateVerified(ctx, client.dispatcher, streams, FileCreateVerifiedInput{
		RequestKey: requestKey, Groups: groups, Metadata: metadata, ContentType: contentType, Size: size, SHA256: checksum,
	}, body)
	return response.FileID, err
}

func (client *coreFilesClient) DeleteFile(ctx context.Context, fileID string, expectedRevision int64) error {
	_, err := Call(ctx, client.dispatcher, FileDeleteOperation(), FileDeleteRequest{FileID: fileID, ExpectedRevision: expectedRevision})
	return err
}

func (client *coreFilesClient) GetManagedFileSet(ctx context.Context, setID string) (FileSet, error) {
	response, err := Call(ctx, client.dispatcher, FileSetGetOperation(), FileSetGetRequest{SetID: setID})
	return response.Set, err
}

func (client *coreFilesClient) ListManagedFileSets(ctx context.Context, namespace, query, cursor string, limit int) ([]FileSet, string, error) {
	response, err := Call(ctx, client.dispatcher, FileSetListOperation(), FileSetListRequest{Namespace: namespace, Query: query, Cursor: cursor, Limit: limit})
	return response.Sets, response.NextCursor, err
}

func (client *coreFilesClient) ListFileSet(ctx context.Context, setID, cursor string, limit int) ([]File, string, error) {
	response, err := Call(ctx, client.dispatcher, FileSetMembersOperation(), FileSetMembersRequest{SetID: setID, Cursor: cursor, Limit: limit})
	return response.Files, response.NextCursor, err
}

func (client *coreFilesClient) AddFileToManagedSet(ctx context.Context, setID, fileID string, expectedRevision int64) (FileSet, error) {
	response, err := Call(ctx, client.dispatcher, FileSetAddOperation(), FileSetMutationRequest{SetID: setID, FileID: fileID, ExpectedRevision: expectedRevision})
	return response.Set, err
}

func (client *coreFilesClient) RemoveFileFromManagedSet(ctx context.Context, setID, fileID string, expectedRevision int64) (FileSet, error) {
	response, err := Call(ctx, client.dispatcher, FileSetRemoveOperation(), FileSetMutationRequest{SetID: setID, FileID: fileID, ExpectedRevision: expectedRevision})
	return response.Set, err
}

func (client *coreFilesClient) streams() StreamRegistrar {
	if client == nil || client.streamFactory == nil {
		return nil
	}
	return client.streamFactory()
}

func CallFileCreateVerified(ctx context.Context, dispatcher Dispatcher, streams StreamRegistrar, input FileCreateVerifiedInput, body io.Reader) (FileCreateResponse, error) {
	if ctx == nil || dispatcher == nil || streams == nil || body == nil || input.RequestKey == "" || len(input.Groups) == 0 || input.ContentType == "" || input.Size < 0 || input.SHA256 == "" {
		return FileCreateResponse{}, ErrInvalidFileOperation
	}
	reference, err := streams.AddReader(io.NopCloser(body))
	if err != nil {
		return FileCreateResponse{}, err
	}
	request := FileCreateVerifiedRequest{
		RequestKey: input.RequestKey, Groups: append([]string(nil), input.Groups...), Metadata: cloneStringMap(input.Metadata),
		ContentType: input.ContentType, Size: input.Size, SHA256: input.SHA256, Body: reference,
	}
	return CallWithStreams(ctx, dispatcher, FileCreateVerifiedOperation(), request, streams)
}

func CallFileRead(ctx context.Context, dispatcher Dispatcher, streams StreamRegistrar, fileID string) (io.ReadCloser, File, error) {
	if ctx == nil || dispatcher == nil || streams == nil || fileID == "" {
		return nil, File{}, ErrInvalidFileOperation
	}
	reader, writer := io.Pipe()
	reference, err := streams.AddWriter(writer)
	if err != nil {
		_ = reader.Close()
		_ = writer.Close()
		return nil, File{}, err
	}
	response, err := Call(WithStreamCapability(ctx, streams), dispatcher, FileReadOperation(), FileReadRequest{FileID: fileID, Body: reference})
	if err != nil {
		_ = reader.Close()
		_ = streams.Close()
		return nil, File{}, err
	}
	body := &scopedFileReader{reader: reader, streams: streams, done: make(chan struct{})}
	if cancelled := ctx.Done(); cancelled != nil {
		go func() {
			select {
			case <-cancelled:
				_ = body.Close()
			case <-body.done:
			}
		}()
	}
	return body, response.File, nil
}

type scopedFileReader struct {
	reader   *io.PipeReader
	streams  ScopedStreamCapability
	done     chan struct{}
	once     sync.Once
	closeErr error
}

func (reader *scopedFileReader) Read(buffer []byte) (int, error) {
	count, err := reader.reader.Read(buffer)
	if errors.Is(err, io.EOF) {
		_ = reader.Close()
	}
	return count, err
}

func (reader *scopedFileReader) Close() error {
	if reader == nil {
		return nil
	}
	reader.once.Do(func() {
		close(reader.done)
		reader.closeErr = errors.Join(reader.reader.Close(), reader.streams.Close())
	})
	return reader.closeErr
}

func cloneStringMap(value map[string]string) map[string]string {
	if value == nil {
		return nil
	}
	result := make(map[string]string, len(value))
	for key, entry := range value {
		result[key] = entry
	}
	return result
}
