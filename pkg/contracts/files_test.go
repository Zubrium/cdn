package contracts

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"testing"
)

type uploadStreams struct {
	reader io.ReadCloser
	writer io.WriteCloser
	closed bool
}

func (streams *uploadStreams) AddReader(reader io.ReadCloser) (StreamRef, error) {
	streams.reader = reader
	return "scoped-upload", nil
}

func (streams *uploadStreams) OpenRead(context.Context, StreamRef) (io.ReadCloser, error) {
	return streams.reader, nil
}

func (streams *uploadStreams) AddWriter(writer io.WriteCloser) (StreamRef, error) {
	streams.writer = writer
	return "scoped-download", nil
}

func (streams *uploadStreams) OpenWrite(context.Context, StreamRef) (io.WriteCloser, error) {
	return streams.writer, nil
}

func (streams *uploadStreams) Close() error {
	streams.closed = true
	if streams.reader != nil {
		return streams.reader.Close()
	}
	return streams.writer.Close()
}

type downloadDispatcher struct{ request Envelope }

func (dispatcher *downloadDispatcher) Dispatch(ctx context.Context, request Envelope) (Envelope, error) {
	dispatcher.request = request
	var payload FileReadRequest
	if err := json.Unmarshal(request.Payload, &payload); err != nil {
		return Envelope{}, err
	}
	capability, ok := StreamCapabilityFromContext(ctx)
	if !ok {
		return Envelope{}, ErrStreamUnavailable
	}
	writer, err := capability.OpenWrite(ctx, payload.Body)
	if err != nil {
		return Envelope{}, err
	}
	go func() {
		_, _ = io.WriteString(writer, "download bytes")
		_ = writer.Close()
	}()
	response, err := json.Marshal(FileReadResponse{File: File{ID: payload.FileID, Size: 14, ContentType: "text/plain"}})
	return Envelope{OperationID: request.OperationID, Version: request.Version, Payload: response}, err
}

func TestFileReadReturnsMetadataAndScopedStream(t *testing.T) {
	dispatcher := &downloadDispatcher{}
	streams := &uploadStreams{}
	body, file, err := CallFileRead(context.Background(), dispatcher, streams, "file-1")
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	if err := body.Close(); err != nil {
		t.Fatal(err)
	}
	if string(data) != "download bytes" || file.ID != "file-1" || file.Size != 14 || file.ContentType != "text/plain" {
		t.Fatalf("read = %q, %#v", data, file)
	}
	if dispatcher.request.OperationID != "core.files.read" || dispatcher.request.Version != 1 {
		t.Fatalf("operation = %s/v%d", dispatcher.request.OperationID, dispatcher.request.Version)
	}
	if bytes.Contains(dispatcher.request.Payload, data) {
		t.Fatal("file bytes were serialized into the DataBus envelope")
	}
	if !streams.closed {
		t.Fatal("scoped download stream was not closed")
	}
}

type statDispatcher struct{ request Envelope }

func (dispatcher *statDispatcher) Dispatch(_ context.Context, request Envelope) (Envelope, error) {
	dispatcher.request = request
	response, err := json.Marshal(FileStatResponse{File: File{ID: "file-9", Revision: 4, Size: 21, Groups: []string{"content"}}})
	return Envelope{OperationID: request.OperationID, Version: request.Version, Payload: response}, err
}

func TestCoreFilesClientStatUsesTypedOperation(t *testing.T) {
	dispatcher := &statDispatcher{}
	files, err := NewCoreFilesClient(dispatcher, func() StreamRegistrar { return &uploadStreams{} })
	if err != nil {
		t.Fatal(err)
	}
	file, err := files.StatFile(context.Background(), "file-9")
	if err != nil {
		t.Fatal(err)
	}
	if file.ID != "file-9" || file.Revision != 4 || file.Size != 21 || len(file.Groups) != 1 || file.Groups[0] != "content" {
		t.Fatalf("file = %#v", file)
	}
	if dispatcher.request.OperationID != "core.files.stat" || dispatcher.request.Version != 1 {
		t.Fatalf("operation = %s/v%d", dispatcher.request.OperationID, dispatcher.request.Version)
	}
	var request map[string]any
	if err := json.Unmarshal(dispatcher.request.Payload, &request); err != nil {
		t.Fatal(err)
	}
	if request["file_id"] != "file-9" {
		t.Fatalf("request = %#v", request)
	}
}

type deleteDispatcher struct{ request Envelope }

func (dispatcher *deleteDispatcher) Dispatch(_ context.Context, request Envelope) (Envelope, error) {
	dispatcher.request = request
	response, err := json.Marshal(FileDeleteResponse{})
	return Envelope{OperationID: request.OperationID, Version: request.Version, Payload: response}, err
}

func TestCoreFilesClientDeleteCarriesExpectedRevision(t *testing.T) {
	dispatcher := &deleteDispatcher{}
	files, err := NewCoreFilesClient(dispatcher, func() StreamRegistrar { return &uploadStreams{} })
	if err != nil {
		t.Fatal(err)
	}
	if err := files.DeleteFile(context.Background(), "file-2", 9); err != nil {
		t.Fatal(err)
	}
	if dispatcher.request.OperationID != "core.files.delete" || dispatcher.request.Version != 1 {
		t.Fatalf("operation = %s/v%d", dispatcher.request.OperationID, dispatcher.request.Version)
	}
	var request FileDeleteRequest
	if err := json.Unmarshal(dispatcher.request.Payload, &request); err != nil {
		t.Fatal(err)
	}
	if request.FileID != "file-2" || request.ExpectedRevision != 9 {
		t.Fatalf("request = %#v", request)
	}
}

type uploadDispatcher struct {
	request Envelope
	body    []byte
}

func (dispatcher *uploadDispatcher) Dispatch(ctx context.Context, request Envelope) (Envelope, error) {
	dispatcher.request = request
	var payload FileCreateVerifiedRequest
	if err := json.Unmarshal(request.Payload, &payload); err != nil {
		return Envelope{}, err
	}
	capability, ok := StreamCapabilityFromContext(ctx)
	if !ok {
		return Envelope{}, ErrStreamUnavailable
	}
	reader, err := capability.OpenRead(ctx, payload.Body)
	if err != nil {
		return Envelope{}, err
	}
	defer reader.Close()
	dispatcher.body, err = io.ReadAll(reader)
	if err != nil {
		return Envelope{}, err
	}
	response, err := json.Marshal(FileCreateResponse{FileID: "file-1"})
	return Envelope{OperationID: request.OperationID, Version: request.Version, Payload: response}, err
}

func TestFileCreateVerifiedUsesScopedStreamAndTypedOperation(t *testing.T) {
	dispatcher := &uploadDispatcher{}
	streams := &uploadStreams{}
	body := []byte("file body that must not be serialized into the operation envelope")
	response, err := CallFileCreateVerified(context.Background(), dispatcher, streams, FileCreateVerifiedInput{
		RequestKey: "request-1", Groups: []string{"content"}, Metadata: map[string]string{"kind": "image"},
		ContentType: "image/png", Size: int64(len(body)), SHA256: "checksum",
	}, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if response.FileID != "file-1" || string(dispatcher.body) != string(body) {
		t.Fatalf("response/body = %#v/%q", response, dispatcher.body)
	}
	if dispatcher.request.OperationID != "core.files.create-verified" || dispatcher.request.Version != 1 {
		t.Fatalf("operation = %s/v%d", dispatcher.request.OperationID, dispatcher.request.Version)
	}
	if bytes.Contains(dispatcher.request.Payload, body) {
		t.Fatal("file bytes were serialized into the DataBus envelope")
	}
	if !streams.closed {
		t.Fatal("scoped upload stream was not closed after the operation")
	}
}
