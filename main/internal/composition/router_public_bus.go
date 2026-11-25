package composition

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"

	formapi "github.com/Zubrium/cdn/engines/forms/presentation"
	loaderapi "github.com/Zubrium/cdn/engines/loader/features/files/pkg"
	"github.com/Zubrium/cdn/main/internal/bus"
	"github.com/Zubrium/cdn/pkg/contracts"
)

// Public Router operations are bound on a dedicated process-local DataBus.
// Module identity is selected here from the compiled operation key, attached
// outside the JSON envelope, and checked by the provider. No visitor payload
// can name a module, class, credential or engine.
const (
	routerPublicRouterModuleID = "engine.router"
	routerPublicFormsModuleID  = "engine.forms"
	routerPublicLoaderModuleID = "engine.loader"
)

type routerPublicModuleKey struct{}

var errRouterPublicInvalid = errors.New("invalid public Router DataBus dependencies")

type routerPublicModuleDispatcher struct {
	dispatcher contracts.Dispatcher
	allowed    map[contracts.Key]struct{}
}

func (dispatcher routerPublicModuleDispatcher) Dispatch(ctx context.Context, request contracts.Envelope) (contracts.Envelope, error) {
	if isNilRouterPublicValue(dispatcher.dispatcher) || len(dispatcher.allowed) == 0 {
		return contracts.Envelope{}, contracts.ErrDispatcherUnavailable
	}
	key := request.Key()
	if _, allowed := dispatcher.allowed[key]; !allowed {
		return contracts.Envelope{}, bus.ErrOperationNotAllowedForModule
	}
	moduleID := routerPublicModuleForOperation(key.ID)
	if moduleID == "" {
		return contracts.Envelope{}, bus.ErrInvalidModuleIdentity
	}
	return dispatcher.dispatcher.Dispatch(context.WithValue(ctx, routerPublicModuleKey{}, moduleID), request)
}

func routerPublicModuleForOperation(operationID string) string {
	switch {
	case strings.HasPrefix(operationID, "forms.public-"):
		return routerPublicFormsModuleID
	case strings.HasPrefix(operationID, "loader.public-"):
		return routerPublicLoaderModuleID
	default:
		return ""
	}
}

func routerPublicModuleID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	moduleID, _ := ctx.Value(routerPublicModuleKey{}).(string)
	return moduleID
}

type routerPublicModuleGuard struct{}

func (routerPublicModuleGuard) requireModule(ctx context.Context, expected string) error {
	if routerPublicModuleID(ctx) != expected {
		if ctx != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		return contracts.ErrPublicUnauthorized
	}
	return nil
}

type routerPublicFormsEngine interface {
	ValidatePublicForm(context.Context, formapi.PublicFormBinding) error
	ValidatePublicUploadRoute(context.Context, formapi.PublicUploadRouteBinding) error
	CreateVisitorSession(context.Context, formapi.CreateVisitorSessionRequest) (formapi.VisitorSessionCredential, error)
	GetVisitorSession(context.Context, formapi.GetVisitorSessionRequest) (formapi.VisitorSession, error)
	BeginVisitorUpload(context.Context, formapi.BeginVisitorUploadRequest) (formapi.VisitorUploadReservation, error)
	CompleteVisitorUpload(context.Context, formapi.CompleteVisitorUploadRequest) (formapi.VisitorAttachment, error)
	AbortVisitorUpload(context.Context, formapi.AbortVisitorUploadRequest) error
	SubmitVisitorResponse(context.Context, formapi.SubmitVisitorResponseRequest) (formapi.VisitorSubmissionReceipt, error)
	CancelVisitorSession(context.Context, formapi.CancelVisitorSessionRequest) error
}

type routerPublicLoaderAPI interface {
	Authenticate(context.Context, string) (context.Context, error)
	BeginUpload(context.Context, loaderapi.BeginUploadInput) (loaderapi.UploadSession, error)
	PutUploadPart(context.Context, string, int, int64, string, io.Reader) (loaderapi.UploadSession, error)
	CommitUpload(context.Context, string) (loaderapi.File, error)
	AbortUpload(context.Context, string) error
	StatFile(context.Context, string) (loaderapi.File, error)
	DeleteFile(context.Context, string, int64) error
	GetFileSet(context.Context, string) (loaderapi.FileSet, error)
	AddFileToSet(context.Context, string, string, int64) (loaderapi.FileSet, error)
}

type routerPublicStaging interface {
	PutStagedObject(context.Context, contracts.AuthorizationSubject, string, []string, int64, string, io.Reader) error
	ReadStagedObject(context.Context, contracts.AuthorizationSubject, string) (io.ReadCloser, int64, string, error)
	PurgeStagedObject(context.Context, string) error
}

type routerPublicServices struct {
	Forms   contracts.PublicFormsVisitor
	Loader  contracts.PublicLoaderClient
	Streams func() contracts.PublicUploadStreams
	close   func(context.Context) error
}

func (services *routerPublicServices) Close(ctx context.Context) error {
	if services == nil || services.close == nil {
		return nil
	}
	return services.close(ctx)
}

type routerPublicAuthorization interface {
	Authenticate(context.Context, string) (contracts.AuthorizationSubject, contracts.Principal, error)
	RequireGroupLevel(context.Context, contracts.AuthorizationSubject, []string, contracts.AccessLevel) error
}

type routerPublicDependencies struct {
	forms         routerPublicFormsEngine
	loader        routerPublicLoaderAPI
	staging       routerPublicStaging
	authorization routerPublicAuthorization
	credential    string
	partSize      int64
	maxFile       int64
	maxParts      int
	maxInFlight   int
}

func newRouterPublicServices(deps routerPublicDependencies) (*routerPublicServices, error) {
	if deps.credential == "" || deps.partSize <= 0 || deps.maxFile <= 0 || deps.maxParts <= 0 || deps.maxInFlight <= 0 {
		return nil, errRouterPublicInvalid
	}
	if isNilRouterPublicValue(deps.loader) || isNilRouterPublicValue(deps.staging) || isNilRouterPublicValue(deps.authorization) {
		return nil, errRouterPublicInvalid
	}
	keys := routerPublicOperationKeys()
	dispatcher, err := bus.NewAllowlisted(bus.ConcurrencyLimits{MaxInFlight: deps.maxInFlight}, keys...)
	if err != nil {
		return nil, fmt.Errorf("configure public Router bus: %w", err)
	}
	forms := deps.forms
	if isNilRouterPublicValue(forms) {
		forms = routerPublicFormsUnavailable{}
	}
	if err := routerPublicRegisterForms(dispatcher, forms); err != nil {
		return nil, err
	}
	if err := routerPublicRegisterLoader(dispatcher, deps); err != nil {
		return nil, err
	}
	if err := dispatcher.EnableScopedStreams(); err != nil {
		return nil, fmt.Errorf("enable public Router scoped streams: %w", err)
	}
	if err := dispatcher.Seal(); err != nil {
		return nil, fmt.Errorf("seal public Router bus: %w", err)
	}
	allowed := make(map[contracts.Key]struct{}, len(keys))
	for _, key := range keys {
		allowed[key] = struct{}{}
	}
	moduleDispatcher := routerPublicModuleDispatcher{dispatcher: dispatcher, allowed: allowed}
	formsClient, err := contracts.NewPublicFormsVisitor(moduleDispatcher)
	if err != nil {
		return nil, fmt.Errorf("configure public Forms DataBus client: %w", err)
	}
	loader, err := contracts.NewPublicLoaderClient(moduleDispatcher)
	if err != nil {
		return nil, fmt.Errorf("configure public Loader DataBus client: %w", err)
	}
	return &routerPublicServices{
		Forms:   formsClient,
		Loader:  loader,
		Streams: func() contracts.PublicUploadStreams { return bus.NewScopedStreams() },
		close:   dispatcher.Close,
	}, nil
}

func routerPublicOperationKeys() []contracts.Key {
	return []contracts.Key{
		contracts.PublicFormValidateOperation().Key,
		contracts.PublicFormValidateUploadOperation().Key,
		contracts.PublicFormCreateSessionOperation().Key,
		contracts.PublicFormGetSessionOperation().Key,
		contracts.PublicFormCancelSessionOperation().Key,
		contracts.PublicFormBeginUploadOperation().Key,
		contracts.PublicFormCompleteUploadOperation().Key,
		contracts.PublicFormAbortUploadOperation().Key,
		contracts.PublicFormSubmitOperation().Key,
		contracts.PublicLoaderBeginUploadOperation().Key,
		contracts.PublicLoaderPutPartOperation().Key,
		contracts.PublicLoaderCommitOperation().Key,
		contracts.PublicLoaderAbortOperation().Key,
		contracts.PublicLoaderStatFileOperation().Key,
		contracts.PublicLoaderDeleteFileOperation().Key,
		contracts.PublicLoaderGetFileSetOperation().Key,
		contracts.PublicLoaderAddFileSetOperation().Key,
	}
}

func routerPublicRegisterForms(dispatcher *bus.Dispatcher, api routerPublicFormsEngine) error {
	provider := &routerPublicFormsProvider{api: api}
	if err := bus.Register(dispatcher, contracts.PublicFormValidateOperation(), provider.validateForm); err != nil {
		return fmt.Errorf("register %s: %w", contracts.PublicFormValidateOperationID, err)
	}
	if err := bus.Register(dispatcher, contracts.PublicFormValidateUploadOperation(), provider.validateUploadRoute); err != nil {
		return fmt.Errorf("register %s: %w", contracts.PublicFormValidateUploadOperationID, err)
	}
	if err := bus.Register(dispatcher, contracts.PublicFormCreateSessionOperation(), provider.createSession); err != nil {
		return fmt.Errorf("register %s: %w", contracts.PublicFormCreateSessionOperationID, err)
	}
	if err := bus.Register(dispatcher, contracts.PublicFormGetSessionOperation(), provider.getSession); err != nil {
		return fmt.Errorf("register %s: %w", contracts.PublicFormGetSessionOperationID, err)
	}
	if err := bus.Register(dispatcher, contracts.PublicFormCancelSessionOperation(), provider.cancelSession); err != nil {
		return fmt.Errorf("register %s: %w", contracts.PublicFormCancelSessionOperationID, err)
	}
	if err := bus.Register(dispatcher, contracts.PublicFormBeginUploadOperation(), provider.beginUpload); err != nil {
		return fmt.Errorf("register %s: %w", contracts.PublicFormBeginUploadOperationID, err)
	}
	if err := bus.Register(dispatcher, contracts.PublicFormCompleteUploadOperation(), provider.completeUpload); err != nil {
		return fmt.Errorf("register %s: %w", contracts.PublicFormCompleteUploadOperationID, err)
	}
	if err := bus.Register(dispatcher, contracts.PublicFormAbortUploadOperation(), provider.abortUpload); err != nil {
		return fmt.Errorf("register %s: %w", contracts.PublicFormAbortUploadOperationID, err)
	}
	if err := bus.Register(dispatcher, contracts.PublicFormSubmitOperation(), provider.submitResponse); err != nil {
		return fmt.Errorf("register %s: %w", contracts.PublicFormSubmitOperationID, err)
	}
	return nil
}

func routerPublicRegisterLoader(dispatcher *bus.Dispatcher, deps routerPublicDependencies) error {
	provider := &routerPublicLoaderProvider{
		api: deps.loader, staging: deps.staging, authorization: deps.authorization, credential: deps.credential,
		partSize: deps.partSize, maxFileBytes: deps.maxFile, maxParts: deps.maxParts,
	}
	if err := bus.Register(dispatcher, contracts.PublicLoaderBeginUploadOperation(), provider.beginUpload); err != nil {
		return fmt.Errorf("register %s: %w", contracts.PublicLoaderBeginUploadOperationID, err)
	}
	if err := bus.Register(dispatcher, contracts.PublicLoaderPutPartOperation(), provider.putPart); err != nil {
		return fmt.Errorf("register %s: %w", contracts.PublicLoaderPutPartOperationID, err)
	}
	if err := bus.Register(dispatcher, contracts.PublicLoaderCommitOperation(), provider.commitUpload); err != nil {
		return fmt.Errorf("register %s: %w", contracts.PublicLoaderCommitOperationID, err)
	}
	if err := bus.Register(dispatcher, contracts.PublicLoaderAbortOperation(), provider.abortUpload); err != nil {
		return fmt.Errorf("register %s: %w", contracts.PublicLoaderAbortOperationID, err)
	}
	if err := bus.Register(dispatcher, contracts.PublicLoaderStatFileOperation(), provider.statFile); err != nil {
		return fmt.Errorf("register %s: %w", contracts.PublicLoaderStatFileOperationID, err)
	}
	if err := bus.Register(dispatcher, contracts.PublicLoaderDeleteFileOperation(), provider.deleteFile); err != nil {
		return fmt.Errorf("register %s: %w", contracts.PublicLoaderDeleteFileOperationID, err)
	}
	if err := bus.Register(dispatcher, contracts.PublicLoaderGetFileSetOperation(), provider.getFileSet); err != nil {
		return fmt.Errorf("register %s: %w", contracts.PublicLoaderGetFileSetOperationID, err)
	}
	if err := bus.Register(dispatcher, contracts.PublicLoaderAddFileSetOperation(), provider.addFileToSet); err != nil {
		return fmt.Errorf("register %s: %w", contracts.PublicLoaderAddFileSetOperationID, err)
	}
	return nil
}

func isNilRouterPublicValue(value any) bool {
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

var _ contracts.Dispatcher = routerPublicModuleDispatcher{}
