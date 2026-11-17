//go:build !full

package main

import (
	"github.com/Zubrium/cdn/coordinator"
	"github.com/Zubrium/cdn/core/platform"
)

func registerProfile(registry *platform.Registry) error { return RegisterGeneratedEngines(registry) }

func compiledBuildPlan() coordinator.BuildPlan { return GeneratedBuildPlan() }
