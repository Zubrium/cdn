package main

import (
	contentEngine "github.com/Zubrium/cdn/engines/content/presentation"
	formsEngine "github.com/Zubrium/cdn/engines/forms/presentation"
	websiteEngine "github.com/Zubrium/cdn/engines/website/presentation"
)

type developmentFixtureAPIs struct {
	website websiteEngine.API
	content contentEngine.API
	forms   formsEngine.API
}
