package bus

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"sync"

	"github.com/Zubrium/cdn/pkg/contracts"
)

var (
	ErrStreamNotFound = errors.New("scoped stream is not registered")
	ErrStreamConsumed = errors.New("scoped stream was already opened")
	ErrStreamClosed   = errors.New("scoped stream set is closed")
	ErrInvalidStream  = errors.New("scoped stream is nil")
)

type ScopedStreams struct {
	mu         sync.Mutex
	entries    map[contracts.StreamRef]*streamEntry
	closeHooks []func()
	retained   int
	closed     bool
}

type streamEntry struct {
	mu       sync.Mutex
	reader   io.ReadCloser
	writer   io.WriteCloser
	opened   bool
	close    sync.Once
	closeErr error
}

func NewScopedStreams() *ScopedStreams {
	return &ScopedStreams{entries: make(map[contracts.StreamRef]*streamEntry)}
}

func (streams *ScopedStreams) AddReader(reader io.ReadCloser) (contracts.StreamRef, error) {
	if reader == nil {
		return "", ErrInvalidStream
	}
	return streams.add(&streamEntry{reader: reader})
}

func (streams *ScopedStreams) AddWriter(writer io.WriteCloser) (contracts.StreamRef, error) {
	if writer == nil {
		return "", ErrInvalidStream
	}
	return streams.add(&streamEntry{writer: writer})
}

func (streams *ScopedStreams) add(entry *streamEntry) (contracts.StreamRef, error) {
	streams.mu.Lock()
	defer streams.mu.Unlock()
	if streams.closed {
		return "", ErrStreamClosed
	}
	if streams.entries == nil {
		streams.entries = make(map[contracts.StreamRef]*streamEntry)
	}
	for {
		var raw [16]byte
		if _, err := rand.Read(raw[:]); err != nil {
			return "", err
		}
		ref := contracts.StreamRef(hex.EncodeToString(raw[:]))
		if _, exists := streams.entries[ref]; exists {
			continue
		}
		streams.entries[ref] = entry
		return ref, nil
	}
}

func (streams *ScopedStreams) OpenRead(ctx context.Context, ref contracts.StreamRef) (io.ReadCloser, error) {
	if ctx == nil {
		return nil, ErrInvalidStream
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entry, err := streams.open(ref, true)
	if err != nil {
		return nil, err
	}
	return &scopedReadCloser{entry: entry}, nil
}

func (streams *ScopedStreams) OpenWrite(ctx context.Context, ref contracts.StreamRef) (io.WriteCloser, error) {
	if ctx == nil {
		return nil, ErrInvalidStream
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entry, err := streams.open(ref, false)
	if err != nil {
		return nil, err
	}
	return &scopedWriteCloser{entry: entry}, nil
}

func (streams *ScopedStreams) open(ref contracts.StreamRef, read bool) (*streamEntry, error) {
	streams.mu.Lock()
	defer streams.mu.Unlock()
	if streams.closed {
		return nil, ErrStreamClosed
	}
	entry := streams.entries[ref]
	if entry == nil || (read && entry.reader == nil) || (!read && entry.writer == nil) {
		return nil, ErrStreamNotFound
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.opened {
		return nil, ErrStreamConsumed
	}
	entry.opened = true
	return entry, nil
}

func (streams *ScopedStreams) Close() error {
	if streams == nil {
		return nil
	}
	streams.mu.Lock()
	if streams.closed {
		streams.mu.Unlock()
		return nil
	}
	streams.closed = true
	entries := make([]*streamEntry, 0, len(streams.entries))
	for _, entry := range streams.entries {
		entries = append(entries, entry)
	}
	hooks := streams.takeCloseHooksLocked()
	streams.mu.Unlock()
	var result error
	for _, entry := range entries {
		result = errors.Join(result, entry.closeResource())
	}
	for _, hook := range hooks {
		hook()
	}
	return result
}

func (streams *ScopedStreams) onClose(hook func()) {
	if streams == nil || hook == nil {
		return
	}
	streams.mu.Lock()
	if streams.closed && streams.retained == 0 {
		streams.mu.Unlock()
		hook()
		return
	}
	streams.closeHooks = append(streams.closeHooks, hook)
	streams.mu.Unlock()
}

func (streams *ScopedStreams) Retain() (func(), error) {
	if streams == nil {
		return nil, ErrStreamClosed
	}
	streams.mu.Lock()
	if streams.closed {
		streams.mu.Unlock()
		return nil, ErrStreamClosed
	}
	streams.retained++
	streams.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			streams.mu.Lock()
			streams.retained--
			hooks := streams.takeCloseHooksLocked()
			streams.mu.Unlock()
			for _, hook := range hooks {
				hook()
			}
		})
	}, nil
}

func (streams *ScopedStreams) takeCloseHooksLocked() []func() {
	if !streams.closed || streams.retained != 0 || len(streams.closeHooks) == 0 {
		return nil
	}
	hooks := append([]func(){}, streams.closeHooks...)
	streams.closeHooks = nil
	return hooks
}

func (entry *streamEntry) closeResource() error {
	entry.close.Do(func() {
		if entry.reader != nil {
			entry.closeErr = entry.reader.Close()
		} else {
			entry.closeErr = entry.writer.Close()
		}
	})
	return entry.closeErr
}

type scopedReadCloser struct{ entry *streamEntry }

func (stream *scopedReadCloser) Read(buffer []byte) (int, error) {
	return stream.entry.reader.Read(buffer)
}
func (stream *scopedReadCloser) Close() error { return stream.entry.closeResource() }

type scopedWriteCloser struct{ entry *streamEntry }

func (stream *scopedWriteCloser) Write(buffer []byte) (int, error) {
	return stream.entry.writer.Write(buffer)
}
func (stream *scopedWriteCloser) Close() error { return stream.entry.closeResource() }
func (stream *scopedWriteCloser) CloseWithError(err error) error {
	stream.entry.close.Do(func() {
		if closer, ok := stream.entry.writer.(interface{ CloseWithError(error) error }); ok {
			stream.entry.closeErr = closer.CloseWithError(err)
		} else {
			stream.entry.closeErr = stream.entry.writer.Close()
		}
	})
	return stream.entry.closeErr
}

var _ contracts.ScopedStreamCapability = (*ScopedStreams)(nil)
