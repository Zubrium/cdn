package router

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"reflect"
	"sort"
	"strings"

	engine "github.com/Zubrium/cdn/engines/router/presentation"
)

// PublicOperationRegistry is an immutable catalog of operations linked into
// the selected build profile. Route data can select an ID, but cannot install
// callbacks or network destinations.
type PublicOperationRegistry struct {
	operations map[string]*registeredPublicOperation
}

type registeredPublicOperation struct {
	implementation engine.PublicOperation
	descriptor     engine.PublicOperationDescriptor
}

type contextualRouteConfigValidator interface {
	ValidateRouteConfigWithContext(context.Context, json.RawMessage) error
}

func NewPublicOperationRegistry(operations ...engine.PublicOperation) (*PublicOperationRegistry, error) {
	registry := &PublicOperationRegistry{operations: make(map[string]*registeredPublicOperation, len(operations))}
	for _, operation := range operations {
		if isNilPublicOperation(operation) {
			return nil, engine.ErrInvalidPublicOperation
		}
		descriptor := clonePublicOperationDescriptor(operation.Descriptor())
		if err := validatePublicOperationDescriptor(descriptor); err != nil {
			return nil, fmt.Errorf("%w: %s", engine.ErrInvalidPublicOperation, descriptor.ID)
		}
		if _, exists := registry.operations[descriptor.ID]; exists {
			return nil, fmt.Errorf("%w: duplicate ID %q", engine.ErrInvalidPublicOperation, descriptor.ID)
		}
		registry.operations[descriptor.ID] = &registeredPublicOperation{
			implementation: operation,
			descriptor:     descriptor,
		}
	}
	return registry, nil
}

func (registry *PublicOperationRegistry) ResolvePublicOperation(ctx context.Context, id string) (engine.PublicOperation, error) {
	if registry == nil || ctx == nil {
		return nil, engine.ErrPublicOperationNotRegistered
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	operation, exists := registry.operations[id]
	if !exists {
		return nil, fmt.Errorf("%w: %q", engine.ErrPublicOperationNotRegistered, id)
	}
	return operation, nil
}

func (registry *PublicOperationRegistry) ListPublicOperations(ctx context.Context) ([]engine.PublicOperationDescriptor, error) {
	if registry == nil || ctx == nil {
		return nil, engine.ErrPublicOperationNotRegistered
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	descriptors := make([]engine.PublicOperationDescriptor, 0, len(registry.operations))
	for _, operation := range registry.operations {
		descriptors = append(descriptors, clonePublicOperationDescriptor(operation.descriptor))
	}
	sort.Slice(descriptors, func(i, j int) bool { return descriptors[i].ID < descriptors[j].ID })
	return descriptors, nil
}

func (registry *PublicOperationRegistry) ValidatePublicOperationTarget(ctx context.Context, target engine.RouteTarget) error {
	if err := target.Validate(); err != nil {
		return err
	}
	if target.Kind != engine.TargetRegisteredOperation {
		return nil
	}
	operation, err := registry.ResolvePublicOperation(ctx, target.OperationID)
	if err != nil {
		return err
	}
	config := append(json.RawMessage(nil), target.OperationConfig...)
	var validationErr error
	if contextual, ok := operation.(contextualRouteConfigValidator); ok {
		validationErr = contextual.ValidateRouteConfigWithContext(ctx, config)
	} else {
		validationErr = operation.ValidateRouteConfig(config)
	}
	if validationErr != nil {
		if errors.Is(validationErr, engine.ErrForbidden) || errors.Is(validationErr, engine.ErrUnauthorized) ||
			errors.Is(validationErr, engine.ErrUnavailable) || errors.Is(validationErr, context.Canceled) || errors.Is(validationErr, context.DeadlineExceeded) {
			return validationErr
		}
		return fmt.Errorf("%w: invalid config for %s: %w", engine.ErrInvalidTarget, target.OperationID, validationErr)
	}
	return nil
}

func (operation *registeredPublicOperation) Descriptor() engine.PublicOperationDescriptor {
	return clonePublicOperationDescriptor(operation.descriptor)
}

func (operation *registeredPublicOperation) ValidateRouteConfig(config json.RawMessage) error {
	return operation.implementation.ValidateRouteConfig(append(json.RawMessage(nil), config...))
}

func (operation *registeredPublicOperation) ValidateRouteConfigWithContext(ctx context.Context, config json.RawMessage) error {
	copy := append(json.RawMessage(nil), config...)
	if contextual, ok := operation.implementation.(contextualRouteConfigValidator); ok {
		return contextual.ValidateRouteConfigWithContext(ctx, copy)
	}
	return operation.implementation.ValidateRouteConfig(copy)
}

func (operation *registeredPublicOperation) Execute(ctx context.Context, config json.RawMessage, request engine.PublicOperationRequest) (engine.DeliveryResponse, error) {
	return operation.implementation.Execute(ctx, append(json.RawMessage(nil), config...), request)
}

// DispatchPublicOperation validates a persisted route against the static
// catalog and executes only the registered local capability. The body remains
// streamed and fails with ErrTooLarge if the operation reads past its limit.
// Upload operations must consume the bounded body fully before committing it.
func DispatchPublicOperation(ctx context.Context, catalog engine.PublicOperationCatalog, target engine.RouteTarget, request engine.PublicOperationRequest) (engine.DeliveryResponse, error) {
	if ctx == nil || catalog == nil || target.Validate() != nil || target.Kind != engine.TargetRegisteredOperation {
		return engine.DeliveryResponse{}, engine.ErrInvalidTarget
	}
	if err := ctx.Err(); err != nil {
		return engine.DeliveryResponse{}, err
	}
	operation, err := catalog.ResolvePublicOperation(ctx, target.OperationID)
	if err != nil {
		return engine.DeliveryResponse{}, err
	}
	if err := operation.ValidateRouteConfig(append(json.RawMessage(nil), target.OperationConfig...)); err != nil {
		return engine.DeliveryResponse{}, fmt.Errorf("%w: invalid config for %s: %w", engine.ErrInvalidTarget, target.OperationID, err)
	}
	descriptor := operation.Descriptor()
	if err := validatePublicOperationDescriptor(descriptor); err != nil {
		return engine.DeliveryResponse{}, engine.ErrInvalidPublicOperation
	}
	if !containsString(descriptor.Methods, request.Method) {
		return engine.DeliveryResponse{}, engine.ErrInvalid
	}
	if request.ContentLength < -1 || request.ContentLength > descriptor.MaxRequestBytes {
		if request.ContentLength > descriptor.MaxRequestBytes {
			return engine.DeliveryResponse{}, engine.ErrTooLarge
		}
		return engine.DeliveryResponse{}, engine.ErrInvalid
	}
	if request.IdempotencyKey != "" && !validIdempotencyKey(request.IdempotencyKey) {
		return engine.DeliveryResponse{}, engine.ErrInvalid
	}
	if request.ContentLength == 0 {
		request.Body = http.NoBody
	} else {
		if request.Body == nil {
			return engine.DeliveryResponse{}, engine.ErrInvalid
		}
		mediaType, normalized, parseErr := normalizeOperationContentType(request.ContentType)
		if parseErr != nil || !containsMediaType(descriptor.AllowedContentTypes, mediaType) {
			return engine.DeliveryResponse{}, engine.ErrInvalid
		}
		request.ContentType = normalized
		request.Body = &boundedOperationBody{source: request.Body, remaining: descriptor.MaxRequestBytes}
	}
	return operation.Execute(ctx, append(json.RawMessage(nil), target.OperationConfig...), request)
}

type boundedOperationBody struct {
	source    io.Reader
	remaining int64
}

func (body *boundedOperationBody) Read(buffer []byte) (int, error) {
	if len(buffer) == 0 {
		return 0, nil
	}
	if body.remaining > 0 {
		if int64(len(buffer)) > body.remaining {
			buffer = buffer[:body.remaining]
		}
		read, err := body.source.Read(buffer)
		body.remaining -= int64(read)
		return read, err
	}
	var probe [1]byte
	emptyReads := 0
	for {
		read, err := body.source.Read(probe[:])
		if read > 0 {
			return 0, engine.ErrTooLarge
		}
		if err != nil {
			return 0, err
		}
		emptyReads++
		if emptyReads >= 100 {
			return 0, io.ErrNoProgress
		}
	}
}

func normalizeOperationContentType(value string) (string, string, error) {
	if value == "" || len(value) > 1024 || strings.ContainsAny(value, "\r\n") {
		return "", "", engine.ErrInvalid
	}
	mediaType, parameters, err := mime.ParseMediaType(value)
	if err != nil || strings.Contains(mediaType, "*") {
		return "", "", engine.ErrInvalid
	}
	normalized := mime.FormatMediaType(mediaType, parameters)
	if normalized == "" {
		return "", "", engine.ErrInvalid
	}
	return mediaType, normalized, nil
}

func containsMediaType(allowed []string, requested string) bool {
	for _, contentType := range allowed {
		mediaType, _, err := mime.ParseMediaType(contentType)
		if err == nil && mediaType == requested {
			return true
		}
	}
	return false
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func clonePublicOperationDescriptor(descriptor engine.PublicOperationDescriptor) engine.PublicOperationDescriptor {
	descriptor.Methods = append([]string(nil), descriptor.Methods...)
	descriptor.AllowedContentTypes = append([]string(nil), descriptor.AllowedContentTypes...)
	return descriptor
}

func validatePublicOperationDescriptor(descriptor engine.PublicOperationDescriptor) error {
	if !engine.ValidPublicOperationID(descriptor.ID) || descriptor.ContractVersion < 1 || descriptor.MaxRequestBytes < 1 ||
		len(descriptor.Methods) == 0 || len(descriptor.Methods) > 6 || len(descriptor.AllowedContentTypes) == 0 || len(descriptor.AllowedContentTypes) > 32 {
		return engine.ErrInvalidPublicOperation
	}

	methods := make(map[string]struct{}, len(descriptor.Methods))
	for _, method := range descriptor.Methods {
		if !validPublicOperationMethod(method) {
			return engine.ErrInvalidPublicOperation
		}
		if _, duplicate := methods[method]; duplicate {
			return engine.ErrInvalidPublicOperation
		}
		methods[method] = struct{}{}
	}

	contentTypes := make(map[string]struct{}, len(descriptor.AllowedContentTypes))
	for _, contentType := range descriptor.AllowedContentTypes {
		if strings.TrimSpace(contentType) != contentType || contentType == "" || strings.ContainsAny(contentType, "*\r\n") {
			return engine.ErrInvalidPublicOperation
		}
		mediaType, params, err := mime.ParseMediaType(contentType)
		if err != nil || len(params) != 0 || strings.Contains(mediaType, "*") {
			return engine.ErrInvalidPublicOperation
		}
		if _, duplicate := contentTypes[mediaType]; duplicate {
			return engine.ErrInvalidPublicOperation
		}
		contentTypes[mediaType] = struct{}{}
	}
	return nil
}

func validPublicOperationMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func isNilPublicOperation(operation engine.PublicOperation) bool {
	if operation == nil {
		return true
	}
	// The generated catalog is normally populated with pointers. A typed nil
	// implementation still satisfies the interface and must fail at startup.
	value := reflect.ValueOf(operation)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
