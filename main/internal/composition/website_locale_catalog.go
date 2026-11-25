package composition

import (
	"context"
	"errors"

	"github.com/Zubrium/cdn/main/internal/contentbridge"
	"github.com/Zubrium/cdn/pkg/contracts"
)

var errLocaleCatalogUnavailable = errors.New("Website locale catalog bridge is not initialized")

// websiteLocaleCatalog is Website's client for the authoritative Content
// locale catalog. It holds no Content handle and dispatches no Content-owned
// operation; the owner-selected workflow declares the Content child task.
type websiteLocaleCatalog struct {
	bridge *contentbridge.Client
}

func newWebsiteLocaleCatalog(bridge *contentbridge.Client) *websiteLocaleCatalog {
	return &websiteLocaleCatalog{bridge: bridge}
}

func (catalog *websiteLocaleCatalog) LocaleCatalogRevision(ctx context.Context, locale string) (int64, error) {
	if catalog == nil || catalog.bridge == nil || ctx == nil || locale == "" {
		return 0, errLocaleCatalogUnavailable
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	response, err := catalog.bridge.ReadLocale(ctx, contracts.ContentLocaleCatalogRequest{})
	if err != nil {
		return 0, err
	}
	if response.Revision < 1 {
		return 0, errLocaleCatalogUnavailable
	}
	for _, configured := range response.Locales {
		if configured == locale {
			return response.Revision, nil
		}
	}
	return 0, errLocaleCatalogUnavailable
}
