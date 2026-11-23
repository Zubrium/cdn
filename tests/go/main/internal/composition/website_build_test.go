package composition

import (
	"context"
	"errors"
	"testing"

	contentapi "github.com/Zubrium/cdn/engines/content/presentation"
	"github.com/Zubrium/cdn/pkg/contracts"
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

func TestWebsiteBuildWorkflowClientFailsClosedWithoutCore(t *testing.T) {
	client := newWebsiteBuildWorkflowClient(nil, "engine.website")
	ctx := context.Background()
	id := uuid.New()
	if _, err := client.Start(ctx, contracts.WorkflowStartInput{}); !errors.Is(err, errWebsiteBuildUnavailable) {
		t.Fatalf("start without Core = %v", err)
	}
	if _, err := client.Get(ctx, id); !errors.Is(err, errWebsiteBuildUnavailable) {
		t.Fatalf("get without Core = %v", err)
	}
	if _, err := client.GetTaskResult(ctx, id, id); !errors.Is(err, errWebsiteBuildUnavailable) {
		t.Fatalf("task result without Core = %v", err)
	}
	if _, err := client.ResolveTaskID(ctx, id, "prerender:child"); !errors.Is(err, errWebsiteBuildUnavailable) {
		t.Fatalf("resolve without Core = %v", err)
	}
}
