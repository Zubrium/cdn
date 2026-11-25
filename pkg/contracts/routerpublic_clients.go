package contracts

import (
	"context"
	"io"
	"reflect"
)

// PublicUploadStreams is the narrow stream registrar handed to Router's public
// upload operation. It has no byte payload in the JSON envelope; the provider
// receives one StreamRef and may open it exactly once.
type PublicUploadStreams interface {
	ScopedStreamCapability
	AddReader(io.ReadCloser) (StreamRef, error)
}

// PublicStreamBody is the HTTP-boundary carrier for a registered body stream.
// The Router adapter wraps r.Body with it and passes only the StreamRef through
// the DataBus payload.
type PublicStreamBody interface {
	io.Reader
	StreamReference() StreamRef
}

// PublicStreamReference extracts a registered stream reference from a request
// body without consuming any bytes.
func PublicStreamReference(body io.Reader) (StreamRef, bool) {
	carrier, ok := body.(PublicStreamBody)
	if !ok {
		return "", false
	}
	ref := carrier.StreamReference()
	return ref, ref != ""
}

type PublicFormsVisitor interface {
	ValidatePublicForm(context.Context, PublicFormBinding) error
	ValidatePublicUploadRoute(context.Context, PublicUploadRouteBinding) error
	CreateVisitorSession(context.Context, PublicFormBinding) (PublicVisitorSessionCredential, error)
	GetVisitorSession(context.Context, PublicGetVisitorSessionRequest) (PublicVisitorSession, error)
	CancelVisitorSession(context.Context, PublicCancelVisitorSessionRequest) error
	BeginVisitorUpload(context.Context, PublicBeginVisitorUploadRequest) (PublicVisitorUploadReservation, error)
	CompleteVisitorUpload(context.Context, PublicCompleteVisitorUploadRequest) (PublicVisitorAttachment, error)
	AbortVisitorUpload(context.Context, PublicAbortVisitorUploadRequest) error
	SubmitVisitorResponse(context.Context, PublicSubmitVisitorResponseRequest) (PublicVisitorSubmissionReceipt, error)
}

type publicFormsClient struct{ dispatcher Dispatcher }

func NewPublicFormsVisitor(dispatcher Dispatcher) (PublicFormsVisitor, error) {
	if isNilPublicContractValue(dispatcher) {
		return nil, ErrDispatcherUnavailable
	}
	return &publicFormsClient{dispatcher: dispatcher}, nil
}

func (client *publicFormsClient) ValidatePublicForm(ctx context.Context, binding PublicFormBinding) error {
	_, err := Call(ctx, client.dispatcher, PublicFormValidateOperation(), binding)
	return err
}

func (client *publicFormsClient) ValidatePublicUploadRoute(ctx context.Context, binding PublicUploadRouteBinding) error {
	_, err := Call(ctx, client.dispatcher, PublicFormValidateUploadOperation(), binding)
	return err
}

func (client *publicFormsClient) CreateVisitorSession(ctx context.Context, binding PublicFormBinding) (PublicVisitorSessionCredential, error) {
	return Call(ctx, client.dispatcher, PublicFormCreateSessionOperation(), binding)
}

func (client *publicFormsClient) GetVisitorSession(ctx context.Context, request PublicGetVisitorSessionRequest) (PublicVisitorSession, error) {
	return Call(ctx, client.dispatcher, PublicFormGetSessionOperation(), request)
}

func (client *publicFormsClient) CancelVisitorSession(ctx context.Context, request PublicCancelVisitorSessionRequest) error {
	_, err := Call(ctx, client.dispatcher, PublicFormCancelSessionOperation(), request)
	return err
}

func (client *publicFormsClient) BeginVisitorUpload(ctx context.Context, request PublicBeginVisitorUploadRequest) (PublicVisitorUploadReservation, error) {
	return Call(ctx, client.dispatcher, PublicFormBeginUploadOperation(), request)
}

func (client *publicFormsClient) CompleteVisitorUpload(ctx context.Context, request PublicCompleteVisitorUploadRequest) (PublicVisitorAttachment, error) {
	return Call(ctx, client.dispatcher, PublicFormCompleteUploadOperation(), request)
}

func (client *publicFormsClient) AbortVisitorUpload(ctx context.Context, request PublicAbortVisitorUploadRequest) error {
	_, err := Call(ctx, client.dispatcher, PublicFormAbortUploadOperation(), request)
	return err
}

func (client *publicFormsClient) SubmitVisitorResponse(ctx context.Context, request PublicSubmitVisitorResponseRequest) (PublicVisitorSubmissionReceipt, error) {
	return Call(ctx, client.dispatcher, PublicFormSubmitOperation(), request)
}

type PublicLoaderClient interface {
	BeginUpload(context.Context, PublicLoaderBeginUploadRequest, PublicUploadStreams) (PublicLoaderUploadSession, error)
	PutPart(context.Context, PublicLoaderPutPartRequest) (PublicLoaderUploadSession, error)
	Commit(context.Context, PublicLoaderCommitRequest) (PublicLoaderFile, error)
	Abort(context.Context, PublicLoaderAbortRequest) error
	StatFile(context.Context, PublicLoaderStatFileRequest) (PublicLoaderFile, error)
	DeleteFile(context.Context, PublicLoaderDeleteFileRequest) error
	GetFileSet(context.Context, PublicLoaderGetFileSetRequest) (PublicLoaderFileSet, error)
	AddFileToSet(context.Context, PublicLoaderAddFileSetRequest) (PublicLoaderFileSet, error)
}

type publicLoaderClient struct{ dispatcher Dispatcher }

func NewPublicLoaderClient(dispatcher Dispatcher) (PublicLoaderClient, error) {
	if isNilPublicContractValue(dispatcher) {
		return nil, ErrDispatcherUnavailable
	}
	return &publicLoaderClient{dispatcher: dispatcher}, nil
}

func (client *publicLoaderClient) BeginUpload(ctx context.Context, request PublicLoaderBeginUploadRequest, streams PublicUploadStreams) (PublicLoaderUploadSession, error) {
	return CallWithStreams(ctx, client.dispatcher, PublicLoaderBeginUploadOperation(), request, streams)
}

func (client *publicLoaderClient) PutPart(ctx context.Context, request PublicLoaderPutPartRequest) (PublicLoaderUploadSession, error) {
	return Call(ctx, client.dispatcher, PublicLoaderPutPartOperation(), request)
}

func (client *publicLoaderClient) Commit(ctx context.Context, request PublicLoaderCommitRequest) (PublicLoaderFile, error) {
	return Call(ctx, client.dispatcher, PublicLoaderCommitOperation(), request)
}

func (client *publicLoaderClient) Abort(ctx context.Context, request PublicLoaderAbortRequest) error {
	_, err := Call(ctx, client.dispatcher, PublicLoaderAbortOperation(), request)
	return err
}

func (client *publicLoaderClient) StatFile(ctx context.Context, request PublicLoaderStatFileRequest) (PublicLoaderFile, error) {
	return Call(ctx, client.dispatcher, PublicLoaderStatFileOperation(), request)
}

func (client *publicLoaderClient) DeleteFile(ctx context.Context, request PublicLoaderDeleteFileRequest) error {
	_, err := Call(ctx, client.dispatcher, PublicLoaderDeleteFileOperation(), request)
	return err
}

func (client *publicLoaderClient) GetFileSet(ctx context.Context, request PublicLoaderGetFileSetRequest) (PublicLoaderFileSet, error) {
	return Call(ctx, client.dispatcher, PublicLoaderGetFileSetOperation(), request)
}

func (client *publicLoaderClient) AddFileToSet(ctx context.Context, request PublicLoaderAddFileSetRequest) (PublicLoaderFileSet, error) {
	return Call(ctx, client.dispatcher, PublicLoaderAddFileSetOperation(), request)
}

func isNilPublicContractValue(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
