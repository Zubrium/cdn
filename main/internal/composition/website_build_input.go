package composition

import (
	"context"
	"errors"

	websiteapp "github.com/Zubrium/cdn/engines/website/application"
	"github.com/Zubrium/cdn/main/internal/contentbridge"
	"github.com/Zubrium/cdn/pkg/contracts"
)

var errContentBuildProjectionUnavailable = errors.New("Content build projection bridge is not initialized")

// websiteContentBuildProjection is Website's client for the trusted Content
// build projection. It holds no Content handle and dispatches no Content-owned
// operation; the owner-selected workflow declares the Content child task.
type websiteContentBuildProjection struct {
	bridge *contentbridge.Client
}

func newWebsiteContentBuildProjection(bridge *contentbridge.Client) *websiteContentBuildProjection {
	return &websiteContentBuildProjection{bridge: bridge}
}

func (p *websiteContentBuildProjection) resolve(ctx context.Context, pageSnapshotID, locale string) (contracts.ContentBuildProjectionResponse, error) {
	return p.resolvePinned(ctx, pageSnapshotID, locale, 0)
}

func (p *websiteContentBuildProjection) resolvePinned(ctx context.Context, pageSnapshotID, locale string, expectedPublicationRevision int64) (contracts.ContentBuildProjectionResponse, error) {
	if p == nil || p.bridge == nil || ctx == nil || pageSnapshotID == "" || locale == "" || expectedPublicationRevision < 0 {
		return contracts.ContentBuildProjectionResponse{}, errContentBuildProjectionUnavailable
	}
	if err := ctx.Err(); err != nil {
		return contracts.ContentBuildProjectionResponse{}, err
	}
	return p.bridge.ReadBuild(ctx, contracts.ContentBuildProjectionRequest{
		PageSnapshotID: pageSnapshotID, Locale: locale,
		ExpectedPublicationRevision: expectedPublicationRevision,
	})
}

type websiteContentBuildProjectionAdapter struct {
	client *websiteContentBuildProjection
}

func (adapter websiteContentBuildProjectionAdapter) ResolveContentBuildProjection(ctx context.Context, pageSnapshotID, locale string) (websiteapp.ContentBuildProjection, error) {
	return adapter.ResolveContentBuildProjectionPinned(ctx, pageSnapshotID, locale, 0)
}

func (adapter websiteContentBuildProjectionAdapter) ResolveContentBuildProjectionPinned(ctx context.Context, pageSnapshotID, locale string, expectedPublicationRevision int64) (websiteapp.ContentBuildProjection, error) {
	if adapter.client == nil {
		return websiteapp.ContentBuildProjection{}, errContentBuildProjectionUnavailable
	}
	response, err := adapter.client.resolvePinned(ctx, pageSnapshotID, locale, expectedPublicationRevision)
	if err != nil {
		return websiteapp.ContentBuildProjection{}, err
	}
	return websiteapp.ContentBuildProjection{
		PublicationRevision: response.PublicationRevision, SelectionDigest: response.SelectionDigest,
	}, nil
}

var _ websiteapp.PinnedContentBuildProjectionResolver = websiteContentBuildProjectionAdapter{}
