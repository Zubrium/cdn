package composition

import (
	"context"
	"errors"
	"testing"

	platform "github.com/Zubrium/cdn/core/presentation"
	contentapi "github.com/Zubrium/cdn/engines/content/presentation"
	"github.com/google/uuid"
)

type websiteLocaleSourceFake struct {
	config contentapi.LocaleConfig
	err    error
}

func (f websiteLocaleSourceFake) GetLocaleConfig(context.Context) (contentapi.LocaleConfig, error) {
	return f.config, f.err
}

func TestWebsiteLocaleCatalogPinsRevisionOnlyForConfiguredLocale(t *testing.T) {
	resolver := newWebsiteLocaleCatalog()
	source := websiteLocaleSourceFake{config: contentapi.LocaleConfig{Revision: 4, Locales: []string{"en-US", "ru-RU"}, DefaultLocale: "en-US"}}
	resolver.setSource(context.Background(), source)

	revision, err := resolver.LocaleCatalogRevision(context.Background(), "ru-RU")
	if err != nil || revision != 4 {
		t.Fatalf("configured locale revision = %d, %v", revision, err)
	}
	if _, err := resolver.LocaleCatalogRevision(context.Background(), "fr-FR"); err == nil {
		t.Fatal("locale outside the catalog was accepted")
	}

	empty := newWebsiteLocaleCatalog()
	if _, err := empty.LocaleCatalogRevision(context.Background(), "en-US"); err == nil {
		t.Fatal("locale catalog without a source was accepted")
	}
}

type workflowDetailFake struct {
	detail platform.WorkflowDetail
	err    error
	calls  int
}

func (f *workflowDetailFake) Get(_ context.Context, _ uuid.UUID, _ platform.WorkflowDetailFilter) (platform.WorkflowDetail, error) {
	f.calls++
	return f.detail, f.err
}

func TestWebsiteBuildWorkflowResolvesOnlyOwnedEmissionKey(t *testing.T) {
	id := uuid.MustParse("55555555-5555-4555-8555-555555555555")
	other := uuid.MustParse("44444444-4444-4444-8444-444444444444")
	details := &workflowDetailFake{detail: platform.WorkflowDetail{
		Workflow: platform.Workflow{ID: id, OwnerClassID: "engine.website"},
		TaskPage: platform.TaskPage{Items: []platform.TaskSummary{{ID: other, EmissionKey: "prerender:other"}}},
	}}
	client := &websiteBuildWorkflowClient{details: details, maxPages: 1}

	resolved, err := client.ResolveTaskID(context.Background(), id, "prerender:other")
	if err != nil || resolved != other {
		t.Fatalf("owned emission resolution = %s, %v", resolved, err)
	}
	if _, err := client.ResolveTaskID(context.Background(), id, "prerender:missing"); !errors.Is(err, platform.ErrTaskMissing) {
		t.Fatalf("missing emission error = %v", err)
	}

	foreign := &websiteBuildWorkflowClient{details: &workflowDetailFake{detail: platform.WorkflowDetail{
		Workflow: platform.Workflow{ID: id, OwnerClassID: "engine.content"},
	}}, maxPages: 1}
	if _, err := foreign.ResolveTaskID(context.Background(), id, "prerender:other"); !errors.Is(err, platform.ErrTaskMissing) {
		t.Fatalf("foreign owner emission resolution = %v", err)
	}
}
