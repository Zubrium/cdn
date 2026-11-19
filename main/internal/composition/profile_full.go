//go:build full && !devfixtures && !router

package composition

import "github.com/Zubrium/cdn/core/presentation"

func RegisterProfile(registry *presentation.Registry) error {
	return RegisterGeneratedEngines(registry)
}

func BuildPlan() presentation.BuildPlan { return GeneratedBuildPlan() }

func DevelopmentFixturesEnabled() bool { return GeneratedDevelopmentFixtures() }
