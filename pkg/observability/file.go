package observability

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type signalFile struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	maxFiles int
	file     *os.File
	closed   bool
}

func newSignalFile(directory, name string, maxBytes int64, maxFiles int) (*signalFile, error) {
	path := filepath.Join(directory, name+".jsonl")
	writer := &signalFile{path: path, maxBytes: maxBytes, maxFiles: maxFiles}
	if err := writer.open(); err != nil {
		return nil, err
	}
	return writer, nil
}

func (w *signalFile) open() error {
	file, err := os.OpenFile(w.path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	w.file = file
	return nil
}

func (w *signalFile) rotate() error {
	if w.file != nil {
		if err := w.file.Close(); err != nil {
			return err
		}
		w.file = nil
	}
	for index := w.maxFiles - 1; index >= 1; index-- {
		current := fmt.Sprintf("%s.%d", w.path, index)
		next := fmt.Sprintf("%s.%d", w.path, index+1)
		if _, err := os.Stat(current); err == nil {
			if err := os.Rename(current, next); err != nil {
				return err
			}
		}
	}
	if err := os.Rename(w.path, w.path+".1"); err != nil && !os.IsNotExist(err) {
		return err
	}
	return w.open()
}

func (w *signalFile) write(record map[string]any) error {
	line, err := json.Marshal(record)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		// Shutdown is terminal: a late signal must not reopen a closed file.
		return nil
	}
	if w.file == nil {
		if err := w.open(); err != nil {
			return err
		}
	}
	info, err := w.file.Stat()
	if err == nil && info.Size()+int64(len(line)) > w.maxBytes {
		if err := w.rotate(); err != nil {
			return err
		}
	}
	_, err = w.file.Write(line)
	return err
}

func (w *signalFile) close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

type fileBackend struct {
	logs    *signalFile
	spans   *signalFile
	metrics *signalFile
}

func newFileBackend(directory string, maxBytes int64, maxFiles int) (backend, error) {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, err
	}
	logs, err := newSignalFile(directory, "logs", maxBytes, maxFiles)
	if err != nil {
		return nil, err
	}
	spans, err := newSignalFile(directory, "spans", maxBytes, maxFiles)
	if err != nil {
		_ = logs.close()
		return nil, err
	}
	metrics, err := newSignalFile(directory, "metrics", maxBytes, maxFiles)
	if err != nil {
		_ = logs.close()
		_ = spans.close()
		return nil, err
	}
	return &fileBackend{logs: logs, spans: spans, metrics: metrics}, nil
}

func recordBase(signal, timestamp string, identity Identity, trace TraceContext) map[string]any {
	record := map[string]any{
		"timestamp": timestamp,
		"signal":    signal,
	}
	for key, value := range attributesMap(identityAttributes(identity)) {
		record[key] = value
	}
	for key, value := range attributesMap(traceAttributes(trace)) {
		record[key] = value
	}
	return record
}

func (b *fileBackend) Log(_ context.Context, identity Identity, level Level, message string, attrs []Attribute, trace TraceContext) {
	record := recordBase("log", time.Now().UTC().Format(time.RFC3339Nano), identity, trace)
	record["level"] = string(level)
	record["message"] = message
	for key, value := range attributesMap(attrs) {
		record[key] = value
	}
	_ = b.logs.write(record)
}

func (b *fileBackend) LogSpan(trace TraceContext, identity Identity, name string, attrs []Attribute) {
	record := recordBase("span", time.Now().UTC().Format(time.RFC3339Nano), identity, trace)
	record["name"] = name
	for key, value := range attributesMap(attrs) {
		record[key] = value
	}
	_ = b.spans.write(record)
}

func (b *fileBackend) Start(ctx context.Context, _ Identity, _ string, _ []Attribute, parent TraceContext) (context.Context, TraceContext, func(error)) {
	return ctx, newTraceContext(parent), nil
}

func metricRecord(ctx context.Context, kind, name string, attrs []Attribute, value float64) map[string]any {
	record := map[string]any{
		"timestamp":    time.Now().UTC().Format(time.RFC3339Nano),
		"signal":       "metric",
		"metric.kind":  kind,
		"metric.name":  name,
		"metric.value": value,
	}
	for key, item := range attributesMap(attrs) {
		record[key] = item
	}
	// Trace IDs are exemplars for metrics: they correlate individual
	// observations with logs/spans without becoming high-cardinality labels.
	for key, item := range attributesMap(traceAttributes(traceFromContext(ctx))) {
		record[key] = item
	}
	return record
}

func (b *fileBackend) Counter(ctx context.Context, name string, attrs []Attribute, value float64) {
	_ = b.metrics.write(metricRecord(ctx, "counter", name, attrs, value))
}

func (b *fileBackend) Histogram(ctx context.Context, name string, attrs []Attribute, value float64) {
	_ = b.metrics.write(metricRecord(ctx, "histogram", name, attrs, value))
}

func (b *fileBackend) Gauge(ctx context.Context, name string, attrs []Attribute, value float64) {
	_ = b.metrics.write(metricRecord(ctx, "gauge", name, attrs, value))
}

func (b *fileBackend) Close(context.Context) error {
	first := b.logs.close()
	if err := b.spans.close(); err != nil && first == nil {
		first = err
	}
	if err := b.metrics.close(); err != nil && first == nil {
		first = err
	}
	return first
}
