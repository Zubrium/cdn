//go:build !full || devfixtures || router

package composition

import (
	appconfig "github.com/Zubrium/cdn/main/internal/config"
	"github.com/Zubrium/cdn/pkg/contracts"
	"github.com/Zubrium/cdn/pkg/observability"
)

func registerWorkerHandlers(map[string]struct{}, appconfig.Resolved, *coreComponent, *observability.Observer) (map[contracts.Key]TaskOperationBinding, error) {
	return map[contracts.Key]TaskOperationBinding{}, nil
}
