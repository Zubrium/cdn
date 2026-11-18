package application

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	core "github.com/Zubrium/cdn/core/presentation"
	"github.com/Zubrium/cdn/engines/router/features/routes/model"
	"github.com/Zubrium/cdn/engines/router/features/routes/repository"
)

type fakeIdentity struct{ err error }

func (identity fakeIdentity) Authenticate(context.Context, string) (core.Session, error) {
	return core.Session{}, identity.err
}
func (identity fakeIdentity) AuthenticateSession(context.Context, string) (core.Session, core.Principal, error) {
	return core.Session{}, core.Principal{}, identity.err
}

type fakeAuthorizer struct {
	maximum core.AccessLevel
	levels  []core.AccessLevel
}

func (authorizer *fakeAuthorizer) RequireGroupLevel(_ context.Context, _ core.Session, groups []string, required core.AccessLevel) error {
	authorizer.levels = append(authorizer.levels, required)
	if len(groups) != 1 || groups[0] != "router_routes" || authorizer.maximum < required {
		return core.ErrForbidden
	}
	return nil
}

type fakeTargetValidator struct{ err error }

func (validator fakeTargetValidator) ValidateRouteTarget(_ context.Context, target model.Target) error {
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
		if current.BranchID == route.BranchID && current.Host == route.Host && current.Path == route.Path {
			return model.Route{}, repository.ErrConflict()
		}
	}
	repository.items[route.ID] = route
	repository.idempotency[key] = struct{ fingerprint, routeID string }{fingerprint, route.ID}
	return route, nil
}
func (repo *fakeRouteRepository) GetByID(_ context.Context, branchID, id string) (model.Route, error) {
	route, ok := repo.items[id]
	if !ok || route.BranchID != branchID {
		return model.Route{}, repository.ErrNotFound
	}
	return route, nil
}
func (repo *fakeRouteRepository) List(_ context.Context, branchID, host, cursor string, limit int) ([]model.Route, string, error) {
	ids := make([]string, 0, len(repo.items))
	for id, route := range repo.items {
		if route.BranchID == branchID && (host == "" || route.Host == host) && id > cursor {
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
func (repo *fakeRouteRepository) FindMatch(_ context.Context, branchID, host, path string) (model.Route, error) {
	var best model.Route
	for _, route := range repo.items {
		if route.BranchID != branchID || route.Host != host || !model.PathMatches(route.Path, path, route.Match) {
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
func (repo *fakeRouteRepository) ReplaceTarget(_ context.Context, branchID, id string, target model.Target, revision int64, now time.Time) (model.Route, error) {
	route, err := repo.GetByID(context.Background(), branchID, id)
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
func (repo *fakeRouteRepository) Delete(_ context.Context, branchID, id string, revision int64) error {
	route, err := repo.GetByID(context.Background(), branchID, id)
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

func newTestService(t *testing.T, level core.AccessLevel, targetValidator TargetValidator) (*Service, *fakeRouteRepository, *fakeAuthorizer, context.Context) {
	t.Helper()
	repo := newFakeRouteRepository()
	authorizer := &fakeAuthorizer{maximum: level}
	service, err := New(repo, fakeIdentity{}, authorizer, targetValidator, Config{
		BranchID: "main", AccessGroup: "router_routes", DefaultPageSize: 20, MaxPageSize: 100,
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
	service, _, authorizer, ctx := newTestService(t, core.AccessLevelRead, fakeTargetValidator{})
	if _, _, err := service.ListRoutes(ctx, "main", "", "", 0); err != nil {
		t.Fatalf("read at level 1 failed: %v", err)
	}
	_, err := service.CreateRoute(ctx, CreateRouteInput{BranchID: "main", Host: "example.com", Path: "/upload", Match: model.MatchExact, Target: uploadTarget(), IdempotencyKey: "router-route-create-001"})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("write at level 1 = %v, want forbidden", err)
	}
	if len(authorizer.levels) != 2 || authorizer.levels[0] != core.AccessLevelRead || authorizer.levels[1] != core.AccessLevelWrite {
		t.Fatalf("group access checks = %v", authorizer.levels)
	}
}

func TestCreateNormalizesRouteAndIdempotentlyReplaysRequest(t *testing.T) {
	service, repo, _, ctx := newTestService(t, core.AccessLevelWrite, fakeTargetValidator{})
	input := CreateRouteInput{BranchID: "main", Host: "EXAMPLE.COM.", Path: "/uploads/a%20b", Match: model.MatchExact,
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

func TestCreateRejectsVisitorControlledBranchTraversalAndUnavailableTarget(t *testing.T) {
	service, repo, _, ctx := newTestService(t, core.AccessLevelWrite, fakeTargetValidator{})
	base := CreateRouteInput{BranchID: "main", Host: "example.com", Path: "/safe", Match: model.MatchExact, Target: uploadTarget(), IdempotencyKey: "router-route-create-003"}
	wrongBranch := base
	wrongBranch.BranchID = "other"
	for name, input := range map[string]CreateRouteInput{
		"branch":                wrongBranch,
		"traversal":             func() CreateRouteInput { value := base; value.Path = "/assets/%2e%2e/private"; return value }(),
		"short idempotency key": func() CreateRouteInput { value := base; value.IdempotencyKey = "short"; return value }(),
	} {
		if _, err := service.CreateRoute(ctx, input); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s input = %v, want invalid", name, err)
		}
	}
	blocked, _, _, blockedCtx := newTestService(t, core.AccessLevelWrite, fakeTargetValidator{err: core.ErrForbidden})
	if _, err := blocked.CreateRoute(blockedCtx, base); !errors.Is(err, ErrForbidden) {
		t.Fatalf("target permission failure = %v, want forbidden", err)
	}
	if len(repo.items) != 0 {
		t.Fatalf("invalid requests created %d routes", len(repo.items))
	}
}

func TestReplaceAndDeleteRequireCurrentRevision(t *testing.T) {
	service, _, _, ctx := newTestService(t, core.AccessLevelWrite, fakeTargetValidator{})
	created, err := service.CreateRoute(ctx, CreateRouteInput{BranchID: "main", Host: "example.com", Path: "/upload", Match: model.MatchExact,
		Target: uploadTarget(), IdempotencyKey: "router-route-create-004"})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := service.ReplaceTarget(ctx, "main", ReplaceTargetInput{RouteID: created.ID, Target: created.Target, ExpectedRevision: 1})
	if err != nil || updated.Revision != 2 {
		t.Fatalf("replace target = %+v, %v", updated, err)
	}
	if _, err := service.ReplaceTarget(ctx, "main", ReplaceTargetInput{RouteID: created.ID, Target: created.Target, ExpectedRevision: 1}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale replacement = %v, want conflict", err)
	}
	if err := service.DeleteRoute(ctx, "main", DeleteRouteInput{RouteID: created.ID, ExpectedRevision: 1}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale delete = %v, want conflict", err)
	}
	if err := service.DeleteRoute(ctx, "main", DeleteRouteInput{RouteID: created.ID, ExpectedRevision: 2}); err != nil {
		t.Fatal(err)
	}
}

func TestPublicResolutionNormalizesRequestAndUsesConfiguredBranch(t *testing.T) {
	service, _, _, ctx := newTestService(t, core.AccessLevelWrite, fakeTargetValidator{})
	baseTarget := model.Target{Kind: model.TargetStaticSiteArtifact, FileID: "01938f7a-68af-7c10-a51d-78b6ca125301"}
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
