package storage

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Zubrium/cdn/core/access"
	"github.com/Zubrium/cdn/core/cache"
)

type fakeMetadata struct {
	object         Object
	principal      access.Principal
	assignments    []access.GroupAssignment
	findErr        error
	actorErr       error
	groupErr       error
	authorizeErr   error
	finds          int
	authorizations int
}

func (m *fakeMetadata) Find(context.Context, string) (Object, error) {
	m.finds++
	return m.object, m.findErr
}
func (m *fakeMetadata) Assignments(context.Context, string) ([]access.GroupAssignment, error) {
	return append([]access.GroupAssignment(nil), m.assignments...), m.groupErr
}
func (m *fakeMetadata) CurrentPrincipal(_ context.Context, id string) (access.Principal, error) {
	if m.actorErr != nil {
		return access.Principal{}, m.actorErr
	}
	if id != m.principal.ID || !m.principal.Active {
		return access.Principal{}, ErrUnauthorized
	}
	return m.principal, nil
}
func (m *fakeMetadata) Authorize(_ context.Context, principalID, objectID string, action access.Action) (int64, error) {
	m.authorizations++
	if m.authorizeErr != nil {
		return 0, m.authorizeErr
	}
	if m.findErr != nil {
		return 0, m.findErr
	}
	if m.groupErr != nil {
		return 0, m.groupErr
	}
	if principalID != m.principal.ID || !m.principal.Active {
		return 0, ErrUnauthorized
	}
	if objectID != m.object.ID || !m.object.Ready {
		return 0, ErrNotFound
	}
	if !access.Authorize(m.principal, action, access.Resource{ID: m.object.ID, Groups: m.object.Groups, Ready: m.object.Ready}, m.assignments) {
		return 0, ErrForbidden
	}
	return m.object.Revision, nil
}

type fakeBytes struct {
	data       string
	readErr    error
	rangeErr   error
	reads      int
	rangeReads int
}

func (b *fakeBytes) Read(context.Context, string) (io.ReadCloser, error) {
	b.reads++
	if b.readErr != nil {
		return nil, b.readErr
	}
	return io.NopCloser(strings.NewReader(b.data)), nil
}
func (b *fakeBytes) ReadRange(_ context.Context, _ string, start, end int64) (io.ReadCloser, error) {
	b.rangeReads++
	if b.rangeErr != nil {
		return nil, b.rangeErr
	}
	if start < 0 || end < start || end >= int64(len(b.data)) {
		return nil, errors.New("bad range")
	}
	return io.NopCloser(strings.NewReader(b.data[start : end+1])), nil
}

type fakeCreator struct {
	id      string
	calls   int
	request string
	groups  []string
	meta    map[string]string
	err     error
}

func (c *fakeCreator) Create(_ context.Context, _ access.Principal, key string, groups []string, metadata map[string]string, _ string, _ int64, _ io.Reader) (string, error) {
	c.calls++
	c.request = key
	c.groups = groups
	c.meta = metadata
	return c.id, c.err
}

type fakeWriter struct {
	updates int
	adds    int
	removes int
	err     error
}

func (w *fakeWriter) Update(context.Context, access.Principal, string, int64, map[string]string) error {
	w.updates++
	return w.err
}
func (w *fakeWriter) AddToGroup(context.Context, access.Principal, string, string, int64) error {
	w.adds++
	return w.err
}
func (w *fakeWriter) RemoveFromGroup(context.Context, access.Principal, string, string, int64) error {
	w.removes++
	return w.err
}

type fakeDeleter struct {
	calls int
	err   error
}

func (d *fakeDeleter) Delete(context.Context, access.Principal, string, int64) error {
	d.calls++
	return d.err
}

func testService() (Service, *fakeMetadata, *fakeBytes, *fakeCreator, *fakeWriter, *fakeDeleter, *cache.Cache) {
	metadata := &fakeMetadata{
		object:      Object{ID: "file-id", Key: "object-key", Groups: []string{"assets"}, Metadata: map[string]string{"name": "asset.txt"}, Revision: 3, Size: 3, ContentType: "text/plain", Ready: true, State: "ready"},
		principal:   access.Principal{ID: "user-id", Active: true},
		assignments: []access.GroupAssignment{{Group: "assets", Level: access.LevelWrite, Active: true}, {Group: "archive", Level: access.LevelWrite, Active: true}},
	}
	bytes := &fakeBytes{data: "abc"}
	creator := &fakeCreator{id: "new-file"}
	writer := &fakeWriter{}
	deleter := &fakeDeleter{}
	byteCache := cache.NewByteBudget(1024, nil)
	service := NewService(metadata, bytes, creator, writer, deleter, byteCache, 100, 128, 8, time.Minute)
	return service, metadata, bytes, creator, writer, deleter, byteCache
}

func TestCreateRequiresEveryConfiguredGroupAndPassesCopiedMetadata(t *testing.T) {
	service, _, _, creator, _, _, _ := testService()
	actor := access.Principal{ID: "user-id", Active: true}
	metadata := map[string]string{"name": "new.txt"}
	id, err := service.Create(context.Background(), actor, "00000000-0000-4000-8000-000000000001", []string{"assets", "archive"}, metadata, "text/plain", 1, strings.NewReader("x"))
	if err != nil || id != "new-file" || creator.calls != 1 {
		t.Fatalf("create: id=%q calls=%d err=%v", id, creator.calls, err)
	}
	if creator.request != "00000000-0000-4000-8000-000000000001" || len(creator.groups) != 2 || creator.meta["name"] != "new.txt" {
		t.Fatalf("request was not forwarded: %+v", creator)
	}
	creator.meta["name"] = "mutated"
	if metadata["name"] != "new.txt" {
		t.Fatal("repository mutation changed caller-owned metadata")
	}
	deniedMetadata := map[string]string{"name": "denied.txt"}
	service.metadata.(*fakeMetadata).assignments[1].Level = access.LevelRead
	if _, err := service.Create(context.Background(), actor, "00000000-0000-4000-8000-000000000002", []string{"assets", "archive"}, deniedMetadata, "text/plain", 1, strings.NewReader("x")); !errors.Is(err, ErrForbidden) {
		t.Fatalf("upload without level 2 in all groups: %v", err)
	}
	if creator.calls != 1 {
		t.Fatal("denied upload reached repository")
	}
}

func TestCreateRejectsUnauthorizedInvalidAndOversizedRequests(t *testing.T) {
	service, _, _, creator, _, _, _ := testService()
	cases := []struct {
		name        string
		actor       access.Principal
		key         string
		groups      []string
		metadata    map[string]string
		contentType string
		size        int64
		body        io.Reader
	}{
		{name: "inactive", actor: access.Principal{ID: "user-id"}, key: "00000000-0000-4000-8000-000000000001", groups: []string{"assets"}, metadata: map[string]string{}, contentType: "text/plain", body: strings.NewReader("")},
		{name: "invalid key", actor: access.Principal{ID: "user-id", Active: true}, key: "bad", groups: []string{"assets"}, metadata: map[string]string{}, contentType: "text/plain", body: strings.NewReader("")},
		{name: "no groups", actor: access.Principal{ID: "user-id", Active: true}, key: "00000000-0000-4000-8000-000000000002", groups: nil, metadata: map[string]string{}, contentType: "text/plain", body: strings.NewReader("")},
		{name: "oversized", actor: access.Principal{ID: "user-id", Active: true}, key: "00000000-0000-4000-8000-000000000003", groups: []string{"assets"}, metadata: map[string]string{}, contentType: "text/plain", size: 101, body: strings.NewReader(strings.Repeat("x", 101))},
		{name: "nil body", actor: access.Principal{ID: "user-id", Active: true}, key: "00000000-0000-4000-8000-000000000004", groups: []string{"assets"}, metadata: map[string]string{}, contentType: "text/plain", body: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := service.Create(context.Background(), tc.actor, tc.key, tc.groups, tc.metadata, tc.contentType, tc.size, tc.body); err == nil {
				t.Fatal("invalid request accepted")
			}
		})
	}
	if creator.calls != 0 {
		t.Fatalf("invalid requests reached repository %d times", creator.calls)
	}
}

func TestReadRechecksDatabaseBeforeCacheAndStreamsLargeObjects(t *testing.T) {
	service, metadata, bytes, _, _, _, byteCache := testService()
	actor := access.Principal{ID: "user-id", Active: true}
	body, object, err := service.Read(context.Background(), actor, "file-id")
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(body)
	_ = body.Close()
	if err != nil || string(data) != "abc" || object.ID != "file-id" || bytes.reads != 1 {
		t.Fatalf("cache miss read: bytes=%q object=%+v reads=%d err=%v", data, object, bytes.reads, err)
	}
	if _, ok := byteCache.Get("file-id"); !ok {
		t.Fatal("small object was not cached")
	}
	body, _, err = service.Read(context.Background(), actor, "file-id")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, body)
	_ = body.Close()
	if bytes.reads != 1 {
		t.Fatal("cache hit read the object store again")
	}
	if metadata.finds != 1 || metadata.authorizations != 2 {
		t.Fatalf("metadata cache missed or skipped database authorization: finds=%d authorizations=%d", metadata.finds, metadata.authorizations)
	}
	metadata.assignments[0].Level = access.LevelNone
	if _, _, err := service.Read(context.Background(), actor, "file-id"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("revoked group access was bypassed by cached metadata and bytes: %v", err)
	}
	metadata.assignments[0].Level = access.LevelWrite
	metadata.groupErr = errors.New("database unavailable")
	if _, _, err := service.Read(context.Background(), actor, "file-id"); !errors.Is(err, metadata.groupErr) {
		t.Fatalf("cached bytes bypassed a failed permission check: %v", err)
	}
	metadata.groupErr = nil
	metadata.object.Size = 10
	metadata.object.Revision++
	metadata.object.Ready = true
	bytes.data = "large-data"
	body, _, err = service.Read(context.Background(), actor, "file-id")
	if err != nil {
		t.Fatal(err)
	}
	data, err = io.ReadAll(body)
	_ = body.Close()
	if err != nil || string(data) != bytes.data || bytes.reads != 2 {
		t.Fatalf("large object was not streamed: %q %v", data, err)
	}
}

func TestReadRejectsStoredLengthMismatchAndRangeValidatesBoundaries(t *testing.T) {
	service, metadata, bytes, _, _, _, _ := testService()
	actor := access.Principal{ID: "user-id", Active: true}
	metadata.object.Size = 4
	metadata.object.Revision++
	if _, _, err := service.Read(context.Background(), actor, "file-id"); err == nil {
		t.Fatal("stored body shorter than metadata was returned")
	}
	metadata.object.Size = 3
	metadata.object.Revision++
	body, _, err := service.ReadRange(context.Background(), actor, "file-id", 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(body)
	_ = body.Close()
	if err != nil || string(data) != "bc" {
		t.Fatalf("range read: %q %v", data, err)
	}
	for _, bounds := range [][2]int64{{-1, 1}, {2, 1}, {0, 3}} {
		if _, _, err := service.ReadRange(context.Background(), actor, "file-id", bounds[0], bounds[1]); err == nil {
			t.Fatalf("accepted range %v", bounds)
		}
	}
	bytes.rangeErr = errors.New("range unavailable")
	if _, _, err := service.ReadRange(context.Background(), actor, "file-id", 0, 1); !errors.Is(err, bytes.rangeErr) {
		t.Fatalf("range error lost: %v", err)
	}
}

func TestMutationsRequireWriteAndInvalidateByteCache(t *testing.T) {
	service, metadata, _, _, writer, deleter, byteCache := testService()
	actor := access.Principal{ID: "user-id", Active: true}
	byteCache.Put("file-id", []byte("abc"), time.Minute)
	if err := service.Update(context.Background(), actor, "file-id", 3, map[string]string{"name": "renamed.txt"}); err != nil || writer.updates != 1 {
		t.Fatalf("metadata update: %d %v", writer.updates, err)
	}
	if _, ok := byteCache.Get("file-id"); ok {
		t.Fatal("metadata update did not invalidate local bytes")
	}
	byteCache.Put("file-id", []byte("abc"), time.Minute)
	if err := service.AddToGroup(context.Background(), actor, "file-id", "archive", 3); err != nil || writer.adds != 1 {
		t.Fatalf("add group: %d %v", writer.adds, err)
	}
	if _, ok := byteCache.Get("file-id"); ok {
		t.Fatal("group update did not invalidate local bytes")
	}
	if err := service.RemoveFromGroup(context.Background(), actor, "file-id", "assets", 3); err != nil || writer.removes != 1 {
		t.Fatalf("remove group: %d %v", writer.removes, err)
	}
	if err := service.Delete(context.Background(), actor, "file-id", 3); err != nil || deleter.calls != 1 {
		t.Fatalf("delete: %d %v", deleter.calls, err)
	}
	if _, ok := byteCache.Get("file-id"); ok {
		t.Fatal("delete did not invalidate local bytes")
	}
	metadata.assignments[0].Level = access.LevelRead
	if err := service.Update(context.Background(), actor, "file-id", 3, map[string]string{"name": "denied.txt"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("read access allowed update: %v", err)
	}
	if writer.updates != 1 {
		t.Fatal("denied mutation reached repository")
	}
}

func TestServiceRejectsMissingDependenciesAndHidesUnavailableObjects(t *testing.T) {
	var empty Service
	if _, _, err := empty.Read(context.Background(), access.Principal{ID: "user", Active: true}, "file"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("nil read dependencies: %v", err)
	}
	service, metadata, _, _, _, _, _ := testService()
	metadata.findErr = ErrNotFound
	if _, err := service.Stat(context.Background(), access.Principal{ID: "user-id", Active: true}, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing file: %v", err)
	}
	metadata.findErr = nil
	metadata.assignments[0].Active = false
	if _, err := service.Stat(context.Background(), access.Principal{ID: "user-id", Active: true}, "file-id"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("inactive group granted access: %v", err)
	}
}
