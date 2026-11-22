package composition

import (
	"context"
	"errors"
	"sync"

	websiteapp "github.com/Zubrium/cdn/engines/website/application"
)

var errContentBuildProjectionUnavailable = errors.New("Content build projection is not initialized")

// websiteContentBuildProjection is the late-bound, trusted Content capability
// used by Website's durable build resolver. Composition activates it only
// after the Content engine and its authenticated service context are ready.
type websiteContentBuildProjection struct {
	mu     sync.RWMutex
	ctx    context.Context
	source websiteapp.ContentBuildProjectionResolver
}

func newWebsiteContentBuildProjection() *websiteContentBuildProjection {
	return &websiteContentBuildProjection{}
}

func (p *websiteContentBuildProjection) setSource(ctx context.Context, source websiteapp.ContentBuildProjectionResolver) {
	if p == nil || ctx == nil || source == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ctx, p.source = ctx, source
}

func (p *websiteContentBuildProjection) ResolveContentBuildProjection(ctx context.Context, pageSnapshotID, locale string) (websiteapp.ContentBuildProjection, error) {
	if p == nil || ctx == nil || pageSnapshotID == "" || locale == "" {
		return websiteapp.ContentBuildProjection{}, errContentBuildProjectionUnavailable
	}
	if err := ctx.Err(); err != nil {
		return websiteapp.ContentBuildProjection{}, err
	}
	p.mu.RLock()
	source, sourceCtx := p.source, p.ctx
	p.mu.RUnlock()
	if source == nil || sourceCtx == nil {
		return websiteapp.ContentBuildProjection{}, errContentBuildProjectionUnavailable
	}
	return source.ResolveContentBuildProjection(sourceCtx, pageSnapshotID, locale)
}
