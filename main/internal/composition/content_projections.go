package composition

import (
	"context"
	"errors"

	contentapi "github.com/Zubrium/cdn/engines/content/presentation"
	"github.com/Zubrium/cdn/main/internal/contentbridge"
	"github.com/Zubrium/cdn/pkg/contracts"
)

var errContentProjectionClientUnavailable = errors.New("Website content projection bridge is not initialized")

// contentProjectionClient implements Content's trusted projection port through
// the neutral workflow bridge. It holds no Website handle and dispatches no
// Website-owned operation; the owner-selected Core workflow declares the
// class-scoped child that reads the projection.
type contentProjectionClient struct {
	bridge *contentbridge.Client
	locale string
}

func newContentProjectionClient(bridge *contentbridge.Client, locale string) (*contentProjectionClient, error) {
	if bridge == nil || locale == "" {
		return nil, errContentProjectionClientUnavailable
	}
	return &contentProjectionClient{bridge: bridge, locale: locale}, nil
}

func (client *contentProjectionClient) GetContentProjections(ctx context.Context, pageSnapshotID string) (contentapi.Projections, error) {
	return client.GetContentProjectionsForLocale(ctx, pageSnapshotID, client.locale)
}

func (client *contentProjectionClient) GetContentProjectionsForLocale(ctx context.Context, pageSnapshotID, locale string) (contentapi.Projections, error) {
	return client.getContentProjectionsForLocaleAllowState(ctx, pageSnapshotID, locale, false)
}

func (client *contentProjectionClient) getContentProjectionsForLocaleAllowState(ctx context.Context, pageSnapshotID, locale string, allowProvisioning bool) (contentapi.Projections, error) {
	if client == nil || client.bridge == nil || ctx == nil || pageSnapshotID == "" || locale == "" {
		return contentapi.Projections{}, contentapi.ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return contentapi.Projections{}, err
	}
	response, err := client.bridge.ReadProjection(ctx, contracts.ContentProjectionRequest{
		PageSnapshotID: pageSnapshotID, Locale: locale, AllowProvisioning: allowProvisioning,
	})
	if err != nil {
		return contentapi.Projections{}, err
	}
	projection := decodeContentProjection(response.Projection)
	if !projection.VerifyScope(pageSnapshotID) || projection.Locale != locale {
		return contentapi.Projections{}, contentapi.ErrUnavailable
	}
	return projection, nil
}

var _ contentapi.ProjectionsProvider = (*contentProjectionClient)(nil)
