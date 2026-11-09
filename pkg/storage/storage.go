// Package storage defines the object-storage contract used by media.
package storage

import (
	"context"
	"io"
	"time"
)

type PutOptions struct {
	ContentType  string
	CacheControl string
}

// ObjectStore stores binary media. Implementations must not expose secrets in
// URLs and must generate public URLs only from trusted configuration.
type ObjectStore interface {
	Put(ctx context.Context, key string, body io.Reader, size int64, opts PutOptions) error
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	Delete(ctx context.Context, key string) error
	PublicURL(key string) (string, error)
	Ready(ctx context.Context) error
}

// RangeStore allows large object delivery without downloading a prefix.
// ObjectStore-only implementations can still stream originals with a bounded
// memory fallback; production S3 implements this interface.
type RangeStore interface {
	Stat(context.Context, string) (ObjectInfo, error)
	GetRange(context.Context, string, int64, int64) (io.ReadCloser, error)
}

type ObjectInfo struct {
	Size        int64
	ContentType string
}

// ComposeStore assembles already verified parts in storage without requiring
// a second full local copy. CleanupMultipart reclaims interrupted compositions.
type ComposeStore interface {
	Compose(context.Context, string, []string, PutOptions) error
	CleanupMultipart(context.Context, time.Time) error
}
