//go:build !full || devfixtures || router

package composition

import (
	platform "github.com/Zubrium/cdn/core/presentation"
	"github.com/Zubrium/cdn/main/internal/bus"
	appconfig "github.com/Zubrium/cdn/main/internal/config"
	"github.com/Zubrium/cdn/pkg/contracts"
	"github.com/Zubrium/cdn/pkg/observability"
)

func registerWorkerHandlers(map[string]struct{}, appconfig.Resolved, *coreComponent, *observability.Observer) (map[contracts.Key]TaskOperationBinding, error) {
	return map[contracts.Key]TaskOperationBinding{}, nil
}

func compiledWorkerClasses(map[string]struct{}) []string { return nil }

func workerTaskPools(appconfig.Resolved, *bus.Dispatcher, map[contracts.Key]TaskOperationBinding) ([]platform.TaskPoolConfig, error) {
	return nil, nil
}
