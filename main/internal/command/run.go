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
	"github.com/Zubrium/cdn/pkg/observability"
)

func closeApplication(observer *observability.Observer, application *platform.ComponentApplication, ctx context.Context) error {
	if application == nil {
		return nil
	}
	return observer.Operation(ctx, observability.Identity{
		ModuleKind: observability.ModuleKindComposition, ModuleID: "composition", ComponentID: "runtime",
		OperationID: "composition.close",
	}, "component.lifecycle", nil, func(context.Context) error { return application.Close(ctx) })
}

func parseRunConfigArguments(args []string) (string, bool, bool) {
	if len(args) == 2 && args[0] == "--config" && args[1] != "" {
		return args[1], false, true
	}
	if len(args) == 3 && args[0] == "--suppress-bootstrap-secret" && args[1] == "--config" && args[2] != "" {
		return args[2], true, true
	}
	return "", false, false
}

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
	configPath, suppressBootstrapSecret, runConfigured := parseRunConfigArguments(args)
	recoverAdmin := len(args) == 2 && args[0] == "--recover-admin" && args[1] != ""
	if runConfigured || recoverAdmin {
		if recoverAdmin {
			configPath = args[1]
		}
		file, err := os.Open(configPath)
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
		if recoverAdmin {
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
		observabilityConfig := resolved.Observability
		observabilityConfig.Profile = composition.ProfileName()
		observabilityConfig.BranchID = resolved.RouterAPI.BranchID
		observer, err := observability.New(observabilityConfig)
		if err != nil {
			fmt.Fprintln(stderr, "observability initialization failed:", err)
			return 1
		}
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), resolved.HTTP.ShutdownTimeout)
			defer cancel()
			_ = observer.Close(shutdownCtx)
		}()
		observer.Log(ctx, observability.LevelInfo, "application starting",
			observability.String("deployment.profile", composition.ProfileName()),
			observability.String("router.branch", resolved.RouterAPI.BranchID))
		componentRegistry, components, err := composition.RegisterComponents(resolved, buildPlan, observer)
		if err != nil {
			observer.Log(ctx, observability.LevelError, "component registration failed", observability.String("error.message", err.Error()))
			fmt.Fprintln(stderr, "Coordinator registration failed")
			return 1
		}
		var application *platform.ComponentApplication
		err = observer.Operation(ctx, observability.Identity{
			ModuleKind: observability.ModuleKindComposition, ModuleID: "composition", ComponentID: "runtime",
			OperationID: "composition.compose",
		}, "component.lifecycle", nil, func(operationCtx context.Context) error {
			var composeErr error
			application, composeErr = platform.Compose(operationCtx, buildPlan, componentRegistry, resolved.HTTP.ShutdownTimeout)
			return composeErr
		})
		if err != nil {
			observer.Log(ctx, observability.LevelError, "application startup failed", observability.String("error.message", err.Error()))
			fmt.Fprintln(stderr, "application startup failed:", err)
			return 1
		}
		observer.Log(ctx, observability.LevelInfo, "application started")
		components.SetApplicationReadiness(application)
		closed := false
		defer func() {
			if !closed {
				shutdownCtx, cancel := context.WithTimeout(context.Background(), resolved.HTTP.ShutdownTimeout)
				defer cancel()
				_ = closeApplication(observer, application, shutdownCtx)
			}
		}()
		if secret := components.InitialAdminSecret(); secret != "" && !suppressBootstrapSecret {
			if _, err := fmt.Fprintln(stderr, "Initial administrator secret (shown once):", secret); err != nil {
				shutdownCtx, cancel := context.WithTimeout(context.Background(), resolved.HTTP.ShutdownTimeout)
				_ = closeApplication(observer, application, shutdownCtx)
				cancel()
				closed = true
				return 1
			}
		}
		if _, err := fmt.Fprintln(stdout, "Core, Coordinator, Loader, and File API listening on", components.AdminAddress()); err != nil {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), resolved.HTTP.ShutdownTimeout)
			_ = closeApplication(observer, application, shutdownCtx)
			cancel()
			closed = true
			return 1
		}
		select {
		case <-ctx.Done():
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), resolved.HTTP.ShutdownTimeout)
			err := closeApplication(observer, application, shutdownCtx)
			shutdownCancel()
			closed = true
			if err != nil {
				fmt.Fprintln(stderr, "application shutdown failed:", err)
				return 1
			}
			return 0
		case err, ok := <-application.Failures():
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), resolved.HTTP.ShutdownTimeout)
			shutdownErr := closeApplication(observer, application, shutdownCtx)
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
	fmt.Fprintln(stderr, "zubrium: provide --config <path>, --suppress-bootstrap-secret --config <path>, --check-config <path>, or --recover-admin <path>")
	return 1
}
