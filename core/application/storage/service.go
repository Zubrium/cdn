// Package storage implements Core file use cases without product-specific
// ownership, visibility, or version semantics.
package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	appaccess "github.com/Zubrium/cdn/core/application/access"
	appcache "github.com/Zubrium/cdn/core/application/cache"
	"github.com/Zubrium/cdn/core/domain/access"
	"github.com/Zubrium/cdn/core/domain/files"
)

var ErrNotFound = files.ErrNotFound
var ErrForbidden = access.ErrForbidden
var ErrUnauthorized = access.ErrUnauthorized
var ErrRevisionConflict = files.ErrRevisionConflict
var ErrFileSetConflict = files.ErrFileSetConflict
var ErrRequestConflict = files.ErrRequestConflict
var ErrUploadInProgress = files.ErrUploadInProgress
var ErrRequestDeleted = files.ErrRequestDeleted
var ErrUnavailable = files.ErrUnavailable
var ErrChecksumMismatch = files.ErrChecksumMismatch
var ErrInvalidSearch = files.ErrInvalidSearch

type Object = files.Object
type MetadataReader = files.MetadataReader
type ByteReader = files.ByteReader
type Creator = files.Creator
type VerifiedCreator = files.VerifiedCreator
type MetadataWriter = files.MetadataWriter
type ContentTypeWriter = files.ContentTypeWriter
type Deleter = files.Deleter
type ByteCache = files.ByteCache

type Service struct {
	metadata             MetadataReader
	bytes                ByteReader
	creator              Creator
	updates              MetadataWriter
	deleter              Deleter
	cache                ByteCache
	cacheStore           *appcache.Store
	cacheAuthorizer      appcache.Authorizer
	concurrency          *storageConcurrency
	maxObjectBytes       int64
	maxMetadataBytes     int64
	maxCachedObjectBytes int64
	cacheTTL             time.Duration
}

func NewService(metadata MetadataReader, byteStore ByteReader, creator Creator, updates MetadataWriter, deleter Deleter, byteCache ByteCache, maxObjectBytes, maxMetadataBytes, maxCachedObjectBytes int64, cacheTTL time.Duration) Service {
	return Service{
		metadata: metadata, bytes: byteStore, creator: creator, updates: updates, deleter: deleter,
		cache: byteCache, maxObjectBytes: maxObjectBytes, maxMetadataBytes: maxMetadataBytes,
		maxCachedObjectBytes: maxCachedObjectBytes, cacheTTL: cacheTTL,
	}
}

// WithAuthorizationCache attaches Core's bounded cache to an existing storage
// service. Authorization is still performed through the injected authorizer on
// every miss and every stale hit; the cache only holds verifiable hints.
func (s Service) WithAuthorizationCache(store *appcache.Store, authorizer appcache.Authorizer) Service {
	s.cacheStore = store
	s.cacheAuthorizer = authorizer
	return s
}

func (s Service) Create(ctx context.Context, actor access.Principal, requestKey string, groups []string, metadata map[string]string, contentType string, size int64, body io.Reader) (string, error) {
	return s.create(ctx, actor, requestKey, groups, metadata, contentType, size, "", body)
}

func (s Service) CreateVerified(ctx context.Context, actor access.Principal, requestKey string, groups []string, metadata map[string]string, contentType string, size int64, expectedSHA256 string, body io.Reader) (string, error) {
	return s.create(ctx, actor, requestKey, groups, metadata, contentType, size, expectedSHA256, body)
}

func (s Service) create(ctx context.Context, actor access.Principal, requestKey string, groups []string, metadata map[string]string, contentType string, size int64, expectedSHA256 string, body io.Reader) (string, error) {
	if s.creator == nil || s.metadata == nil {
		return "", ErrUnavailable
	}
	if !actor.Active || actor.ID == "" {
		return "", ErrUnauthorized
	}
	if size < 0 || s.maxObjectBytes <= 0 || size > s.maxObjectBytes {
		return "", errors.New("file size exceeds configured limit")
	}
	if body == nil || !ValidContentType(contentType) || !ValidMetadata(metadata, s.maxMetadataBytes) || !ValidGroups(groups) || !ValidUUID(requestKey) || (expectedSHA256 != "" && !ValidSHA256(expectedSHA256)) {
		return "", errors.New("invalid file upload")
	}
	current, err := s.metadata.CurrentPrincipal(ctx, actor.ID)
	if err != nil {
		return "", err
	}
	assignments, err := s.metadata.Assignments(ctx, actor.ID)
	if err != nil {
		return "", err
	}
	for _, group := range groups {
		if !appaccess.AuthorizeGroup(current, access.Upload, group, assignments) {
			return "", ErrForbidden
		}
	}
	if expectedSHA256 == "" {
		return s.creator.Create(ctx, current, requestKey, groups, cloneMetadata(metadata), contentType, size, body)
	}
	verifiedCreator, ok := s.creator.(VerifiedCreator)
	if !ok {
		return "", ErrUnavailable
	}
	return verifiedCreator.CreateVerified(ctx, current, requestKey, groups, cloneMetadata(metadata), contentType, size, expectedSHA256, body)
}

func (s Service) Read(ctx context.Context, actor access.Principal, id string) (io.ReadCloser, Object, error) {
	if s.bytes == nil || s.metadata == nil {
		return nil, Object{}, ErrUnavailable
	}
	release, err := s.acquire(ctx, false)
	if err != nil {
		return nil, Object{}, err
	}
	defer release()
	object, err := s.Stat(ctx, actor, id)
	if err != nil {
		return nil, Object{}, err
	}
	if object.Size <= s.maxCachedObjectBytes && s.cacheStore != nil {
		if view, ok := s.cacheStore.GetBytes(ctx, object.ID); ok {
			return io.NopCloser(bytes.NewReader(view.Bytes())), object, nil
		}
		reader, err := s.bytes.Read(ctx, object.Key)
		if err != nil {
			return nil, Object{}, err
		}
		body, readErr := io.ReadAll(io.LimitReader(reader, object.Size+1))
		closeErr := reader.Close()
		if readErr != nil {
			return nil, Object{}, fmt.Errorf("read file bytes: %w", readErr)
		}
		if closeErr != nil {
			return nil, Object{}, fmt.Errorf("close file stream: %w", closeErr)
		}
		if int64(len(body)) != object.Size {
			return nil, Object{}, errors.New("stored file size does not match metadata")
		}
		if err := s.cacheStore.PutBytes(ctx, object.ID, object.Revision, body); err != nil {
			return nil, Object{}, err
		}
		return io.NopCloser(bytes.NewReader(body)), object, nil
	}
	if object.Size <= s.maxCachedObjectBytes && s.cache != nil {
		if body, ok := s.cache.Get(object.ID); ok {
			return io.NopCloser(bytes.NewReader(body)), object, nil
		}
		reader, err := s.bytes.Read(ctx, object.Key)
		if err != nil {
			return nil, Object{}, err
		}
		body, readErr := io.ReadAll(io.LimitReader(reader, object.Size+1))
		closeErr := reader.Close()
		if readErr != nil {
			return nil, Object{}, fmt.Errorf("read file bytes: %w", readErr)
		}
		if closeErr != nil {
			return nil, Object{}, fmt.Errorf("close file stream: %w", closeErr)
		}
		if int64(len(body)) != object.Size {
			return nil, Object{}, errors.New("stored file size does not match metadata")
		}
		s.cache.Put(object.ID, body, s.cacheTTL)
		return io.NopCloser(bytes.NewReader(body)), object, nil
	}
	reader, err := s.bytes.Read(ctx, object.Key)
	if err != nil {
		return nil, Object{}, err
	}
	return reader, object, nil
}

func (s Service) ReadRange(ctx context.Context, actor access.Principal, id string, start, end int64) (io.ReadCloser, Object, error) {
	if s.bytes == nil || s.metadata == nil {
		return nil, Object{}, ErrUnavailable
	}
	release, err := s.acquire(ctx, false)
	if err != nil {
		return nil, Object{}, err
	}
	defer release()
	object, err := s.Stat(ctx, actor, id)
	if err != nil {
		return nil, Object{}, err
	}
	if start < 0 || end < start || end >= object.Size {
		return nil, Object{}, errors.New("invalid file range")
	}
	if object.Size <= s.maxCachedObjectBytes && s.cacheStore != nil {
		if view, ok := s.cacheStore.GetBytes(ctx, object.ID); ok {
			return io.NopCloser(bytes.NewReader(view.Bytes()[start : end+1])), object, nil
		}
	}
	if object.Size <= s.maxCachedObjectBytes && s.cache != nil {
		if body, ok := s.cache.Get(object.ID); ok {
			return io.NopCloser(bytes.NewReader(body[start : end+1])), object, nil
		}
	}
	reader, err := s.bytes.ReadRange(ctx, object.Key, start, end)
	if err != nil {
		return nil, Object{}, err
	}
	return reader, object, nil
}

func (s Service) Stat(ctx context.Context, actor access.Principal, id string) (Object, error) {
	return s.authorizedObject(ctx, actor, id, access.Read)
}

func (s Service) Update(ctx context.Context, actor access.Principal, id string, expected int64, metadata map[string]string) error {
	return s.UpdateWithContentType(ctx, actor, id, expected, metadata, "")
}

func (s Service) UpdateWithContentType(ctx context.Context, actor access.Principal, id string, expected int64, metadata map[string]string, contentType string) error {
	object, err := s.authorizeWrite(ctx, actor, id)
	if err != nil {
		return err
	}
	if s.updates == nil || !ValidMetadata(metadata, s.maxMetadataBytes) {
		return errors.New("invalid file metadata update")
	}
	if contentType != "" && !ValidContentType(contentType) {
		return errors.New("invalid file content type update")
	}
	if contentType != "" {
		writer, ok := s.updates.(ContentTypeWriter)
		if !ok {
			return ErrUnavailable
		}
		if err := writer.UpdateWithContentType(ctx, actor, id, expected, cloneMetadata(metadata), contentType); err != nil {
			return err
		}
	} else if err := s.updates.Update(ctx, actor, id, expected, cloneMetadata(metadata)); err != nil {
		return err
	}
	if s.cache != nil {
		s.invalidate(object.ID)
	}
	return nil
}

func (s Service) AddToGroup(ctx context.Context, actor access.Principal, id, group string, expected int64) error {
	object, err := s.authorizeWrite(ctx, actor, id)
	if err != nil {
		return err
	}
	assignments, err := s.metadata.Assignments(ctx, actor.ID)
	if err != nil {
		return err
	}
	if !appaccess.AuthorizeGroup(actor, access.Write, group, assignments) {
		return ErrForbidden
	}
	if s.updates == nil {
		return errors.New("file group update is unavailable")
	}
	if err := s.updates.AddToGroup(ctx, actor, id, group, expected); err != nil {
		return err
	}
	if s.cache != nil {
		s.invalidate(object.ID)
	}
	return nil
}

func (s Service) RemoveFromGroup(ctx context.Context, actor access.Principal, id, group string, expected int64) error {
	object, err := s.authorizeWrite(ctx, actor, id)
	if err != nil {
		return err
	}
	assignments, err := s.metadata.Assignments(ctx, actor.ID)
	if err != nil {
		return err
	}
	if !appaccess.AuthorizeGroup(actor, access.Write, group, assignments) {
		return ErrForbidden
	}
	if s.updates == nil {
		return errors.New("file group update is unavailable")
	}
	if err := s.updates.RemoveFromGroup(ctx, actor, id, group, expected); err != nil {
		return err
	}
	if s.cache != nil {
		s.invalidate(object.ID)
	}
	return nil
}

func (s Service) Delete(ctx context.Context, actor access.Principal, id string, expected int64) error {
	object, err := s.authorizeWrite(ctx, actor, id)
	if err != nil {
		return err
	}
	if s.deleter == nil {
		return errors.New("file deletion is unavailable")
	}
	if err := s.deleter.Delete(ctx, actor, id, expected); err != nil {
		return err
	}
	if s.cache != nil {
		s.invalidate(object.ID)
	}
	return nil
}

func (s Service) authorizeWrite(ctx context.Context, actor access.Principal, id string) (Object, error) {
	return s.authorizedObject(ctx, actor, id, access.Write)
}

// authorizedObject checks current access and revision in PostgreSQL before it
// consults metadata cache. Group changes advance the revision in the same
// transaction, so cached metadata can never outlive the authorization snapshot.
func (s Service) authorizedObject(ctx context.Context, actor access.Principal, id string, action access.Action) (Object, error) {
	if s.metadata == nil {
		return Object{}, ErrUnavailable
	}
	if !actor.Active || actor.ID == "" {
		return Object{}, ErrUnauthorized
	}
	if !appaccess.ValidAction(action) {
		return Object{}, errors.New("invalid file action")
	}
	release, err := s.acquire(ctx, true)
	if err != nil {
		return Object{}, err
	}
	defer release()
	for attempt := 0; attempt < 3; attempt++ {
		if s.cacheStore != nil && s.cacheAuthorizer != nil {
			object, err := s.authorizedObjectCached(ctx, actor.ID, id, action)
			if err != nil {
				return Object{}, err
			}
			return object, nil
		}
		revision, visibleGroups, err := s.metadata.Authorize(ctx, actor.ID, id, action)
		if err != nil {
			return Object{}, err
		}
		cacheKey := metadataCacheKey(id)
		if s.cache != nil {
			if encoded, ok := s.cache.Get(cacheKey); ok {
				var object Object
				if json.Unmarshal(encoded, &object) == nil && object.ID == id && object.Revision == revision && object.Ready {
					object.Groups = filterGroups(object.Groups, visibleGroups)
					return object, nil
				}
				s.cache.Delete(cacheKey)
			}
		}
		object, err := s.metadata.Find(ctx, id)
		if err != nil {
			return Object{}, err
		}
		if object.Revision != revision {
			continue
		}
		if !object.Ready || object.ID != id {
			return Object{}, ErrNotFound
		}
		if s.cache != nil {
			if encoded, err := json.Marshal(object); err == nil {
				s.cache.Put(cacheKey, encoded, s.cacheTTL)
			}
		}
		object.Groups = filterGroups(object.Groups, visibleGroups)
		return object, nil
	}
	return Object{}, errors.New("file metadata changed repeatedly during request")
}

// authorizedObjectCached uses the application-owned authorization and metadata
// caches. A miss still performs the full authoritative check, and a cached hit
// is accepted only while both revision and access_epoch still match.
func (s Service) authorizedObjectCached(ctx context.Context, principalID, id string, action access.Action) (Object, error) {
	auth, err := s.cacheStore.Authorize(ctx, principalID, id, string(action), s.cacheAuthorizer)
	if err != nil {
		return Object{}, err
	}
	if cached, ok := s.cacheStore.GetMetadata(ctx, id); ok {
		object := objectFromCachedMetadata(cached)
		object.Groups = filterGroups(object.Groups, auth.Groups)
		return object, nil
	}
	object, err := s.metadata.Find(ctx, id)
	if err != nil {
		return Object{}, err
	}
	if object.Revision != auth.Revision {
		s.cacheStore.Notify(id)
		return Object{}, errors.New("file metadata changed during request")
	}
	if !object.Ready || object.ID != id {
		return Object{}, ErrNotFound
	}
	cached := appcache.Metadata{
		FileID: object.ID, Key: object.Key, Revision: object.Revision, Size: int(object.Size),
		Ready: object.Ready, Groups: object.Groups, Metadata: cloneMetadata(object.Metadata),
		SHA256: object.SHA256, ContentType: object.ContentType, State: object.State,
	}
	if err := s.cacheStore.PutMetadata(ctx, cached); err != nil {
		return Object{}, err
	}
	object.Groups = filterGroups(object.Groups, auth.Groups)
	return object, nil
}

func objectFromCachedMetadata(cached appcache.Metadata) Object {
	return Object{
		ID: cached.FileID, Key: cached.Key, Groups: append([]string(nil), cached.Groups...),
		Metadata: cloneMetadata(cached.Metadata), SHA256: cached.SHA256, Revision: cached.Revision,
		Size: int64(cached.Size), ContentType: cached.ContentType, Ready: cached.Ready, State: cached.State,
	}
}

func filterGroups(groups, visible []string) []string {
	if len(groups) == 0 || len(visible) == 0 {
		return []string{}
	}
	allowed := make(map[string]struct{}, len(visible))
	for _, group := range visible {
		allowed[group] = struct{}{}
	}
	filtered := make([]string, 0, len(groups))
	for _, group := range groups {
		if _, ok := allowed[group]; ok {
			filtered = append(filtered, group)
		}
	}
	return filtered
}

func (s Service) invalidate(id string) {
	if s.cacheStore != nil {
		s.cacheStore.Notify(id)
	}
	if s.cache != nil {
		s.cache.Delete(id)
		s.cache.Delete(metadataCacheKey(id))
	}
}

// InvalidateCache releases all locally cached state for a database object.
func (s Service) InvalidateCache(id string) {
	if id == "" {
		return
	}
	if s.cacheStore != nil || s.cache != nil {
		s.invalidate(id)
	}
}

// InvalidateAll clears local caches after a LISTEN reconnect.
func (s Service) InvalidateAll() {
	if s.cacheStore != nil {
		s.cacheStore.OnReconnect()
	}
	if clearer, ok := s.cache.(interface{ Clear() }); ok {
		clearer.Clear()
	}
}

func metadataCacheKey(id string) string { return "core-metadata:" + id }

func cloneMetadata(source map[string]string) map[string]string {
	copy := make(map[string]string, len(source))
	for key, value := range source {
		copy[key] = value
	}
	return copy
}
