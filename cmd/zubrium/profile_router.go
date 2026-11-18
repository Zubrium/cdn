//go:build router && !full && !devfixtures

package main

import "github.com/Zubrium/cdn/core/presentation"

func registerProfile(registry *presentation.Registry) error {
	return RegisterGeneratedEngines(registry)
}

func compiledBuildPlan() presentation.BuildPlan { return GeneratedBuildPlan() }

func compiledDevelopmentFixtures() bool { return GeneratedDevelopmentFixtures() }
