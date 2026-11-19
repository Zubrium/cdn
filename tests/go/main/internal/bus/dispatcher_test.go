package bus

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Zubrium/cdn/pkg/contracts"
)

type pingRequest struct{ Value string }
type pingResponse struct{ Value string }

func TestDispatcherRequiresSealAndRejectsDuplicateOperations(t *testing.T) {
	dispatcher := New()
	operation := contracts.Operation[pingRequest, pingResponse]{Key: contracts.Key{ID: "core.ping", Version: 1}}
	handler := func(_ context.Context, request pingRequest) (pingResponse, error) {
		return pingResponse{Value: request.Value}, nil
	}
	if err := Register(dispatcher, operation, handler); err != nil {
		t.Fatal(err)
	}
	if err := Register(dispatcher, operation, handler); !errors.Is(err, ErrDuplicateKey) {
		t.Fatalf("duplicate registration = %v", err)
	}
	if _, err := contracts.Call(context.Background(), dispatcher, operation, pingRequest{Value: "hello"}); !errors.Is(err, ErrNotSealed) {
		t.Fatalf("dispatch before seal = %v", err)
	}
	if err := dispatcher.Seal(); err != nil {
		t.Fatal(err)
	}
	response, err := contracts.Call(context.Background(), dispatcher, operation, pingRequest{Value: "hello"})
	if err != nil || response.Value != "hello" {
		t.Fatalf("typed dispatch = %#v, %v", response, err)
	}
	if err := dispatcher.Seal(); err != nil {
		t.Fatalf("sealing twice = %v", err)
	}
	if err := Register(dispatcher, contracts.Operation[int, int]{Key: contracts.Key{ID: "core.late", Version: 1}}, func(context.Context, int) (int, error) { return 0, nil }); !errors.Is(err, ErrNotSealed) {
		t.Fatalf("registration after seal = %v", err)
	}
}

func TestDispatcherPropagatesHandlerErrorsAndRejectsUnknownOrWrongTypes(t *testing.T) {
	dispatcher := New()
	failure := errors.New("handler failed")
	operation := contracts.Operation[pingRequest, pingResponse]{Key: contracts.Key{ID: "core.ping", Version: 1}}
	if err := Register(dispatcher, operation, func(context.Context, pingRequest) (pingResponse, error) {
		return pingResponse{}, failure
	}); err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Seal(); err != nil {
		t.Fatal(err)
	}
	if _, err := contracts.Call(context.Background(), dispatcher, operation, pingRequest{}); !errors.Is(err, failure) {
		t.Fatalf("handler error = %v", err)
	}
	if _, err := dispatcher.Dispatch(context.Background(), contracts.Key{ID: "core.unknown", Version: 1}, nil); !errors.Is(err, ErrHandlerNotFound) {
		t.Fatalf("unknown operation = %v", err)
	}
	if _, err := dispatcher.Dispatch(context.Background(), operation.Key, "wrong"); !errors.Is(err, contracts.ErrRequestTypeMismatch) {
		t.Fatalf("wrong request type = %v", err)
	}
}

func TestCloseStopsAdmissionAndWaitsForInFlightHandlers(t *testing.T) {
	dispatcher := New()
	operation := contracts.Operation[pingRequest, pingResponse]{Key: contracts.Key{ID: "core.ping", Version: 1}}
	started := make(chan struct{})
	release := make(chan struct{})
	if err := Register(dispatcher, operation, func(ctx context.Context, _ pingRequest) (pingResponse, error) {
		close(started)
		select {
		case <-release:
			return pingResponse{Value: "done"}, nil
		case <-ctx.Done():
			return pingResponse{}, ctx.Err()
		}
	}); err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Seal(); err != nil {
		t.Fatal(err)
	}
	callDone := make(chan struct{})
	go func() {
		defer close(callDone)
		_, _ = contracts.Call(context.Background(), dispatcher, operation, pingRequest{})
	}()
	<-started
	closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	if err := dispatcher.Close(closeCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close() before handler exits = %v", err)
	}
	if _, err := contracts.Call(context.Background(), dispatcher, operation, pingRequest{}); !errors.Is(err, ErrClosed) {
		t.Fatalf("dispatch after close = %v", err)
	}
	close(release)
	<-callDone
	if err := dispatcher.Close(context.Background()); err != nil {
		t.Fatalf("Close() after handler exits = %v", err)
	}
}

func TestCoreReadinessUsesTypedOperationAndPropagatesFailures(t *testing.T) {
	dispatcher := New()
	failure := errors.New("coordinator is not ready")
	if err := RegisterCoreReadiness(dispatcher, func(context.Context) error { return failure }); err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Seal(); err != nil {
		t.Fatal(err)
	}
	operation := contracts.CoreReadinessOperation()
	if _, err := contracts.Call(context.Background(), dispatcher, operation, contracts.CoreReadinessRequest{}); !errors.Is(err, failure) {
		t.Fatalf("readiness failure = %v", err)
	}

	readyDispatcher := New()
	if err := RegisterCoreReadiness(readyDispatcher, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := readyDispatcher.Seal(); err != nil {
		t.Fatal(err)
	}
	response, err := contracts.Call(context.Background(), readyDispatcher, operation, contracts.CoreReadinessRequest{})
	if err != nil || !response.Ready {
		t.Fatalf("readiness response = %#v, %v", response, err)
	}
}
