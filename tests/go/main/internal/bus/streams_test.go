package bus

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/Zubrium/cdn/pkg/contracts"
	"github.com/Zubrium/cdn/pkg/observability"
)

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

type streamProbeRequest struct{}

type streamBodyRequest struct {
	Ref contracts.StreamRef
}

type streamProbeResponse struct {
	Payload string
}

func TestDispatcherExposesScopedStreamCapabilityPerCall(t *testing.T) {
	operation := contracts.Operation[streamProbeRequest, streamProbeResponse]{Key: contracts.Key{ID: "bus.streams.probe", Version: 1}}
	dispatcher, err := NewAllowlisted(operation.Key)
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.EnableScopedStreams(); err != nil {
		t.Fatal(err)
	}
	observer, err := observability.New(observability.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer observer.Close(context.Background())
	dispatcher.WithObserver(observer)
	handler := func(ctx context.Context, _ streamProbeRequest) (streamProbeResponse, error) {
		if _, ok := contracts.StreamCapabilityFromContext(ctx); !ok {
			return streamProbeResponse{}, errors.New("stream capability is missing")
		}
		return streamProbeResponse{Payload: "ok"}, nil
	}
	if err := Register(dispatcher, operation, handler); err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Seal(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		response, err := contracts.Call(context.Background(), dispatcher, operation, streamProbeRequest{})
		if err != nil || response.Payload != "ok" {
			t.Fatalf("stream probe = %#v, %v", response, err)
		}
	}
}

func TestCallWithStreamsPassesRegisteredBodyToHandler(t *testing.T) {
	operation := contracts.Operation[streamBodyRequest, streamProbeResponse]{Key: contracts.Key{ID: "bus.streams.body", Version: 1}}
	dispatcher, err := NewAllowlisted(operation.Key)
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.EnableScopedStreams(); err != nil {
		t.Fatal(err)
	}
	handler := func(ctx context.Context, request streamBodyRequest) (streamProbeResponse, error) {
		capability, ok := contracts.StreamCapabilityFromContext(ctx)
		if !ok {
			return streamProbeResponse{}, errors.New("stream capability is missing")
		}
		reader, err := capability.OpenRead(context.Background(), request.Ref)
		if err != nil {
			return streamProbeResponse{}, err
		}
		data, err := io.ReadAll(reader)
		if err != nil {
			return streamProbeResponse{}, err
		}
		return streamProbeResponse{Payload: string(data)}, nil
	}
	if err := Register(dispatcher, operation, handler); err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Seal(); err != nil {
		t.Fatal(err)
	}
	streams := NewScopedStreams()
	ref, err := streams.AddReader(io.NopCloser(bytes.NewReader([]byte("payload"))))
	if err != nil {
		t.Fatal(err)
	}
	response, err := contracts.CallWithStreams(context.Background(), dispatcher, operation, streamBodyRequest{Ref: ref}, streams)
	if err != nil || response.Payload != "payload" {
		t.Fatalf("body call = %#v, %v", response, err)
	}
	if _, err := streams.OpenRead(context.Background(), ref); !errors.Is(err, ErrStreamClosed) {
		t.Fatalf("streams were not closed by CallWithStreams: %v", err)
	}
}

func TestDispatcherWaitsForProvidedScopedStreamsToClose(t *testing.T) {
	operation := contracts.Operation[streamProbeRequest, streamProbeResponse]{Key: contracts.Key{ID: "bus.streams.lifetime", Version: 1}}
	dispatcher, err := NewAllowlisted(operation.Key)
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.EnableScopedStreams(); err != nil {
		t.Fatal(err)
	}
	if err := Register(dispatcher, operation, func(context.Context, streamProbeRequest) (streamProbeResponse, error) {
		return streamProbeResponse{Payload: "ready"}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Seal(); err != nil {
		t.Fatal(err)
	}
	streams := NewScopedStreams()
	_, err = streams.AddWriter(nopWriteCloser{Writer: &bytes.Buffer{}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := contracts.Call(contracts.WithStreamCapability(context.Background(), streams), dispatcher, operation, streamProbeRequest{}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := dispatcher.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close before stream close = %v, want deadline", err)
	}
	if err := streams.Close(); err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Close(context.Background()); err != nil {
		t.Fatalf("Close after stream close = %v", err)
	}
}

func TestScopedStreamsRejectUnknownConsumedAndClosedRefs(t *testing.T) {
	streams := NewScopedStreams()
	if _, err := streams.OpenRead(context.Background(), "unknown"); !errors.Is(err, ErrStreamNotFound) {
		t.Fatalf("unknown ref = %v", err)
	}
	if _, err := streams.AddReader(nil); !errors.Is(err, ErrInvalidStream) {
		t.Fatalf("nil reader = %v", err)
	}
	ref, err := streams.AddReader(io.NopCloser(bytes.NewReader([]byte("payload"))))
	if err != nil {
		t.Fatal(err)
	}
	reader, err := streams.OpenRead(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(reader)
	if err != nil || string(data) != "payload" {
		t.Fatalf("read = %q, %v", data, err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := streams.OpenRead(context.Background(), ref); !errors.Is(err, ErrStreamConsumed) {
		t.Fatalf("second open = %v", err)
	}
	if err := streams.Close(); err != nil {
		t.Fatal(err)
	}
	if err := streams.Close(); err != nil {
		t.Fatalf("second close = %v", err)
	}
	if _, err := streams.AddWriter(nopWriteCloser{Writer: &bytes.Buffer{}}); !errors.Is(err, ErrStreamClosed) {
		t.Fatalf("add after close = %v", err)
	}
}

func TestEnableScopedStreamsRequiresUnsealedDispatcher(t *testing.T) {
	dispatcher, err := NewAllowlisted(contracts.Key{ID: "bus.streams.sealed", Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Seal(); err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.EnableScopedStreams(); !errors.Is(err, ErrAlreadySealed) {
		t.Fatalf("enable after seal = %v", err)
	}
}
