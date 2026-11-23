//go:build full && !devfixtures && !router

package composition

import (
	"testing"

	appconfig "github.com/Zubrium/cdn/main/internal/config"
)

func TestWebsiteBuildCapabilitiesAreAvailableBeforeComponentStart(t *testing.T) {
	website := newWebsiteEngineComponent(nil, appconfig.Resolved{})
	if website.localeCatalog == nil || website.contentBuildView == nil {
		t.Fatal("Website build capabilities must be initialized before dependent components are registered")
	}
	content := newContentEngineComponent(nil, appconfig.Resolved{}, nil)
	content.buildLocaleCatalog = website.localeCatalog
	content.buildProjection = website.contentBuildView
	if content.buildLocaleCatalog != website.localeCatalog || content.buildProjection != website.contentBuildView {
		t.Fatal("Content must retain the Website build capability instances created during composition")
	}
}
