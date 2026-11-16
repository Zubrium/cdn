// Command zubrium is the composition root of the new platform.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	config "github.com/Zubrium/cdn/cmd/zubrium/config"
	"github.com/Zubrium/cdn/core/platform"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && args[0] == "--version" {
		fmt.Fprintln(stdout, "zubrium dev_v2 (incomplete)")
		return 0
	}
	if len(args) == 2 && args[0] == "--check-config" {
		file, err := os.Open(args[1])
		if err != nil {
			fmt.Fprintln(stderr, "cannot open configuration")
			return 1
		}
		defer file.Close()
		cfg, err := config.Load(file)
		if err != nil {
			fmt.Fprintln(stderr, "invalid configuration")
			return 1
		}
		if _, err := cfg.Resolve(os.Getenv); err != nil {
			fmt.Fprintln(stderr, "invalid configuration:", err)
			return 1
		}
		fmt.Fprintln(stdout, "configuration is valid")
		return 0
	}
	if len(args) == 2 && (args[0] == "--config" || args[0] == "--recover-admin") {
		file, err := os.Open(args[1])
		if err != nil {
			fmt.Fprintln(stderr, "cannot open configuration")
			return 1
		}
		cfg, err := config.Load(file)
		_ = file.Close()
		if err != nil {
			fmt.Fprintln(stderr, "invalid configuration")
			return 1
		}
		resolved, err := cfg.Resolve(os.Getenv)
		if err != nil {
			fmt.Fprintln(stderr, "invalid configuration:", err)
			return 1
		}
		if args[0] == "--recover-admin" {
			ctx, cancel := context.WithTimeout(context.Background(), resolved.Database.ConnectTimeout)
			defer cancel()
			secret, err := platform.RecoverBootstrap(ctx, resolved.Database)
			if err != nil {
				fmt.Fprintln(stderr, "administrator recovery failed:", err)
				return 1
			}
			if _, err := fmt.Fprintln(stderr, "Administrator recovery secret (shown once):", secret); err != nil {
				return 1
			}
			return 0
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		runtime, secret, err := platform.Start(ctx, resolved)
		if err != nil {
			fmt.Fprintln(stderr, "core startup failed:", err)
			return 1
		}
		defer runtime.Close()
		if secret != "" {
			if _, err := fmt.Fprintln(stderr, "Initial administrator secret (shown once):", secret); err != nil {
				return 1
			}
		}
		if err := registerProfile(runtime.Registry); err != nil {
			fmt.Fprintln(stderr, "module registration failed:", err)
			return 1
		}
		fmt.Fprintln(stdout, "Core initialized; engine and presentation modules are not yet active")
		for {
			select {
			case <-ctx.Done():
				return 0
			case err := <-runtime.Errors():
				fmt.Fprintln(stderr, err)
			}
		}
	}
	fmt.Fprintln(stderr, "zubrium: provide --config <path>, --check-config <path>, or --recover-admin <path>")
	return 1
}
