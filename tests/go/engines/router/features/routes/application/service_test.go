package application

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/Zubrium/cdn/engines/router/features/routes/model"
	"github.com/Zubrium/cdn/engines/router/features/routes/repository"
	"github.com/Zubrium/cdn/pkg/contracts"
)

type fakeIdentity struct{ err error }

func (identity fakeIdentity) Authenticate(context.Context, string) (contracts.Principal, error) {
	return contracts.Principal{ID: "principal", Active: true}, identity.err
}
func (identity fakeIdentity) AuthenticateSession(context.Context, string) (contracts.Principal, error) {
	return contracts.Principal{ID: "principal", Active: true}, identity.err
}

type fakeAuthorizer struct {
	maximum  contracts.AccessLevel
	levels   []contracts.AccessLevel
	subjects []contracts.AuthorizationSubject
}

func (authorizer *fakeAuthorizer) RequireGroupLevel(_ context.Context, subject contracts.AuthorizationSubject, groups []string, required contracts.AccessLevel) error {
	authorizer.levels = append(authorizer.levels, required)
	authorizer.subjects = append(authorizer.subjects, subject)
	if len(groups) != 1 || groups[0] != "router_routes" || authorizer.maximum < required {
		return contracts.ErrAccessForbidden
	}
	return nil
}

func TestSessionTokenIsRetainedOnlyAsDataBusAuthorizationSubject(t *testing.T) {
	repo := newFakeRouteRepository()
	authorizer := &fakeAuthorizer{maximum: contracts.AccessLevelRead}
	service, err := New(repo, fakeIdentity{}, authorizer, fakeTargetValidator{}, Config{
		AccessGroup: "router_routes", DefaultPageSize: 20, MaxPageSize: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := service.AuthenticateSession(context.Background(), "opaque-token")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.ListRoutes(ctx, "", "", 0); err != nil {
		t.Fatal(err)
	}
	want := contracts.AuthorizationSubject{SessionToken: "opaque-token"}
	if len(authorizer.subjects) != 1 || authorizer.subjects[0] != want {
		t.Fatalf("authorization subject = %#v, want %#v", authorizer.subjects, want)
	}
}

type fakeTargetValidator struct{ err error }

func (validator fakeTargetValidator) ValidateRouteTarget(_ context.Context, target model.Target, _ string) error {
	if validator.err != nil {
		return validator.err
	}
	return target.Validate()
}

type fakeRouteRepository struct {
	items       map[string]model.Route
	idempotency map[string]struct{ fingerprint, routeID string }
}

func newFakeRouteRepository() *fakeRouteRepository {
	return &fakeRouteRepository{items: make(map[string]model.Route), idempotency: make(map[string]struct{ fingerprint, routeID string })}
}

func (repository *fakeRouteRepository) Create(_ context.Context, route model.Route, key, fingerprint string) (model.Route, error) {
	if prior, ok := repository.idempotency[key]; ok {
		if prior.fingerprint != fingerprint {
			return model.Route{}, repository.ErrConflict()
		}
		saved, exists := repository.items[prior.routeID]
		if !exists {
			return model.Route{}, repository.ErrNotFound()
		}
		return saved, nil
	}
	for _, current := range repository.items {
		if current.Host == route.Host && current.Path == route.Path {
			return model.Route{}, repository.ErrConflict()
		}
	}
	repository.items[route.ID] = route
	repository.idempotency[key] = struct{ fingerprint, routeID string }{fingerprint, route.ID}
	return route, nil
}
func (repo *fakeRouteRepository) GetByID(_ context.Context, id string) (model.Route, error) {
	route, ok := repo.items[id]
	if !ok {
		return model.Route{}, repository.ErrNotFound
	}
	return route, nil
}
func (repo *fakeRouteRepository) List(_ context.Context, host, cursor string, limit int) ([]model.Route, string, error) {
	ids := make([]string, 0, len(repo.items))
	for id, route := range repo.items {
		if (host == "" || route.Host == host) && id > cursor {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	var next string
	if len(ids) > limit {
		next = ids[limit-1]
		ids = ids[:limit]
	}
	items := make([]model.Route, 0, len(ids))
	for _, id := range ids {
		items = append(items, repo.items[id])
	}
	return items, next, nil
}
func (repo *fakeRouteRepository) FindMatch(_ context.Context, host, path string) (model.Route, error) {
	var best model.Route
	for _, route := range repo.items {
		if route.Host != host || !model.PathMatches(route.Path, path, route.Match) {
			continue
		}
		if best.ID == "" || route.Match == model.MatchExact || (best.Match != model.MatchExact && len(route.Path) > len(best.Path)) {
			best = route
		}
	}
	if best.ID == "" {
		return model.Route{}, repository.ErrNotFound
	}
	return best, nil
}
func (repo *fakeRouteRepository) ReplaceTarget(_ context.Context, id string, target model.Target, revision int64, now time.Time) (model.Route, error) {
	route, err := repo.GetByID(context.Background(), id)
	if err != nil {
		return model.Route{}, err
	}
	if route.Revision != revision {
		return model.Route{}, repository.ErrConflict
	}
	route.Target, route.Revision, route.UpdatedAt = target, revision+1, now
	repo.items[id] = route
	return route, nil
}
func (repo *fakeRouteRepository) ReplaceLocaleTarget(_ context.Context, id string, update repository.LocaleTargetUpdate, now time.Time) (model.Route, error) {
	route, err := repo.GetByID(context.Background(), id)
	if err != nil {
		return model.Route{}, err
	}
	index := -1
	for position, item := range route.Target.LocaleTargets {
		if item.Locale == update.Locale {
			index = position
			break
		}
	}
	if index < 0 || route.Target.LocaleTargets[index].Revision != update.ExpectedRevision {
		return model.Route{}, repository.ErrConflict
	}
	route.Target.LocaleTargets[index].ArtifactManifestFileID = update.ArtifactManifestFileID
	route.Target.LocaleTargets[index].Revision++
	route.UpdatedAt = now
	repo.items[id] = route
	return route, nil
}

func (repo *fakeRouteRepository) Delete(_ context.Context, id string, revision int64) error {
	route, err := repo.GetByID(context.Background(), id)
	if err != nil {
		return err
	}
	if route.Revision != revision {
		return repository.ErrConflict
	}
	delete(repo.items, id)
	return nil
}
func (*fakeRouteRepository) ErrConflict() error { return repository.ErrConflict }
func (*fakeRouteRepository) ErrNotFound() error { return repository.ErrNotFound }

func newTestService(t *testing.T, level contracts.AccessLevel, targetValidator TargetValidator) (*Service, *fakeRouteRepository, *fakeAuthorizer, context.Context) {
	t.Helper()
	repo := newFakeRouteRepository()
	authorizer := &fakeAuthorizer{maximum: level}
	service, err := New(repo, fakeIdentity{}, authorizer, targetValidator, Config{
		AccessGroup: "router_routes", DefaultPageSize: 20, MaxPageSize: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := service.Authenticate(context.Background(), "user-credential")
	if err != nil {
		t.Fatal(err)
	}
	return service, repo, authorizer, ctx
}

func uploadTarget() model.Target {
	return model.Target{Kind: model.TargetRegisteredOperation, OperationID: "public.form-upload", OperationConfig: []byte(`{"enabled":true}`)}
}

func TestGroupLevelOneReadsButCannotChangeRoutes(t *testing.T) {
	service, _, authorizer, ctx := newTestService(t, contracts.AccessLevelRead, fakeTargetValidator{})
	if _, _, err := service.ListRoutes(ctx, "", "", 0); err != nil {
		t.Fatalf("read at level 1 failed: %v", err)
	}
	_, err := service.CreateRoute(ctx, CreateRouteInput{Host: "example.com", Path: "/upload", Match: model.MatchExact, Target: uploadTarget(), IdempotencyKey: "router-route-create-001"})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("write at level 1 = %v, want forbidden", err)
	}
	if len(authorizer.levels) != 2 || authorizer.levels[0] != contracts.AccessLevelRead || authorizer.levels[1] != contracts.AccessLevelWrite {
		t.Fatalf("group access checks = %v", authorizer.levels)
	}
	if len(authorizer.subjects) != 2 || authorizer.subjects[0].Credential != "user-credential" || authorizer.subjects[1].Credential != "user-credential" {
		t.Fatalf("credential authorization subjects = %#v", authorizer.subjects)
	}
}

func TestCreateNormalizesRouteAndIdempotentlyReplaysRequest(t *testing.T) {
	service, repo, _, ctx := newTestService(t, contracts.AccessLevelWrite, fakeTargetValidator{})
	input := CreateRouteInput{Host: "EXAMPLE.COM.", Path: "/uploads/a%20b", Match: model.MatchExact,
		Target: uploadTarget(), IdempotencyKey: "router-route-create-002"}
	first, err := service.CreateRoute(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if first.Host != "example.com" || first.Path != "/uploads/a%20b" || first.Revision != 1 {
		t.Fatalf("route was not normalized: %+v", first)
	}
	replayed, err := service.CreateRoute(ctx, input)
	if err != nil || replayed.ID != first.ID || len(repo.items) != 1 {
		t.Fatalf("idempotent replay = %+v, %v, stored=%d", replayed, err, len(repo.items))
	}
	changed := input
	changed.Path = "/different"
	if _, err := service.CreateRoute(ctx, changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("key reused for another route = %v, want conflict", err)
	}
}

func TestCreateRejectsTraversalAndUnavailableTarget(t *testing.T) {
	service, repo, _, ctx := newTestService(t, contracts.AccessLevelWrite, fakeTargetValidator{})
	base := CreateRouteInput{Host: "example.com", Path: "/safe", Match: model.MatchExact, Target: uploadTarget(), IdempotencyKey: "router-route-create-003"}
	for name, input := range map[string]CreateRouteInput{
		"traversal":             func() CreateRouteInput { value := base; value.Path = "/assets/%2e%2e/private"; return value }(),
		"short idempotency key": func() CreateRouteInput { value := base; value.IdempotencyKey = "short"; return value }(),
	} {
		if _, err := service.CreateRoute(ctx, input); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s input = %v, want invalid", name, err)
		}
	}
	blocked, _, _, blockedCtx := newTestService(t, contracts.AccessLevelWrite, fakeTargetValidator{err: contracts.ErrAccessForbidden})
	if _, err := blocked.CreateRoute(blockedCtx, base); !errors.Is(err, ErrForbidden) {
		t.Fatalf("target permission failure = %v, want forbidden", err)
	}
	if len(repo.items) != 0 {
		t.Fatalf("invalid requests created %d routes", len(repo.items))
	}
}

func TestReplaceAndDeleteRequireCurrentRevision(t *testing.T) {
	service, _, _, ctx := newTestService(t, contracts.AccessLevelWrite, fakeTargetValidator{})
	created, err := service.CreateRoute(ctx, CreateRouteInput{Host: "example.com", Path: "/upload", Match: model.MatchExact,
		Target: uploadTarget(), IdempotencyKey: "router-route-create-004"})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := service.ReplaceTarget(ctx, ReplaceTargetInput{RouteID: created.ID, Target: created.Target, ExpectedRevision: 1})
	if err != nil || updated.Revision != 2 {
		t.Fatalf("replace target = %+v, %v", updated, err)
	}
	if _, err := service.ReplaceTarget(ctx, ReplaceTargetInput{RouteID: created.ID, Target: created.Target, ExpectedRevision: 1}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale replacement = %v, want conflict", err)
	}
	if err := service.DeleteRoute(ctx, DeleteRouteInput{RouteID: created.ID, ExpectedRevision: 1}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale delete = %v, want conflict", err)
	}
	if err := service.DeleteRoute(ctx, DeleteRouteInput{RouteID: created.ID, ExpectedRevision: 2}); err != nil {
		t.Fatal(err)
	}
}

func TestPublicResolutionNormalizesRequestAndSeparatesHosts(t *testing.T) {
	service, _, _, ctx := newTestService(t, contracts.AccessLevelWrite, fakeTargetValidator{})
	baseTarget, err := model.NewStaticArtifactTarget("en-US", []model.LocaleArtifactTarget{
		{Locale: "en-US", ArtifactManifestFileID: "01938f7a-68af-7c10-a51d-78b6ca125301", Revision: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	base, err := service.CreateRoute(ctx, CreateRouteInput{Host: "example.com", Path: "/assets", Match: model.MatchPrefix,
		Target: baseTarget, IdempotencyKey: "router-route-resolve-001"})
	if err != nil {
		t.Fatal(err)
	}
	deep, err := service.CreateRoute(ctx, CreateRouteInput{Host: "example.com", Path: "/assets/icons/", Match: model.MatchPrefix,
		Target: baseTarget, IdempotencyKey: "router-route-resolve-002"})
	if err != nil {
		t.Fatal(err)
	}
	fileTarget := model.Target{Kind: model.TargetCoreFile, FileID: "01938f7a-68af-7c10-a51d-78b6ca125399"}
	exact, err := service.CreateRoute(ctx, CreateRouteInput{Host: "example.com", Path: "/assets/icons/logo.svg", Match: model.MatchExact,
		Target: fileTarget, IdempotencyKey: "router-route-resolve-003"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := service.CreateRoute(ctx, CreateRouteInput{Host: "other.example", Path: "/assets/icons/logo.svg", Match: model.MatchExact,
		Target: fileTarget, IdempotencyKey: "router-route-resolve-004"})
	if err != nil {
		t.Fatal(err)
	}
	if matched, err := service.ResolveRoute(context.Background(), "other.example", "/assets/icons/logo.svg"); err != nil || matched.ID != other.ID {
		t.Fatalf("other host mapping = %+v, %v; want %q", matched, err, other.ID)
	}
	got, err := service.ResolveRoute(context.Background(), "EXAMPLE.COM:443", "/assets/icons/logo.svg")
	if err != nil || got.ID != exact.ID {
		t.Fatalf("exact public mapping = %+v, %v; want %q", got, err, exact.ID)
	}
	got, err = service.ResolveRoute(context.Background(), "example.com", "/assets/icons/other.svg")
	if err != nil || got.ID != deep.ID {
		t.Fatalf("longest public prefix = %+v, %v; want %q", got, err, deep.ID)
	}
	got, err = service.ResolveRoute(context.Background(), "example.com", "/assets/other.svg")
	if err != nil || got.ID != base.ID {
		t.Fatalf("base public prefix = %+v, %v; want %q", got, err, base.ID)
	}
	if _, err := service.ResolveRoute(context.Background(), "example.com", "/assets-old/other.svg"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("partial-segment path = %v, want not found", err)
	}
	if _, err := service.ResolveRoute(context.Background(), "example.com", "/invalid//path"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ambiguous path = %v, want not found", err)
	}
}
