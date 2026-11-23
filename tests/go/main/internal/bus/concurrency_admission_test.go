package bus

import (
	"context"
	"errors"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Zubrium/cdn/pkg/contracts"
	"github.com/Zubrium/cdn/pkg/observability"
)

type admissionPingRequest struct{ Value string }
type admissionPingResponse struct{ Value string }

func admissionObserver(t *testing.T) *observability.Observer {
	t.Helper()
	observer, err := observability.New(observability.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = observer.Close(context.Background()) })
	return observer
}

func admissionMetricLine(t *testing.T, observer *observability.Observer, name, label string) string {
	t.Helper()
	recorder := httptest.NewRecorder()
	observer.MetricsHandler().ServeHTTP(recorder, httptest.NewRequest("GET", "/metrics", nil))
	for _, line := range strings.Split(recorder.Body.String(), "\n") {
		if !strings.HasPrefix(line, name+"{") && !strings.HasPrefix(line, name+" ") {
			continue
		}
		if label == "" || strings.Contains(line, label) {
			return line
		}
	}
	return ""
}

func admissionMetricValue(t *testing.T, observer *observability.Observer, name, label string) float64 {
	t.Helper()
	line := admissionMetricLine(t, observer, name, label)
	if line == "" {
		t.Fatalf("metric %s with %q is absent", name, label)
	}
	fields := strings.Fields(line)
	value, err := strconv.ParseFloat(fields[len(fields)-1], 64)
	if err != nil {
		t.Fatalf("metric line %q: %v", line, err)
	}
	return value
}

func admissionDispatcher(t *testing.T, limits ConcurrencyLimits, observer *observability.Observer, keys ...contracts.Key) *Dispatcher {
	t.Helper()
	dispatcher, err := NewAllowlisted(limits, keys...)
	if err != nil {
		t.Fatalf("NewAllowlisted: %v", err)
	}
	dispatcher.WithObserver(observer)
	return dispatcher
}

func TestConcurrencyAdmissionScenario(t *testing.T) {
	t.Run("CONC-003", func(t *testing.T) {
		observer := admissionObserver(t)
		operation := contracts.Operation[admissionPingRequest, admissionPingResponse]{
			Key: contracts.Key{ID: "core.admission.ping", Version: 1},
		}
		dispatcher := admissionDispatcher(t, ConcurrencyLimits{MaxInFlight: 1}, observer, operation.Key)
		started := make(chan struct{})
		release := make(chan struct{})
		var calls atomic.Int64
		if err := Register(dispatcher, operation, func(context.Context, admissionPingRequest) (admissionPingResponse, error) {
			calls.Add(1)
			close(started)
			<-release
			return admissionPingResponse{Value: "ok"}, nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := dispatcher.Seal(); err != nil {
			t.Fatal(err)
		}

		firstDone := make(chan error, 1)
		go func() {
			_, err := contracts.Call(context.Background(), dispatcher, operation, admissionPingRequest{})
			firstDone <- err
		}()
		<-started
		overloadStarted := time.Now()
		_, err := contracts.Call(context.Background(), dispatcher, operation, admissionPingRequest{})
		if !errors.Is(err, ErrOverloaded) {
			t.Fatalf("dispatch above global limit = %v, want ErrOverloaded", err)
		}
		if elapsed := time.Since(overloadStarted); elapsed > 100*time.Millisecond {
			t.Fatalf("overload waited %s, want fail-fast", elapsed)
		}
		if calls.Load() != 1 {
			t.Fatalf("handler calls after overload = %d, want 1", calls.Load())
		}
		if got := admissionMetricValue(t, observer, "zubrium_concurrency_rejected_total", `reason="GLOBAL"`); got != 1 {
			t.Fatalf("rejected{GLOBAL} = %v, want 1", got)
		}
		if got := admissionMetricValue(t, observer, "zubrium_bus_in_flight", `scope="BUS"`); got != 1 {
			t.Fatalf("bus in-flight = %v, want 1", got)
		}
		close(release)
		if err := <-firstDone; err != nil {
			t.Fatalf("admitted dispatch failed: %v", err)
		}
		if got := admissionMetricValue(t, observer, "zubrium_bus_in_flight", `scope="BUS"`); got != 0 {
			t.Fatalf("bus in-flight after completion = %v, want 0", got)
		}

		perOperationObserver := admissionObserver(t)
		perOperation := contracts.Operation[admissionPingRequest, admissionPingResponse]{
			Key: contracts.Key{ID: "core.admission.per_operation", Version: 1},
		}
		perOperationDispatcher := admissionDispatcher(t, ConcurrencyLimits{
			MaxInFlight: 4,
			PerOperation: PerOperationLimits{
				Enabled:            true,
				DefaultMaxInFlight: 2,
				Overrides: []OperationLimit{
					{Key: perOperation.Key, MaxInFlight: 1},
				},
			},
		}, perOperationObserver, perOperation.Key)
		perOperationStarted := make(chan struct{})
		perOperationRelease := make(chan struct{})
		if err := Register(perOperationDispatcher, perOperation, func(context.Context, admissionPingRequest) (admissionPingResponse, error) {
			close(perOperationStarted)
			<-perOperationRelease
			return admissionPingResponse{}, nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := perOperationDispatcher.Seal(); err != nil {
			t.Fatal(err)
		}
		perOperationDone := make(chan error, 1)
		go func() {
			_, err := contracts.Call(context.Background(), perOperationDispatcher, perOperation, admissionPingRequest{})
			perOperationDone <- err
		}()
		<-perOperationStarted
		if _, err := contracts.Call(context.Background(), perOperationDispatcher, perOperation, admissionPingRequest{}); !errors.Is(err, ErrOverloaded) {
			t.Fatalf("dispatch above per-operation limit = %v, want ErrOverloaded", err)
		}
		if got := admissionMetricValue(t, perOperationObserver, "zubrium_concurrency_rejected_total", `reason="PER_OPERATION"`); got != 1 {
			t.Fatalf("rejected{PER_OPERATION} = %v, want 1", got)
		}
		close(perOperationRelease)
		if err := <-perOperationDone; err != nil {
			t.Fatal(err)
		}
	})

	t.Run("CONC-011", func(t *testing.T) {
		observer := admissionObserver(t)
		operation := contracts.Operation[admissionPingRequest, admissionPingResponse]{
			Key: contracts.Key{ID: "core.admission.shutdown", Version: 1},
		}
		dispatcher := admissionDispatcher(t, ConcurrencyLimits{MaxInFlight: 1}, observer, operation.Key)
		started := make(chan struct{})
		cancelHandler := make(chan struct{})
		if err := Register(dispatcher, operation, func(ctx context.Context, _ admissionPingRequest) (admissionPingResponse, error) {
			close(started)
			select {
			case <-cancelHandler:
				return admissionPingResponse{}, nil
			case <-ctx.Done():
				return admissionPingResponse{}, ctx.Err()
			}
		}); err != nil {
			t.Fatal(err)
		}
		if err := dispatcher.Seal(); err != nil {
			t.Fatal(err)
		}
		callCtx, cancelCall := context.WithCancel(context.Background())
		defer cancelCall()
		done := make(chan error, 1)
		go func() {
			_, err := contracts.Call(callCtx, dispatcher, operation, admissionPingRequest{})
			done <- err
		}()
		<-started
		closeCtx, cancelClose := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancelClose()
		if err := dispatcher.Close(closeCtx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Close with active dispatch = %v, want deadline", err)
		}
		if _, err := contracts.Call(context.Background(), dispatcher, operation, admissionPingRequest{}); !errors.Is(err, ErrClosed) {
			t.Fatalf("dispatch after Close = %v, want ErrClosed", err)
		}
		cancelCall()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("active dispatch after cancel = %v, want canceled", err)
		}
		if err := dispatcher.Close(context.Background()); err != nil {
			t.Fatalf("drained Close = %v", err)
		}
		if err := dispatcher.Close(context.Background()); err != nil {
			t.Fatalf("repeated Close = %v", err)
		}
		if got := admissionMetricValue(t, observer, "zubrium_bus_in_flight", `scope="BUS"`); got != 0 {
			t.Fatalf("bus in-flight after drain = %v, want 0", got)
		}
		if line := admissionMetricLine(t, observer, "zubrium_concurrency_rejected_total", `reason="GLOBAL"`); line != "" {
			fields := strings.Fields(line)
			value, err := strconv.ParseFloat(fields[len(fields)-1], 64)
			if err != nil {
				t.Fatal(err)
			}
			if value != 0 {
				t.Fatalf("rejected after closed dispatch = %v, want 0", value)
			}
		}
	})
}
