//go:build !devfixtures

package composition

import (
	"errors"

	platform "github.com/Zubrium/cdn/core/presentation"
	appconfig "github.com/Zubrium/cdn/main/internal/config"
)

func createDevelopmentFixtures(*platform.Runtime, appconfig.Resolved) (*developmentFixtureAPIs, error) {
	return nil, errors.New("development fixtures require the devfixtures build tag")
}
