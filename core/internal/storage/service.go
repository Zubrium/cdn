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

	"github.com/Zubrium/cdn/core/access"
)

var ErrNotFound = errors.New("file not found")
var ErrForbidden = access.ErrForbidden
var ErrUnauthorized = access.ErrUnauthorized
var ErrRevisionConflict = errors.New("file metadata revision conflict")
var ErrFileSetConflict = errors.New("file set name conflicts within its namespace")
var ErrRequestConflict = errors.New("upload request key reused with different content")
var ErrUploadInProgress = errors.New("upload request is still in progress")
var ErrRequestDeleted = errors.New("upload request refers to a deleted file")
var ErrUnavailable = errors.New("file storage is unavailable")
var ErrChecksumMismatch = errors.New("file checksum does not match expected SHA-256")
var ErrInvalidSearch = errors.New("invalid file search query")

type Object struct {
	ID          string
	Key         string
	Groups      []string
	Metadata    map[string]string
	SHA256      string
	Revision    int64
	Size        int64
	ContentType string
	Ready       bool
	State       string
}

type MetadataReader interface {
	Find(context.Context, string) (Object, error)
	Authorize(context.Context, string, string, access.Action) (int64, []string, error)
	Assignments(context.Context, string) ([]access.GroupAssignment, error)
	CurrentPrincipal(context.Context, string) (access.Principal, error)
}

type ByteReader interface {
	Read(context.Context, string) (io.ReadCloser, error)
	ReadRange(context.Context, string, int64, int64) (io.ReadCloser, error)
}

type Creator interface {
	Create(context.Context, access.Principal, string, []string, map[string]string, string, int64, io.Reader) (string, error)
}

type VerifiedCreator interface {
	CreateVerified(context.Context, access.Principal, string, []string, map[string]string, string, int64, string, io.Reader) (string, error)
}

type MetadataWriter interface {
	Update(context.Context, access.Principal, string, int64, map[string]string) error
	AddToGroup(context.Context, access.Principal, string, string, int64) error
	RemoveFromGroup(context.Context, access.Principal, string, string, int64) error
}

type ContentTypeWriter interface {
	UpdateWithContentType(context.Context, access.Principal, string, int64, map[string]string, string) error
}

type Deleter interface {
	Delete(context.Context, access.Principal, string, int64) error
}

type ByteCache interface {
	Get(string) ([]byte, bool)
	Put(string, []byte, time.Duration)
	Delete(string)
}

type Service struct {
	metadata             MetadataReader
	bytes                ByteReader
	creator              Creator
	updates              MetadataWriter
	deleter              Deleter
	cache                ByteCache
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
		if !access.AuthorizeGroup(current, access.Upload, group, assignments) {
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
	object, err := s.Stat(ctx, actor, id)
	if err != nil {
		return nil, Object{}, err
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
	object, err := s.Stat(ctx, actor, id)
	if err != nil {
		return nil, Object{}, err
	}
	if start < 0 || end < start || end >= object.Size {
		return nil, Object{}, errors.New("invalid file range")
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
	if !access.AuthorizeGroup(actor, access.Write, group, assignments) {
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
	if !access.AuthorizeGroup(actor, access.Write, group, assignments) {
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
	if !access.ValidAction(action) {
		return Object{}, errors.New("invalid file action")
	}
	for attempt := 0; attempt < 3; attempt++ {
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
	s.cache.Delete(id)
	s.cache.Delete(metadataCacheKey(id))
}

// InvalidateCache releases all locally cached state for a database object.
func (s Service) InvalidateCache(id string) {
	if s.cache != nil && id != "" {
		s.invalidate(id)
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
