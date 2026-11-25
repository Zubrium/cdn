//go:build full && !devfixtures && !router

package composition

import (
	"testing"

	appconfig "github.com/Zubrium/cdn/main/internal/config"
)

func TestContentComponentHasNoLateBoundWebsiteHandles(t *testing.T) {
	content := newContentEngineComponent(nil, appconfig.Resolved{})
	if content.projections != nil {
		t.Fatal("Content component must not retain a Website projection handle before Start")
	}
	website := newWebsiteEngineComponent(nil, appconfig.Resolved{})
	if website.localeCatalog != nil || website.contentBuildView != nil {
		t.Fatal("Website build clients must not be initialized before trusted Core capabilities exist")
	}
}
