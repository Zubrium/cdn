package contracts

import (
	"context"
	"errors"
	"io"
)

type StreamRef string

type StreamCapability interface {
	OpenRead(context.Context, StreamRef) (io.ReadCloser, error)
	OpenWrite(context.Context, StreamRef) (io.WriteCloser, error)
}

type ScopedStreamCapability interface {
	StreamCapability
	Close() error
}

// StreamRetainer keeps an active bus stream operation alive until its
// producer has completed, even when the enclosing JSON dispatch has returned.
type StreamRetainer interface {
	Retain() (func(), error)
}

type streamCapabilityKey struct{}

func WithStreamCapability(ctx context.Context, capability StreamCapability) context.Context {
	return context.WithValue(ctx, streamCapabilityKey{}, capability)
}

func StreamCapabilityFromContext(ctx context.Context) (StreamCapability, bool) {
	if ctx == nil {
		return nil, false
	}
	capability, ok := ctx.Value(streamCapabilityKey{}).(StreamCapability)
	return capability, ok && capability != nil
}

func CallWithStreams[Request, Response any](ctx context.Context, dispatcher Dispatcher, operation Operation[Request, Response], request Request, streams ScopedStreamCapability) (response Response, callErr error) {
	if ctx == nil || streams == nil {
		return response, ErrStreamUnavailable
	}
	defer func() {
		if closeErr := streams.Close(); closeErr != nil {
			callErr = errors.Join(callErr, closeErr)
		}
	}()
	return Call(WithStreamCapability(ctx, streams), dispatcher, operation, request)
}
