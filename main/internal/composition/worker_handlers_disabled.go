//go:build !full || devfixtures || router

package composition

import platform "github.com/Zubrium/cdn/core/presentation"

func registerWorkerHandlers(*platform.RuntimeRegistry, map[string]struct{}) error { return nil }
