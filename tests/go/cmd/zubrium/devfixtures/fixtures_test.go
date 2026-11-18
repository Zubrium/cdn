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

func (f fakeCore) EngineIdentity() core.EngineIdentityCapability { return fakeCore{access: f.access} }
func (f fakeCore) Sessions() core.SessionCapability              { return fakeCore{access: f.access} }
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
	return map[string]core.AccessLevel{"features": core.AccessLevelRead, "content": core.AccessLevelRead, "forms": core.AccessLevelRead}
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
	page, err := fixtures.Website.ListSnapshots(ctx, "", "", 20)
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != syntheticSnapshotID {
		t.Fatalf("read fixture snapshot: page=%+v err=%v", page, err)
	}
	designSystem, err := fixtures.Website.GetSnapshotDesignSystem(ctx, syntheticSnapshotID)
	if err != nil || string(designSystem.FeatureTokens) != "[]" {
		t.Fatalf("read fixture feature tokens: tokens=%s err=%v", designSystem.FeatureTokens, err)
	}

	denied, err := New(fakeCore{access: map[string]core.AccessLevel{}}, Config{ConfiguredGroups: configuredGroups()})
	if err != nil {
		t.Fatal(err)
	}
	deniedCtx, err := denied.Forms.Authenticate(context.Background(), "valid")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := denied.Forms.ListSchemas(deniedCtx, syntheticSnapshotID, "", 20); !errors.Is(err, forms.ErrForbidden) {
		t.Fatalf("missing group access was not denied: %v", err)
	}
}

func TestFixtureWritesNeverClaimPersistence(t *testing.T) {
	groups := configuredGroups()
	groups["features"] = core.AccessLevelWrite
	groups["content"] = core.AccessLevelWrite
	groups["forms"] = core.AccessLevelWrite
	fixtures, err := New(fakeCore{access: groups}, Config{ConfiguredGroups: groups})
	if err != nil {
		t.Fatal(err)
	}
	websiteContext, err := fixtures.Website.AuthenticateSession(context.Background(), "session")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixtures.Website.CreateSnapshot(websiteContext, website.CreateSnapshotRequest{}); !errors.Is(err, website.ErrUnavailable) {
		t.Fatalf("Website fixture reported a successful write: %v", err)
	}
	contentContext, err := fixtures.Content.Authenticate(context.Background(), "valid")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixtures.Content.SaveWorkspaceDraft(contentContext, content.SaveWorkspaceDraftRequest{}); !errors.Is(err, content.ErrUnavailable) {
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
