//go:build devfixtures

package main

import (
	"errors"

	appconfig "github.com/Zubrium/cdn/cmd/zubrium/config"
	platform "github.com/Zubrium/cdn/core/presentation"
	"github.com/Zubrium/cdn/cmd/zubrium/devfixtures"
)

func createDevelopmentFixtures(runtime *platform.Runtime, config appconfig.Resolved) (*developmentFixtureAPIs, error) {
	if runtime == nil || !compiledDevelopmentFixtures() {
		return nil, errors.New("development fixtures are unavailable in this build profile")
	}
	fixtures, err := devfixtures.New(runtime, devfixtures.Config{ConfiguredGroups: config.Core.AccessGroups})
	if err != nil {
		return nil, err
	}
	return &developmentFixtureAPIs{website: fixtures.Website, content: fixtures.Content, forms: fixtures.Forms}, nil
}
