package appconfig

import (
	"testing"
)

const (
	cacheWarmupRouteID   = "01938f7a-68af-7c20-a51d-78b6ca125399"
	cacheWarmupRouteID2  = "01938f7a-68af-7c20-a51d-78b6ca125400"
	cacheWarmupFileA     = "01938f7a-68af-7c10-a51d-78b6ca125301"
	cacheWarmupFileB     = "01938f7a-68af-7c10-a51d-78b6ca125302"
	cacheWarmupFileC     = "01938f7a-68af-7c10-a51d-78b6ca125303"
	cacheWarmupPrincipal = "01938f7a-68af-7c30-a51d-78b6ca125301"
)

func cacheWarmupConfig() Config {
	config := validRuntimeConfig()
	config.Core.Cache.Warmup.PublishedArtifacts = []PublishedArtifactTarget{}
	return config
}

func cacheWarmupGetenv(key string) string {
	values := map[string]string{
		"DB":           "postgres://runtime:local-secret@127.0.0.1:5432/zubrium_core_qa_unit",
		"MIGRATION_DB": "postgres://migrator:local-secret@127.0.0.1:5432/zubrium_core_qa_unit",
		"ACCESS":       "access", "SECRET": "secret",
	}
	return values[key]
}

func publishedArtifact(fileID string) PublishedArtifactTarget {
	return PublishedArtifactTarget{
		RouteID: cacheWarmupRouteID, Locale: "en-US", FileID: fileID, Tier: "required",
		Paths: []string{"/site/"},
		Authorization: WarmupKeyAuthorization{
			Principals: []string{cacheWarmupPrincipal}, Actions: []string{"read"},
		},
	}
}

func TestResolveDerivesPublishedArtifactWarmupKeys(t *testing.T) {
	config := cacheWarmupConfig()
	config.Core.Cache.Warmup.Sources = WarmupSourcesTarget{PublishedArtifacts: true}
	config.Core.Cache.Warmup.PublishedArtifacts = []PublishedArtifactTarget{publishedArtifact(cacheWarmupFileA)}

	resolved, err := config.Resolve(cacheWarmupGetenv)
	if err != nil {
		t.Fatalf("published artifact warm-up configuration rejected: %v", err)
	}
	if len(resolved.Core.Warmup.Keys) != 1 || resolved.Core.Warmup.Keys[0].FileID != cacheWarmupFileA {
		t.Fatalf("derived Core warm-up keys = %+v", resolved.Core.Warmup.Keys)
	}
	if len(resolved.PublishedArtifacts) != 1 || resolved.PublishedArtifacts[0].RouteID != cacheWarmupRouteID ||
		resolved.PublishedArtifacts[0].Locale != "en-US" || resolved.PublishedArtifacts[0].FileID != cacheWarmupFileA {
		t.Fatalf("resolved published artifacts = %+v", resolved.PublishedArtifacts)
	}
}

func TestWarmupSourceScenarios(t *testing.T) {
	t.Run("CACHE-013", func(t *testing.T) {
		config := cacheWarmupConfig()
		config.Core.Cache.Warmup.MaxKeys = 2
		config.Core.Cache.Warmup.Sources = WarmupSourcesTarget{Inline: true, PublishedArtifacts: true}
		config.Core.Cache.Warmup.Keys = []WarmupKeyTarget{{FileID: cacheWarmupFileB, Tier: "optional"}}
		first := publishedArtifact(cacheWarmupFileC)
		first.RouteID = cacheWarmupRouteID2
		first.Tier = "optional"
		config.Core.Cache.Warmup.PublishedArtifacts = []PublishedArtifactTarget{publishedArtifact(cacheWarmupFileA), first}

		resolved, err := config.Resolve(cacheWarmupGetenv)
		if err != nil {
			t.Fatalf("merged warm-up configuration rejected: %v", err)
		}
		keys := resolved.Core.Warmup.Keys
		if len(keys) != 2 || keys[0].FileID != cacheWarmupFileA || keys[1].FileID != cacheWarmupFileB {
			t.Fatalf("merged warm-up keys = %+v", keys)
		}
		if len(resolved.PublishedArtifacts) != 1 || resolved.PublishedArtifacts[0].FileID != cacheWarmupFileA {
			t.Fatalf("router warm-up artifacts must reflect trimmed keys: %+v", resolved.PublishedArtifacts)
		}
	})
}

func TestResolveRejectsInvalidPublishedArtifactSources(t *testing.T) {
	valid := cacheWarmupConfig()
	valid.Core.Cache.Warmup.Sources = WarmupSourcesTarget{PublishedArtifacts: true}
	valid.Core.Cache.Warmup.PublishedArtifacts = []PublishedArtifactTarget{publishedArtifact(cacheWarmupFileA)}

	missingBlock := cacheWarmupConfig()
	missingBlock.Core.Cache.Warmup.Sources = WarmupSourcesTarget{PublishedArtifacts: true}
	missingBlock.Core.Cache.Warmup.PublishedArtifacts = nil

	invalidLocale := cacheWarmupConfig()
	invalidLocale.Core.Cache.Warmup.Sources = WarmupSourcesTarget{PublishedArtifacts: true}
	invalidLocale.Core.Cache.Warmup.PublishedArtifacts = []PublishedArtifactTarget{publishedArtifact(cacheWarmupFileA)}
	invalidLocale.Core.Cache.Warmup.PublishedArtifacts[0].Locale = "not a locale"

	duplicateSlot := cacheWarmupConfig()
	duplicateSlot.Core.Cache.Warmup.Sources = WarmupSourcesTarget{PublishedArtifacts: true}
	duplicateSlot.Core.Cache.Warmup.PublishedArtifacts = []PublishedArtifactTarget{
		publishedArtifact(cacheWarmupFileA), publishedArtifact(cacheWarmupFileB),
	}

	conflictingFile := cacheWarmupConfig()
	conflictingFile.Core.Cache.Warmup.Sources = WarmupSourcesTarget{Inline: true, PublishedArtifacts: true}
	conflictingFile.Core.Cache.Warmup.Keys = []WarmupKeyTarget{{FileID: cacheWarmupFileA, Tier: "optional"}}
	conflictingFile.Core.Cache.Warmup.PublishedArtifacts = []PublishedArtifactTarget{publishedArtifact(cacheWarmupFileA)}

	disabledSource := cacheWarmupConfig()
	disabledSource.Core.Cache.Warmup.Sources = WarmupSourcesTarget{Inline: true}
	disabledSource.Core.Cache.Warmup.Keys = []WarmupKeyTarget{{FileID: cacheWarmupFileA, Tier: "required"}}
	disabledSource.Core.Cache.Warmup.PublishedArtifacts = []PublishedArtifactTarget{publishedArtifact(cacheWarmupFileA)}

	duplicatePrincipal := cacheWarmupConfig()
	duplicatePrincipal.Core.Cache.Warmup.Sources = WarmupSourcesTarget{PublishedArtifacts: true}
	duplicatePrincipal.Core.Cache.Warmup.PublishedArtifacts = []PublishedArtifactTarget{publishedArtifact(cacheWarmupFileA)}
	duplicatePrincipal.Core.Cache.Warmup.PublishedArtifacts[0].Authorization.Principals = []string{cacheWarmupPrincipal, cacheWarmupPrincipal}

	duplicateAction := cacheWarmupConfig()
	duplicateAction.Core.Cache.Warmup.Sources = WarmupSourcesTarget{PublishedArtifacts: true}
	duplicateAction.Core.Cache.Warmup.PublishedArtifacts = []PublishedArtifactTarget{publishedArtifact(cacheWarmupFileA)}
	duplicateAction.Core.Cache.Warmup.PublishedArtifacts[0].Authorization.Actions = []string{"read", "read"}

	tooManyPaths := cacheWarmupConfig()
	tooManyPaths.Core.Cache.Warmup.MaxKeys = 1
	tooManyPaths.Core.Cache.Warmup.Sources = WarmupSourcesTarget{PublishedArtifacts: true}
	tooManyPaths.Core.Cache.Warmup.PublishedArtifacts = []PublishedArtifactTarget{publishedArtifact(cacheWarmupFileA)}
	tooManyPaths.Core.Cache.Warmup.PublishedArtifacts[0].Paths = []string{"/site/", "/site/a"}

	tooManyArtifacts := cacheWarmupConfig()
	tooManyArtifacts.Core.Cache.Warmup.MaxKeys = 1
	tooManyArtifacts.Core.Cache.Warmup.Sources = WarmupSourcesTarget{PublishedArtifacts: true}
	tooManyArtifacts.Core.Cache.Warmup.PublishedArtifacts = []PublishedArtifactTarget{
		publishedArtifact(cacheWarmupFileA), publishedArtifact(cacheWarmupFileB),
	}

	for name, config := range map[string]Config{
		"missing explicit block": missingBlock,
		"invalid locale":         invalidLocale,
		"duplicate slot":         duplicateSlot,
		"conflicting file":       conflictingFile,
		"disabled source":        disabledSource,
		"duplicate principal":    duplicatePrincipal,
		"duplicate action":       duplicateAction,
		"too many paths":         tooManyPaths,
		"too many artifacts":     tooManyArtifacts,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := config.Resolve(cacheWarmupGetenv); err == nil {
				t.Fatal("invalid published artifact warm-up configuration was accepted")
			}
		})
	}
	if _, err := valid.Resolve(cacheWarmupGetenv); err != nil {
		t.Fatalf("valid published artifact warm-up configuration rejected: %v", err)
	}
}
