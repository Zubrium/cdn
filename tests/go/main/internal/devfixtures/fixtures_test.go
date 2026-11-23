//go:build devfixtures

package devfixtures

import (
	"context"
	"errors"
	"testing"

	core "github.com/Zubrium/cdn/core/presentation"
	content "github.com/Zubrium/cdn/engines/content/presentation"
	forms "github.com/Zubrium/cdn/engines/forms/presentation"
	website "github.com/Zubrium/cdn/engines/website/presentation"
)

type fakeCore struct{ access map[string]core.AccessLevel }

func (f fakeCore) Identity() core.IdentityCapability { return fakeCore{access: f.access} }
func (f fakeCore) Sessions() core.SessionCapability  { return fakeCore{access: f.access} }
func (f fakeCore) GroupAuthorization() core.GroupAuthorizationCapability {
	return f
}
func (fakeCore) Authenticate(context.Context, string) (core.Session, core.Principal, error) {
	return core.Session{}, core.Principal{ID: "principal"}, nil
}
func (fakeCore) CreateSession(context.Context, string) (core.CreatedSession, error) {
	return core.CreatedSession{Token: "ss_fixture"}, nil
}
func (fakeCore) AuthenticateSession(context.Context, string) (core.Session, core.Principal, error) {
	return core.Session{}, core.Principal{ID: "principal"}, nil
}
func (fakeCore) RevokeSession(context.Context, string) error { return nil }
func (f fakeCore) RequireGroupLevel(_ context.Context, _ core.Session, groups []string, level core.AccessLevel) error {
	for _, group := range groups {
		if f.access[group] < level {
			return core.ErrForbidden
		}
	}
	return nil
}

func configuredGroups() map[string]core.AccessLevel {
	return map[string]core.AccessLevel{
		"features": core.AccessLevelRead, "content": core.AccessLevelRead,
		forms.SchemaAccessGroup: core.AccessLevelRead, forms.SubmissionAccessGroup: core.AccessLevelRead,
	}
}

func TestFixtureReadsRequireCurrentDirectGroupAccess(t *testing.T) {
	authority := fakeCore{access: configuredGroups()}
	fixtures, err := New(authority, Config{ConfiguredGroups: configuredGroups()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := fixtures.Website.Authenticate(context.Background(), "valid")
	if err != nil {
		t.Fatal(err)
	}
	page, err := fixtures.Website.ListSnapshots(ctx, syntheticPageID, "", 20)
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != syntheticSnapshotID || page.Items[0].PageID != syntheticPageID {
		t.Fatalf("read fixture page snapshots: page=%+v err=%v", page, err)
	}
	model, err := fixtures.Website.GetContentModel(ctx)
	if err != nil || !model.VerifyDigest() {
		t.Fatalf("read fixture content model: model=%+v err=%v", model, err)
	}
	pageProjection, err := fixtures.Website.GetPagePlacementProjection(ctx, syntheticPageID, syntheticSnapshotID)
	if err != nil || pageProjection.PageID != syntheticPageID || !pageProjection.VerifyDigest() {
		t.Fatalf("read fixture page placement projection: projection=%+v err=%v", pageProjection, err)
	}
	projections := content.Projections{
		ContentModel: content.ContentModelProjection{Version: model.Version, ModelRevision: model.ModelRevision, Digest: model.Digest, DataModel: model.DataModel},
		Page: content.PagePlacementProjection{Version: pageProjection.Version, PageSnapshotID: pageProjection.PageSnapshotID, PageID: pageProjection.PageID,
			Revision: pageProjection.Revision, State: pageProjection.State, Placements: []content.Placement{}},
	}
	projections.Page.Digest, err = projections.Page.ComputeDigest()
	if err != nil {
		t.Fatalf("compute fixture Content page projection digest: %v", err)
	}
	workspace, err := fixtures.Content.GetWorkspace(ctx, projections)
	if err != nil || workspace.PageSnapshotID != syntheticSnapshotID || workspace.ModelDigest != model.Digest {
		t.Fatalf("read fixture content workspace: workspace=%+v err=%v", workspace, err)
	}

	denied, err := New(fakeCore{access: map[string]core.AccessLevel{}}, Config{ConfiguredGroups: configuredGroups()})
	if err != nil {
		t.Fatal(err)
	}
	deniedCtx, err := denied.Forms.Authenticate(context.Background(), "valid")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := denied.Forms.ListSchemas(deniedCtx, "", 20); !errors.Is(err, forms.ErrForbidden) {
		t.Fatalf("missing group access was not denied: %v", err)
	}
}

func TestFixtureWritesNeverClaimPersistence(t *testing.T) {
	groups := configuredGroups()
	groups["features"] = core.AccessLevelWrite
	groups["content"] = core.AccessLevelWrite
	groups[forms.SchemaAccessGroup] = core.AccessLevelWrite
	groups[forms.SubmissionAccessGroup] = core.AccessLevelWrite
	fixtures, err := New(fakeCore{access: groups}, Config{ConfiguredGroups: groups})
	if err != nil {
		t.Fatal(err)
	}
	websiteContext, err := fixtures.Website.AuthenticateSession(context.Background(), "session")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixtures.Website.SaveContentModel(websiteContext, website.SaveContentModelRequest{ExpectedRevision: 1, DataModel: []byte(`{"synthetic":"request"}`)}); !errors.Is(err, website.ErrUnavailable) {
		t.Fatalf("Website fixture reported a successful model write: %v", err)
	}
	if _, err := fixtures.Website.CreateSnapshot(websiteContext, website.CreateSnapshotRequest{PageID: syntheticPageID, Name: "request body"}); !errors.Is(err, website.ErrUnavailable) {
		t.Fatalf("Website fixture reported a successful write: %v", err)
	}
	contentContext, err := fixtures.Content.Authenticate(context.Background(), "valid")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixtures.Content.SaveWorkspaceDraft(contentContext, content.Projections{}, content.SaveWorkspaceDraftRequest{Records: []content.RecordInput{{ClientKey: "request", EntityID: "request-entity", Values: map[string]any{"title": "not persisted"}}}}); !errors.Is(err, content.ErrUnavailable) {
		t.Fatalf("Content fixture reported a successful write: %v", err)
	}
	formsContext, err := fixtures.Forms.Authenticate(context.Background(), "valid")
	if err != nil {
		t.Fatal(err)
	}
	if err := fixtures.Forms.DeleteSubmission(formsContext, forms.DeleteSubmissionRequest{}); !errors.Is(err, forms.ErrUnavailable) {
		t.Fatalf("Forms fixture reported a successful write: %v", err)
	}
}

func TestFixtureConstructionRequiresConfiguredGroups(t *testing.T) {
	if _, err := New(fakeCore{access: configuredGroups()}, Config{ConfiguredGroups: map[string]core.AccessLevel{"features": core.AccessLevelRead}}); err == nil {
		t.Fatal("fixture runtime started with missing configured access groups")
	}
}
