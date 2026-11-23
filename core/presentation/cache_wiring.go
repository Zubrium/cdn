package presentation

import (
	"context"
	"errors"
	"io"

	appcache "github.com/Zubrium/cdn/core/application/cache"
	"github.com/Zubrium/cdn/core/domain/access"
	"github.com/Zubrium/cdn/core/domain/files"
	"github.com/Zubrium/cdn/core/infrastructure/cache"
)

// newCacheStoreConfig is the single production wiring point for Core's bounded
// caches. Every dependency is injected explicitly; a missing observer is
// rejected by StoreConfig.Validate instead of being replaced by a no-op sink.
func newCacheStoreConfig(cfg RuntimeConfig, epoch appcache.EpochSource) appcache.StoreConfig {
	storeConfig := cfg.CacheStore
	storeConfig.Epoch = epoch
	storeConfig.Observer = cfg.CacheObserver
	storeConfig.CacheFactory = cache.Factory{}
	return storeConfig
}

// coreAuthorizer adapts the existing authoritative Core access check to the
// application-owned cache port. It also reads epoch and revision in one
// database snapshot so a grant change cannot be split across two reads.
type coreAuthorizer struct {
	metadata files.MetadataReader
	epoch    appcache.EpochSource
}

var _ appcache.Authorizer = coreAuthorizer{}

func (a coreAuthorizer) Authorize(ctx context.Context, principalID, fileID, action string) (appcache.Authorization, error) {
	if a.metadata == nil || a.epoch == nil {
		return appcache.Authorization{}, errors.New("core cache authorizer is not configured")
	}
	revision, groups, err := a.metadata.Authorize(ctx, principalID, fileID, access.Action(action))
	if err != nil {
		return appcache.Authorization{}, err
	}
	epoch, stateRevision, err := a.epoch.AccessState(ctx, principalID, fileID)
	if err != nil {
		return appcache.Authorization{}, err
	}
	if stateRevision != revision {
		return appcache.Authorization{}, errors.New("authorization state changed during check")
	}
	return appcache.Authorization{
		PrincipalID: principalID, FileID: fileID, Action: action,
		Revision: revision, AccessEpoch: epoch, Groups: append([]string(nil), groups...),
	}, nil
}

// coreMetadataSource adapts the existing metadata repository for warm-up.
type coreMetadataSource struct{ metadata files.MetadataReader }

var _ appcache.MetadataSource = coreMetadataSource{}

func (m coreMetadataSource) Metadata(ctx context.Context, fileID string, auth appcache.Authorization) (appcache.Metadata, error) {
	if m.metadata == nil {
		return appcache.Metadata{}, errors.New("core cache metadata source is not configured")
	}
	object, err := m.metadata.Find(ctx, fileID)
	if err != nil {
		return appcache.Metadata{}, err
	}
	if object.ID != fileID || object.Revision != auth.Revision || !object.Ready {
		return appcache.Metadata{}, errors.New("metadata does not match the authorization snapshot")
	}
	return appcache.Metadata{
		FileID: object.ID, Key: object.Key, Revision: object.Revision, Size: int(object.Size),
		Ready: object.Ready, Groups: append([]string(nil), object.Groups...),
		Metadata: cloneStringMap(object.Metadata), SHA256: object.SHA256,
		ContentType: object.ContentType, State: object.State,
	}, nil
}

// coreBytesSource adapts S3-backed reads for bounded byte warm-up. Oversized
// objects are refused by the application cache before this source is called.
type coreBytesSource struct{ bytes files.ByteReader }

var _ appcache.BytesSource = coreBytesSource{}

func (b coreBytesSource) Bytes(ctx context.Context, fileID string, metadata appcache.Metadata) ([]byte, error) {
	if b.bytes == nil || metadata.Key == "" {
		return nil, errors.New("core cache byte source is not configured")
	}
	reader, err := b.bytes.Read(ctx, metadata.Key)
	if err != nil {
		return nil, err
	}
	data, readErr := io.ReadAll(io.LimitReader(reader, int64(metadata.Size)+1))
	closeErr := reader.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if len(data) != metadata.Size {
		return nil, errors.New("byte length does not match verified metadata")
	}
	return data, nil
}

func cloneStringMap(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
