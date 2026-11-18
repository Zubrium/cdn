package router

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	engine "github.com/Zubrium/cdn/engines/router/presentation"
)

type testPublicOperation struct {
	descriptor engine.PublicOperationDescriptor
	configErr  error
	executeErr error
	request    engine.PublicOperationRequest
	config     json.RawMessage
	body       string
	response   engine.DeliveryResponse
}

func (o *testPublicOperation) Descriptor() engine.PublicOperationDescriptor { return o.descriptor }
func (o *testPublicOperation) ValidateRouteConfig(json.RawMessage) error    { return o.configErr }
func (o *testPublicOperation) Execute(_ context.Context, config json.RawMessage, request engine.PublicOperationRequest) (engine.DeliveryResponse, error) {
	o.config = append(json.RawMessage(nil), config...)
	o.request = request
	if request.Body != nil {
		body, err := io.ReadAll(request.Body)
		o.body = string(body)
		if err != nil {
			return engine.DeliveryResponse{}, err
		}
	}
	return o.response, o.executeErr
}

func TestDispatchPublicOperationValidatesAndStreamsRequest(t *testing.T) {
	op := validPublicOperation("public.form-upload")
	op.descriptor.MaxRequestBytes = 8
	registry, err := NewPublicOperationRegistry(op)
	if err != nil {
		t.Fatal(err)
	}
	target := engine.RouteTarget{Kind: engine.TargetRegisteredOperation, OperationID: op.descriptor.ID, OperationConfig: json.RawMessage(`{"max_bytes":8}`)}
	response, err := DispatchPublicOperation(context.Background(), registry, target, engine.PublicOperationRequest{
		Method: "POST", ContentType: "application/octet-stream; charset=binary", ContentLength: -1,
		IdempotencyKey: "public-upload-request-0001", Body: strings.NewReader("filedata"),
	})
	if err != nil || response.StatusCode != 0 {
		t.Fatalf("dispatch failed: response=%+v err=%v", response, err)
	}
	if op.body != "filedata" || op.request.ContentType != "application/octet-stream; charset=binary" || string(op.config) != `{"max_bytes":8}` {
		t.Fatalf("operation did not receive the validated streamed request: body=%q request=%+v config=%s", op.body, op.request, op.config)
	}
}

func TestDispatchPublicOperationRejectsOversizedAndDisallowedRequests(t *testing.T) {
	for _, test := range []struct {
		name    string
		request engine.PublicOperationRequest
		wantErr error
	}{
		{name: "declared size", request: engine.PublicOperationRequest{Method: "POST", ContentType: "application/octet-stream", ContentLength: 9, Body: strings.NewReader("123456789")}, wantErr: engine.ErrTooLarge},
		{name: "streamed size", request: engine.PublicOperationRequest{Method: "POST", ContentType: "application/octet-stream", ContentLength: -1, Body: strings.NewReader("123456789")}, wantErr: engine.ErrTooLarge},
		{name: "method", request: engine.PublicOperationRequest{Method: "TRACE", ContentLength: 0}, wantErr: engine.ErrInvalid},
		{name: "content type", request: engine.PublicOperationRequest{Method: "POST", ContentType: "text/plain", ContentLength: 1, Body: strings.NewReader("x")}, wantErr: engine.ErrInvalid},
	} {
		t.Run(test.name, func(t *testing.T) {
			op := validPublicOperation("public.form-upload")
			op.descriptor.MaxRequestBytes = 8
			registry, err := NewPublicOperationRegistry(op)
			if err != nil {
				t.Fatal(err)
			}
			target := engine.RouteTarget{Kind: engine.TargetRegisteredOperation, OperationID: op.descriptor.ID, OperationConfig: json.RawMessage(`{}`)}
			if _, err := DispatchPublicOperation(context.Background(), registry, target, test.request); !errors.Is(err, test.wantErr) {
				t.Fatalf("dispatch returned %v, want %v", err, test.wantErr)
			}
		})
	}
}

func validPublicOperation(id string) *testPublicOperation {
	return &testPublicOperation{descriptor: engine.PublicOperationDescriptor{
		ID: id, ContractVersion: 1, Methods: []string{"POST"}, MaxRequestBytes: 1 << 20,
		AllowedContentTypes: []string{"application/octet-stream"},
	}}
}

func TestPublicOperationRegistryIsStaticAndRejectsUnknownIDs(t *testing.T) {
	op := validPublicOperation("public.form-upload")
	registry, err := NewPublicOperationRegistry(op)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := registry.ResolvePublicOperation(context.Background(), "public.form-upload")
	if err != nil || resolved == nil || resolved.Descriptor().ID != op.descriptor.ID {
		t.Fatalf("registered operation did not resolve: operation=%v err=%v", resolved, err)
	}
	if _, err := registry.ResolvePublicOperation(context.Background(), "public.uncompiled"); !errors.Is(err, engine.ErrPublicOperationNotRegistered) {
		t.Fatalf("unknown operation returned %v", err)
	}
	if _, err := NewPublicOperationRegistry(op, validPublicOperation("public.form-upload")); !errors.Is(err, engine.ErrInvalidPublicOperation) {
		t.Fatalf("duplicate operation ID returned %v", err)
	}
}

func TestPublicOperationRegistryListsImmutableSortedDescriptors(t *testing.T) {
	first := validPublicOperation("public.z-upload")
	second := validPublicOperation("public.a-upload")
	registry, err := NewPublicOperationRegistry(first, second)
	if err != nil {
		t.Fatal(err)
	}

	descriptors, err := registry.ListPublicOperations(context.Background())
	if err != nil || len(descriptors) != 2 || descriptors[0].ID != "public.a-upload" || descriptors[1].ID != "public.z-upload" {
		t.Fatalf("operation catalog is not listed deterministically: descriptors=%+v err=%v", descriptors, err)
	}
	descriptors[0].Methods[0] = "TRACE"
	descriptors[0].AllowedContentTypes[0] = "text/html"
	secondRead, err := registry.ListPublicOperations(context.Background())
	if err != nil || secondRead[0].Methods[0] != "POST" || secondRead[0].AllowedContentTypes[0] != "application/octet-stream" {
		t.Fatalf("caller mutation changed the registered catalog: descriptors=%+v err=%v", secondRead, err)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := registry.ListPublicOperations(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled catalog listing returned %v", err)
	}
}

func TestPublicOperationRegistryValidatesDescriptorAndRouteConfig(t *testing.T) {
	for _, descriptor := range []engine.PublicOperationDescriptor{
		{ID: "forms.upload", ContractVersion: 1, Methods: []string{"POST"}, MaxRequestBytes: 10, AllowedContentTypes: []string{"application/octet-stream"}},
		{ID: "public.form-upload", ContractVersion: 0, Methods: []string{"POST"}, MaxRequestBytes: 10, AllowedContentTypes: []string{"application/octet-stream"}},
		{ID: "public.form-upload", ContractVersion: 1, Methods: []string{"TRACE"}, MaxRequestBytes: 10, AllowedContentTypes: []string{"application/octet-stream"}},
		{ID: "public.form-upload", ContractVersion: 1, Methods: []string{"POST"}, MaxRequestBytes: 0, AllowedContentTypes: []string{"application/octet-stream"}},
		{ID: "public.form-upload", ContractVersion: 1, Methods: []string{"POST"}, MaxRequestBytes: 10, AllowedContentTypes: []string{"*/*"}},
	} {
		if _, err := NewPublicOperationRegistry(&testPublicOperation{descriptor: descriptor}); !errors.Is(err, engine.ErrInvalidPublicOperation) {
			t.Errorf("invalid descriptor %#v returned %v", descriptor, err)
		}
	}

	op := validPublicOperation("public.form-upload")
	registry, err := NewPublicOperationRegistry(op)
	if err != nil {
		t.Fatal(err)
	}
	target := engine.RouteTarget{Kind: engine.TargetRegisteredOperation, OperationID: op.descriptor.ID, OperationConfig: json.RawMessage(`{"max_bytes":4096}`)}
	if err := registry.ValidatePublicOperationTarget(context.Background(), target); err != nil {
		t.Fatalf("registered target rejected: %v", err)
	}
	if err := registry.ValidatePublicOperationTarget(context.Background(), engine.RouteTarget{Kind: engine.TargetRegisteredOperation, OperationID: "public.uncompiled", OperationConfig: json.RawMessage(`{}`)}); !errors.Is(err, engine.ErrPublicOperationNotRegistered) {
		t.Fatalf("uncompiled target returned %v", err)
	}
	op.configErr = engine.ErrInvalidTarget
	if err := registry.ValidatePublicOperationTarget(context.Background(), target); !errors.Is(err, engine.ErrInvalidTarget) {
		t.Fatalf("operation config rejection returned %v", err)
	}
}
