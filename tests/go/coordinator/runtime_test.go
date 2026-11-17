package coordinator

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type lifecycleRecorder struct {
	id    string
	order *[]string
	ready atomic.Bool
	err   error
}

type fatalRecorder struct {
	*lifecycleRecorder
	failures chan error
}
type panicFailureSource struct{ *lifecycleRecorder }
type nilFailureSource struct{ *lifecycleRecorder }

type stopUntilContextEnds struct{}
type panicStopComponent struct{}
type panicReadinessComponent struct{ *lifecycleRecorder }
type typedNilComponent struct{}
type blockingPanicProbe struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}
type activationRecorder struct {
	*lifecycleRecorder
	readyAfterActivation bool
}

func (stopUntilContextEnds) Start(context.Context) error { return nil }
func (stopUntilContextEnds) Ready(context.Context) error { return nil }
func (stopUntilContextEnds) Stop(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}
func (panicStopComponent) Start(context.Context) error  { return nil }
func (panicStopComponent) Ready(context.Context) error  { return nil }
func (panicStopComponent) Stop(context.Context) error   { panic("sensitive stop failure") }
func (panicReadinessComponent) IsReady() bool           { panic("sensitive readiness failure") }
func (*typedNilComponent) Start(context.Context) error  { return nil }
func (*typedNilComponent) Ready(context.Context) error  { return nil }
func (*typedNilComponent) Stop(context.Context) error   { return nil }
func (*blockingPanicProbe) Start(context.Context) error { return nil }
func (*blockingPanicProbe) Ready(context.Context) error { return nil }
func (*blockingPanicProbe) Stop(context.Context) error  { return nil }
func (p *blockingPanicProbe) IsReady() bool {
	p.once.Do(func() {
		close(p.entered)
		<-p.release
	})
	panic("sensitive readiness failure")
}
func (c *activationRecorder) Activate(context.Context) error {
	*c.order = append(*c.order, "activate:"+c.id)
	c.ready.Store(c.readyAfterActivation)
	return nil
}

func (c *fatalRecorder) Errors() <-chan error   { return c.failures }
func (panicFailureSource) Errors() <-chan error { panic("sensitive monitoring failure") }
func (nilFailureSource) Errors() <-chan error   { return nil }

func (c *lifecycleRecorder) Start(context.Context) error {
	*c.order = append(*c.order, "start:"+c.id)
	return c.err
}
func (c *lifecycleRecorder) Ready(context.Context) error { return nil }
func (c *lifecycleRecorder) Stop(context.Context) error {
	*c.order = append(*c.order, "stop:"+c.id)
	return nil
}
func (c *lifecycleRecorder) IsReady() bool { return c.ready.Load() }

func TestComposeStartsDependenciesAndStopsInReverse(t *testing.T) {
	var order []string
	registry := NewRuntimeRegistry()
	for _, component := range []RuntimeComponent{
		{ID: "core", New: func(context.Context) (ManagedComponent, error) {
			item := &lifecycleRecorder{id: "core", order: &order}
			item.ready.Store(true)
			return item, nil
		}},
		{ID: "coordinator", Requires: []string{"core"}, New: func(context.Context) (ManagedComponent, error) {
			item := &lifecycleRecorder{id: "coordinator", order: &order}
			item.ready.Store(true)
			return item, nil
		}},
		{ID: "engine.loader", Requires: []string{"core", "coordinator"}, New: func(context.Context) (ManagedComponent, error) {
			item := &lifecycleRecorder{id: "loader", order: &order}
			item.ready.Store(true)
			return item, nil
		}},
		{ID: "admin.files", Requires: []string{"engine.loader", "coordinator"}, New: func(context.Context) (ManagedComponent, error) {
			item := &lifecycleRecorder{id: "files", order: &order}
			item.ready.Store(true)
			return item, nil
		}},
	} {
		if err := registry.Register(component); err != nil {
			t.Fatal(err)
		}
	}
	runtime, err := Compose(context.Background(), BuildPlan{Components: []string{"core", "coordinator", "engine.loader", "admin.files"}}, registry, time.Second)
	if err != nil || !runtime.Ready() {
		t.Fatalf("Compose: runtime=%v err=%v", runtime, err)
	}
	if err := runtime.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{"start:core", "start:coordinator", "start:loader", "start:files", "stop:files", "stop:loader", "stop:coordinator", "stop:core"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("lifecycle order = %v, want %v", order, want)
	}
	if runtime.Ready() {
		t.Fatal("closed runtime reported ready")
	}
	if err := runtime.Close(context.Background()); err != nil {
		t.Fatalf("second Close was not idempotent: %v", err)
	}
}

func TestComposeActivatesAfterEveryComponentPassedReadiness(t *testing.T) {
	var order []string
	registry := NewRuntimeRegistry()
	core := &lifecycleRecorder{id: "core", order: &order}
	core.ready.Store(true)
	worker := &activationRecorder{lifecycleRecorder: &lifecycleRecorder{id: "coordinator", order: &order}, readyAfterActivation: true}
	if err := registry.Register(RuntimeComponent{ID: "core", New: func(context.Context) (ManagedComponent, error) {
		return core, nil
	}}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(RuntimeComponent{ID: "coordinator", Requires: []string{"core"}, New: func(context.Context) (ManagedComponent, error) {
		return worker, nil
	}}); err != nil {
		t.Fatal(err)
	}
	runtime, err := Compose(context.Background(), BuildPlan{Components: []string{"core", "coordinator"}}, registry, time.Second)
	if err != nil {
		t.Fatalf("Compose before worker activation: %v", err)
	}
	if !runtime.Ready() {
		t.Fatal("runtime did not report ready after activating its worker component")
	}
	if got := order[len(order)-1]; got != "activate:coordinator" {
		t.Fatalf("worker activation did not happen after startup/readiness phases: %v", order)
	}
	if err := runtime.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestComposeRejectsUnavailableComponentsBeforeStartingAnything(t *testing.T) {
	var starts atomic.Int32
	registry := NewRuntimeRegistry()
	if err := registry.Register(RuntimeComponent{ID: "core", New: func(context.Context) (ManagedComponent, error) {
		starts.Add(1)
		return &lifecycleRecorder{}, nil
	}}); err != nil {
		t.Fatal(err)
	}
	_, err := Compose(context.Background(), BuildPlan{Components: []string{"core", "coordinator", "engine.router"}}, registry, time.Second)
	if !errors.Is(err, ErrComponentUnavailable) || starts.Load() != 0 {
		t.Fatalf("missing component must fail before side effects: starts=%d err=%v", starts.Load(), err)
	}
}

func TestComposeRollsBackOnStartFailure(t *testing.T) {
	var order []string
	registry := NewRuntimeRegistry()
	for _, entry := range []struct {
		id       string
		requires []string
		fail     bool
	}{{"core", nil, false}, {"coordinator", []string{"core"}, false}, {"engine.loader", []string{"core", "coordinator"}, true}} {
		entry := entry
		if err := registry.Register(RuntimeComponent{ID: entry.id, Requires: entry.requires, New: func(context.Context) (ManagedComponent, error) {
			item := &lifecycleRecorder{id: entry.id, order: &order}
			if entry.fail {
				item.err = errors.New("fail")
			}
			return item, nil
		}}); err != nil {
			t.Fatal(err)
		}
	}
	_, err := Compose(context.Background(), BuildPlan{Components: []string{"core", "coordinator", "engine.loader"}}, registry, time.Second)
	if err == nil {
		t.Fatal("start failure was ignored")
	}
	want := []string{"start:core", "start:coordinator", "start:engine.loader", "stop:engine.loader", "stop:coordinator", "stop:core"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("rollback order = %v, want %v", order, want)
	}
}

func TestComposeRejectsCancelledStartupBeforeStartingComponents(t *testing.T) {
	var starts atomic.Int32
	registry := NewRuntimeRegistry()
	for _, id := range []string{"core", "coordinator"} {
		id := id
		requires := []string(nil)
		if id == "coordinator" {
			requires = []string{"core"}
		}
		if err := registry.Register(RuntimeComponent{ID: id, Requires: requires, New: func(context.Context) (ManagedComponent, error) {
			starts.Add(1)
			return &lifecycleRecorder{id: id, order: new([]string)}, nil
		}}); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Compose(ctx, BuildPlan{Components: []string{"core", "coordinator"}}, registry, time.Second)
	if !errors.Is(err, context.Canceled) || starts.Load() != 0 {
		t.Fatalf("cancelled startup started components: starts=%d err=%v", starts.Load(), err)
	}
}

func TestRuntimeReadinessDropsAfterFatalComponentFailure(t *testing.T) {
	var order []string
	failureComponent := &fatalRecorder{lifecycleRecorder: &lifecycleRecorder{id: "coordinator", order: &order}, failures: make(chan error, 1)}
	failureComponent.ready.Store(true)
	registry := NewRuntimeRegistry()
	if err := registry.Register(RuntimeComponent{ID: "core", New: func(context.Context) (ManagedComponent, error) {
		component := &lifecycleRecorder{id: "core", order: &order}
		component.ready.Store(true)
		return component, nil
	}}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(RuntimeComponent{ID: "coordinator", Requires: []string{"core"}, New: func(context.Context) (ManagedComponent, error) {
		return failureComponent, nil
	}}); err != nil {
		t.Fatal(err)
	}
	runtime, err := Compose(context.Background(), BuildPlan{Components: []string{"core", "coordinator"}}, registry, time.Second)
	if err != nil || !runtime.Ready() {
		t.Fatalf("compose runtime: ready=%v err=%v", runtime != nil && runtime.Ready(), err)
	}
	failureComponent.failures <- errors.New("sensitive backend failure")
	select {
	case failure := <-runtime.Failures():
		if failure == nil || failure.Error() != `component "coordinator" reported a fatal failure` {
			t.Fatalf("failure source leaked details: %v", failure)
		}
	case <-time.After(time.Second):
		t.Fatal("fatal component failure was not surfaced")
	}
	if runtime.Ready() {
		t.Fatal("runtime remained ready after a fatal component failure")
	}
	if err := runtime.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeCloseBoundsShutdownByConfiguredTimeout(t *testing.T) {
	registry := NewRuntimeRegistry()
	if err := registry.Register(RuntimeComponent{ID: "core", New: func(context.Context) (ManagedComponent, error) {
		item := &lifecycleRecorder{id: "core", order: new([]string)}
		item.ready.Store(true)
		return item, nil
	}}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(RuntimeComponent{ID: "coordinator", Requires: []string{"core"}, New: func(context.Context) (ManagedComponent, error) {
		return stopUntilContextEnds{}, nil
	}}); err != nil {
		t.Fatal(err)
	}
	runtime, err := Compose(context.Background(), BuildPlan{Components: []string{"core", "coordinator"}}, registry, 25*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	err = runtime.Close(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown did not surface the configured deadline: %v", err)
	}
	if time.Since(started) > time.Second {
		t.Fatalf("shutdown exceeded its configured bound: %s", time.Since(started))
	}
}

func TestComposeContainsFactoryPanicAndRollsBackStartedDependencies(t *testing.T) {
	var order []string
	registry := NewRuntimeRegistry()
	if err := registry.Register(RuntimeComponent{ID: "core", New: func(context.Context) (ManagedComponent, error) {
		component := &lifecycleRecorder{id: "core", order: &order}
		component.ready.Store(true)
		return component, nil
	}}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(RuntimeComponent{ID: "coordinator", Requires: []string{"core"}, New: func(context.Context) (ManagedComponent, error) {
		panic("must not escape component boundary")
	}}); err != nil {
		t.Fatal(err)
	}
	_, err := Compose(context.Background(), BuildPlan{Components: []string{"core", "coordinator"}}, registry, time.Second)
	if !errors.Is(err, ErrComponentPanic) {
		t.Fatalf("component factory panic was not converted to a startup error: %v", err)
	}
	if !reflect.DeepEqual(order, []string{"start:core", "stop:core"}) {
		t.Fatalf("started dependency was not rolled back after panic: %v", order)
	}
	if strings.Contains(err.Error(), "must not escape") {
		t.Fatal("panic detail leaked through returned error")
	}
}

func TestComposeRejectsTypedNilComponentFactoryResult(t *testing.T) {
	registry := NewRuntimeRegistry()
	if err := registry.Register(RuntimeComponent{ID: "core", New: func(context.Context) (ManagedComponent, error) {
		var component *typedNilComponent
		return component, nil
	}}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(RuntimeComponent{ID: "coordinator", Requires: []string{"core"}, New: func(context.Context) (ManagedComponent, error) {
		return &lifecycleRecorder{}, nil
	}}); err != nil {
		t.Fatal(err)
	}
	_, err := Compose(context.Background(), BuildPlan{Components: []string{"core", "coordinator"}}, registry, time.Second)
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("typed nil factory result was not rejected: %v", err)
	}
}

func TestRuntimeReadinessPanicFailsClosedWithoutLeakingDetails(t *testing.T) {
	var order []string
	registry := NewRuntimeRegistry()
	if err := registry.Register(RuntimeComponent{ID: "core", New: func(context.Context) (ManagedComponent, error) {
		component := &lifecycleRecorder{id: "core", order: &order}
		component.ready.Store(true)
		return component, nil
	}}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(RuntimeComponent{ID: "coordinator", Requires: []string{"core"}, New: func(context.Context) (ManagedComponent, error) {
		component := &lifecycleRecorder{id: "coordinator", order: &order}
		component.ready.Store(true)
		return &panicReadinessComponent{lifecycleRecorder: component}, nil
	}}); err != nil {
		t.Fatal(err)
	}
	runtime, err := Compose(context.Background(), BuildPlan{Components: []string{"core", "coordinator"}}, registry, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.Ready() {
		t.Fatal("readiness panic did not fail closed")
	}
	select {
	case failure := <-runtime.Failures():
		if failure == nil || failure.Error() != "component \"coordinator\" reported a fatal failure" {
			t.Fatalf("readiness panic detail leaked: %v", failure)
		}
	case <-time.After(time.Second):
		t.Fatal("readiness panic was not surfaced")
	}
	if err := runtime.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeReadinessProbeCanRaceWithClose(t *testing.T) {
	probe := &blockingPanicProbe{entered: make(chan struct{}), release: make(chan struct{})}
	registry := NewRuntimeRegistry()
	if err := registry.Register(RuntimeComponent{ID: "core", New: func(context.Context) (ManagedComponent, error) {
		core := &lifecycleRecorder{id: "core", order: new([]string)}
		core.ready.Store(true)
		return core, nil
	}}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(RuntimeComponent{ID: "coordinator", Requires: []string{"core"}, New: func(context.Context) (ManagedComponent, error) {
		return probe, nil
	}}); err != nil {
		t.Fatal(err)
	}
	runtime, err := Compose(context.Background(), BuildPlan{Components: []string{"core", "coordinator"}}, registry, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	readyDone := make(chan struct{})
	go func() {
		defer close(readyDone)
		_ = runtime.Ready()
	}()
	<-probe.entered
	if err := runtime.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	close(probe.release)
	select {
	case <-readyDone:
	case <-time.After(time.Second):
		t.Fatal("readiness probe did not finish after close")
	}
	if failure, open := <-runtime.Failures(); open {
		t.Fatalf("closed runtime accepted a late readiness failure: %v", failure)
	}
}

func TestRuntimeStopPanicDoesNotSkipEarlierDependencies(t *testing.T) {
	var order []string
	registry := NewRuntimeRegistry()
	if err := registry.Register(RuntimeComponent{ID: "core", New: func(context.Context) (ManagedComponent, error) {
		return &lifecycleRecorder{id: "core", order: &order}, nil
	}}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(RuntimeComponent{ID: "coordinator", Requires: []string{"core"}, New: func(context.Context) (ManagedComponent, error) {
		return panicStopComponent{}, nil
	}}); err != nil {
		t.Fatal(err)
	}
	runtime, err := Compose(context.Background(), BuildPlan{Components: []string{"core", "coordinator"}}, registry, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	err = runtime.Close(context.Background())
	if !errors.Is(err, ErrComponentPanic) {
		t.Fatalf("stop panic was not returned as a controlled error: %v", err)
	}
	if !reflect.DeepEqual(order, []string{"start:core", "stop:core"}) {
		t.Fatalf("stop panic skipped dependency cleanup: %v", order)
	}
}

func TestComposeContainsFailureSourcePanicAndRollsBack(t *testing.T) {
	var order []string
	registry := NewRuntimeRegistry()
	if err := registry.Register(RuntimeComponent{ID: "core", New: func(context.Context) (ManagedComponent, error) {
		component := &lifecycleRecorder{id: "core", order: &order}
		component.ready.Store(true)
		return component, nil
	}}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(RuntimeComponent{ID: "coordinator", Requires: []string{"core"}, New: func(context.Context) (ManagedComponent, error) {
		component := &lifecycleRecorder{id: "coordinator", order: &order}
		component.ready.Store(true)
		return &panicFailureSource{lifecycleRecorder: component}, nil
	}}); err != nil {
		t.Fatal(err)
	}
	_, err := Compose(context.Background(), BuildPlan{Components: []string{"core", "coordinator"}}, registry, time.Second)
	if !errors.Is(err, ErrComponentPanic) || strings.Contains(err.Error(), "sensitive monitoring failure") {
		t.Fatalf("failure source panic was not contained: %v", err)
	}
	want := []string{"start:core", "start:coordinator", "stop:coordinator", "stop:core"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("startup was not rolled back after failure source panic: got=%v want=%v", order, want)
	}
}

func TestComposeRejectsNilFailureStreamAndRollsBack(t *testing.T) {
	var order []string
	registry := NewRuntimeRegistry()
	if err := registry.Register(RuntimeComponent{ID: "core", New: func(context.Context) (ManagedComponent, error) {
		component := &lifecycleRecorder{id: "core", order: &order}
		component.ready.Store(true)
		return component, nil
	}}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(RuntimeComponent{ID: "coordinator", Requires: []string{"core"}, New: func(context.Context) (ManagedComponent, error) {
		component := &lifecycleRecorder{id: "coordinator", order: &order}
		component.ready.Store(true)
		return &nilFailureSource{lifecycleRecorder: component}, nil
	}}); err != nil {
		t.Fatal(err)
	}
	_, err := Compose(context.Background(), BuildPlan{Components: []string{"core", "coordinator"}}, registry, time.Second)
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("nil failure stream was accepted: %v", err)
	}
	want := []string{"start:core", "start:coordinator", "stop:coordinator", "stop:core"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("startup was not rolled back after nil failure stream: got=%v want=%v", order, want)
	}
}

func TestClosedFailureSourceMarksRuntimeUnready(t *testing.T) {
	var order []string
	failureComponent := &fatalRecorder{lifecycleRecorder: &lifecycleRecorder{id: "coordinator", order: &order}, failures: make(chan error, 1)}
	failureComponent.ready.Store(true)
	registry := NewRuntimeRegistry()
	if err := registry.Register(RuntimeComponent{ID: "core", New: func(context.Context) (ManagedComponent, error) {
		component := &lifecycleRecorder{id: "core", order: &order}
		component.ready.Store(true)
		return component, nil
	}}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(RuntimeComponent{ID: "coordinator", Requires: []string{"core"}, New: func(context.Context) (ManagedComponent, error) {
		return failureComponent, nil
	}}); err != nil {
		t.Fatal(err)
	}
	runtime, err := Compose(context.Background(), BuildPlan{Components: []string{"core", "coordinator"}}, registry, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	close(failureComponent.failures)
	select {
	case <-runtime.Failures():
	case <-time.After(time.Second):
		t.Fatal("closed failure stream did not fail the runtime")
	}
	if runtime.Ready() {
		t.Fatal("runtime remained ready after its failure stream closed")
	}
	if err := runtime.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}
