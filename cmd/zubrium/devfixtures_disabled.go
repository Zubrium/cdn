//go:build !devfixtures

package main

import (
	"errors"

	appconfig "github.com/Zubrium/cdn/cmd/zubrium/config"
	platform "github.com/Zubrium/cdn/core/presentation"
)

func createDevelopmentFixtures(*platform.Runtime, appconfig.Resolved) (*developmentFixtureAPIs, error) {
	return nil, errors.New("development fixtures require the devfixtures build tag")
}
