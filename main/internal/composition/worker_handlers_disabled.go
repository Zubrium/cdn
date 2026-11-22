//go:build !full || devfixtures || router

package composition

import (
	platform "github.com/Zubrium/cdn/core/presentation"
	appconfig "github.com/Zubrium/cdn/main/internal/config"
)

func registerWorkerHandlers(*platform.RuntimeRegistry, map[string]struct{}, appconfig.Resolved, *coreComponent) error {
	return nil
}
