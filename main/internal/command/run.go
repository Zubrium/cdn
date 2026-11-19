// Package command implements the zubrium command-line entrypoint.
package command

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	platform "github.com/Zubrium/cdn/core/presentation"
	"github.com/Zubrium/cdn/main/internal/composition"
	config "github.com/Zubrium/cdn/main/internal/config"
)

func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && args[0] == "--version" {
		fmt.Fprintln(stdout, "zubrium dev_v2")
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
		resolved, err := cfg.Resolve(os.Getenv)
		if err != nil {
			fmt.Fprintln(stderr, "invalid configuration:", err)
			return 1
		}
		if _, err := resolved.WithBuildPlan(composition.BuildPlan()); err != nil {
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
		buildPlan := composition.BuildPlan()
		if args[0] == "--config" {
			resolved, err = resolved.WithBuildPlan(buildPlan)
			if err != nil {
				fmt.Fprintln(stderr, "invalid configuration:", err)
				return 1
			}
		}
		if args[0] == "--recover-admin" {
			ctx, cancel := context.WithTimeout(context.Background(), resolved.Core.Database.ConnectTimeout)
			defer cancel()
			secret, err := platform.RecoverBootstrap(ctx, resolved.Core.Database)
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
		componentRegistry, components, err := composition.RegisterComponents(resolved, buildPlan)
		if err != nil {
			fmt.Fprintln(stderr, "Coordinator registration failed")
			return 1
		}
		application, err := platform.Compose(ctx, buildPlan, componentRegistry, resolved.HTTP.ShutdownTimeout)
		if err != nil {
			fmt.Fprintln(stderr, "application startup failed:", err)
			return 1
		}
		components.SetApplicationReadiness(application)
		closed := false
		defer func() {
			if !closed {
				shutdownCtx, cancel := context.WithTimeout(context.Background(), resolved.HTTP.ShutdownTimeout)
				defer cancel()
				_ = application.Close(shutdownCtx)
			}
		}()
		if secret := components.InitialAdminSecret(); secret != "" {
			if _, err := fmt.Fprintln(stderr, "Initial administrator secret (shown once):", secret); err != nil {
				shutdownCtx, cancel := context.WithTimeout(context.Background(), resolved.HTTP.ShutdownTimeout)
				_ = application.Close(shutdownCtx)
				cancel()
				closed = true
				return 1
			}
		}
		if _, err := fmt.Fprintln(stdout, "Core, Coordinator, Loader, and File API listening on", components.HTTPAddress()); err != nil {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), resolved.HTTP.ShutdownTimeout)
			_ = application.Close(shutdownCtx)
			cancel()
			closed = true
			return 1
		}
		select {
		case <-ctx.Done():
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), resolved.HTTP.ShutdownTimeout)
			err := application.Close(shutdownCtx)
			shutdownCancel()
			closed = true
			if err != nil {
				fmt.Fprintln(stderr, "application shutdown failed:", err)
				return 1
			}
			return 0
		case err, ok := <-application.Failures():
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), resolved.HTTP.ShutdownTimeout)
			shutdownErr := application.Close(shutdownCtx)
			shutdownCancel()
			closed = true
			if ok && err != nil {
				fmt.Fprintln(stderr, "application component failed:", err)
			}
			if shutdownErr != nil {
				fmt.Fprintln(stderr, "application shutdown failed:", shutdownErr)
			}
			return 1
		}
	}
	fmt.Fprintln(stderr, "zubrium: provide --config <path>, --check-config <path>, or --recover-admin <path>")
	return 1
}
