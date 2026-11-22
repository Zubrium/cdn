//go:build devfixtures && !full && !router

package composition

import "github.com/Zubrium/cdn/core/presentation"

func RegisterProfile(registry *presentation.Registry) error {
	return RegisterGeneratedEngines(registry)
}

func BuildPlan() presentation.BuildPlan { return GeneratedBuildPlan() }

func ProfileName() string { return "development-fixtures" }

func DevelopmentFixturesEnabled() bool { return GeneratedDevelopmentFixtures() }
