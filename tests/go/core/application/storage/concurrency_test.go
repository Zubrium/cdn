package storage

import (
	"context"
	"errors"
	"testing"
	"time"

	appcache "github.com/Zubrium/cdn/core/application/cache"
	"github.com/Zubrium/cdn/core/domain/access"
)

type noopCacheObserver struct{}

func (noopCacheObserver) IncCounter(context.Context, string, float64, ...appcache.Label) {}
func (noopCacheObserver) SetGauge(context.Context, string, float64, ...appcache.Label)   {}
func (noopCacheObserver) ObserveHistogram(context.Context, string, float64, ...appcache.Label) {
}

type blockingMetadata struct {
	*fakeMetadata
	entered chan struct{}
	release chan struct{}
}

func (b *blockingMetadata) Authorize(ctx context.Context, principalID, fileID string, action access.Action) (int64, []string, error) {
	close(b.entered)
	<-b.release
	return b.fakeMetadata.Authorize(ctx, principalID, fileID, action)
}

// TestStorageConcurrencyLimitsRejectBeforeRepositoryCall proves the configured
// process-local bounds are applied before a request reaches the repository.
func TestStorageConcurrencyLimitsRejectBeforeRepositoryCall(t *testing.T) {
	service, metadata, bytes, creator, writer, deleter, byteCache := testService()
	blocking := &blockingMetadata{fakeMetadata: metadata, entered: make(chan struct{}), release: make(chan struct{})}
	service = NewService(blocking, bytes, creator, writer, deleter, byteCache, 100, 128, 8, time.Minute).
		WithConcurrency(1, 1, 25*time.Millisecond, noopCacheObserver{})

	firstDone := make(chan error, 1)
	go func() {
		_, err := service.Stat(context.Background(), metadata.principal, metadata.object.ID)
		firstDone <- err
	}()
	<-blocking.entered

	started := time.Now()
	_, err := service.Stat(context.Background(), metadata.principal, metadata.object.ID)
	elapsed := time.Since(started)
	if !errors.Is(err, ErrOverloaded) {
		t.Fatalf("second authorization error = %v, want ErrOverloaded", err)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("overload rejection waited %v, want the explicit operation timeout", elapsed)
	}
	if metadata.authorizations != 0 {
		t.Fatalf("rejected request reached the repository %d times", metadata.authorizations)
	}
	close(blocking.release)
	if err := <-firstDone; err != nil {
		t.Fatalf("admitted authorization failed: %v", err)
	}
	if metadata.authorizations != 1 {
		t.Fatalf("repository authorizations = %d, want 1", metadata.authorizations)
	}
}
