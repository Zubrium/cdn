package runtime_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Zubrium/cdn/engines/website/features/runtime"
)

func TestCommandExecutorIsFailClosedOnConfiguration(t *testing.T) {
	for _, config := range []runtime.CommandConfig{
		{},
		{Binary: "node", MaxOutputBytes: 1024},
		{Binary: "/usr/bin/node\n--version", MaxOutputBytes: 1024},
		{Binary: "/usr/bin/node", MaxOutputBytes: 0},
		{Binary: "/usr/bin/node", MaxOutputBytes: 1 << 30},
	} {
		if err := config.Validate(); err == nil {
			t.Fatalf("invalid config accepted: %+v", config)
		}
	}
	executor, err := runtime.NewCommandExecutor(runtime.CommandConfig{Binary: "/nonexistent/supervised-feature-host", MaxOutputBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	if err := executor.Execute(context.Background(), runtime.OpRender, runtime.FeatureContext{}); !errors.Is(err, runtime.ErrExecutor) {
		t.Fatalf("missing supervised process: err=%v", err)
	}
}

func TestCommandConfigAcceptsSupervisedAbsoluteBinary(t *testing.T) {
	config := runtime.CommandConfig{Binary: "/usr/local/bin/feature-host", Args: []string{"--envelope", "stdin"}, MaxOutputBytes: 1 << 16}
	if err := config.Validate(); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.NewCommandExecutor(config); err != nil {
		t.Fatal(err)
	}
}
