package integration

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"websiteengine/features/sitebuilder"
	"websiteengine/pkg/principal"
)

// TestVitasilAllCurrentPlacementsBrowserAcceptance verifies the complete set
// of current Vitasil format-2 builds against one isolated model-backed site.
// Existing focused tests cover form submission, media upload/picking, and the
// not-found fallback; this test integrates the current placements in the
// browser without writing to the Vitasil runtime.
func TestVitasilAllCurrentPlacementsBrowserAcceptance(t *testing.T) {
	featureDir := strings.TrimSpace(os.Getenv("TEST_FEATURE_DIR"))
	node := strings.TrimSpace(os.Getenv("PRERENDER_TEST_NODE"))
	module := strings.TrimSpace(os.Getenv("PRERENDER_TEST_MODULE"))
	browser := strings.TrimSpace(os.Getenv("PRERENDER_TEST_BROWSER"))
	if featureDir == "" || node == "" || module == "" || browser == "" {
		t.Skip("set TEST_FEATURE_DIR and PRERENDER_TEST_NODE/MODULE/BROWSER for the all-placement Vitasil browser acceptance")
	}
	requireAllPlacementsBrowserDatabase(t)

	env := newTestEnv(t)
	featureKeys := []string{"background", "catalog", "footer", "form", "header", "hero", "main-data", "not-found"}
	manifests := make(map[string]sitebuilder.Manifest, len(featureKeys))
	versions := make(map[string]string, len(featureKeys))
	features := make([]sitebuilder.BuildRef, 0, len(featureKeys))
	for _, key := range featureKeys {
		archive, version := readExternalFeatureArchive(t, featureDir, key)
		verified, err := sitebuilder.VerifyArchive(archive, int64(len(archive)))
		if err != nil {
			t.Fatalf("verify current Vitasil %s archive: %v", key, err)
		}
		manifest := verified.Manifest
		if manifest.Key != key || manifest.Version != version || manifest.FormatVersion != 2 || manifest.RuntimeABI() != sitebuilder.RuntimeABIStaticModule {
			t.Fatalf("current Vitasil %s archive has unexpected contract: key=%q version=%q format=%d ABI=%q", key, manifest.Key, manifest.Version, manifest.FormatVersion, manifest.RuntimeABI())
		}
		status, body, _ := env.rawRequest(http.MethodPost, "/api/v2/admin/feature-builds", env.adminToken, strings.NewReader(string(archive)), "application/zip")
		if status != http.StatusCreated && status != http.StatusOK {
			t.Fatalf("register current Vitasil %s@%s ZIP: status=%d body=%v", key, version, status, body)
		}
		if body["key"] != key || body["version"] != version {
			t.Fatalf("registered Vitasil build identity differs from ZIP manifest for %s@%s: %v", key, version, body)
		}
		manifests[key], versions[key] = manifest, version
		features = append(features, sitebuilder.BuildRef{FeatureKey: key, Version: version})
		t.Logf("registered current Vitasil feature %s@%s", key, version)
	}

	productPort := modelAcceptancePort(t, manifests["catalog"], "products")
	mediaImage, err := env.media.Upload(context.Background(), principal.Principal{TokenID: modelAcceptanceActor(t, env)}, "vitasil-product-rendition-test.png", makeVitasilResponsiveTestPNG(t))
	if err != nil {
		t.Fatalf("upload disposable responsive Product image: %v", err)
	}
	model, productEntity, productFieldIDs, productFieldKeys := modelAcceptanceEntity("product", "Product", productPort, false)
	entities := map[string]sitebuilder.DataModelEntity{"product": productEntity}
	fieldIDs := map[string]map[string]string{"product": productFieldIDs}
	fieldKeys := map[string]map[string]string{"product": productFieldKeys}
	bindings := make(map[string]sitebuilder.DataInputBinding)
	for key, portKey := range map[string]string{
		"catalog":   "products",
		"main-data": "products",
	} {
		port := modelAcceptancePort(t, manifests[key], portKey)
		modelAcceptanceAssertSameOutputShape(t, productPort, port, key+"."+portKey)
		bindings[key+"."+portKey] = sitebuilder.DataInputBinding{
			InputKey: portKey, EntityID: productEntity.ID,
			Fields: modelAcceptanceFieldBindings(port, productFieldIDs),
		}
	}

	addEntity := func(featureKey, inputKey, entityKey, entityName string) sitebuilder.DataInputBinding {
		port := modelAcceptancePort(t, manifests[featureKey], inputKey)
		_, entity, ids, keys := modelAcceptanceEntity(entityKey, entityName, port, false)
		model.Entities = append(model.Entities, entity)
		entities[featureKey+"."+inputKey] = entity
		fieldIDs[featureKey+"."+inputKey], fieldKeys[featureKey+"."+inputKey] = ids, keys
		binding := sitebuilder.DataInputBinding{InputKey: inputKey, EntityID: entity.ID, Fields: modelAcceptanceFieldBindings(port, ids)}
		bindings[featureKey+"."+inputKey] = binding
		return binding
	}
	for _, item := range []struct{ feature, input, key, name string }{
		{"footer", "footer", "footer", "Footer"},
		{"footer", "links", "footer_link", "Footer link"},
		{"footer", "contacts", "footer_contact", "Footer contact"},
		{"form", "form", "contact_form", "Contact form"},
		{"header", "header", "site_header", "Site header"},
		{"header", "navigation", "navigation_link", "Navigation link"},
		{"hero", "hero", "hero_content", "Hero content"},
		{"main-data", "info", "main_info", "Main info"},
		{"main-data", "advantages", "advantage", "Advantage"},
		{"main-data", "slider", "product_slider", "Product slider"},
		{"not-found", "notFound", "not_found", "Not found"},
	} {
		addEntity(item.feature, item.input, item.key, item.name)
	}
	legalSettingsPort := modelAcceptancePort(t, manifests["form"], "legalSettings")
	_, legalSettingsEntity, legalSettingsFieldIDs, legalSettingsFieldKeys := modelAcceptanceEntity("legal_settings", "LegalSettings", legalSettingsPort, false)
	legalSettingsEntity.Cardinality = sitebuilder.DataModelEntityCardinalitySingleton
	model.Entities = append(model.Entities, legalSettingsEntity)
	fieldIDs["form.legalSettings"], fieldKeys["form.legalSettings"] = legalSettingsFieldIDs, legalSettingsFieldKeys
	bindings["form.legalSettings"] = sitebuilder.DataInputBinding{
		InputKey: legalSettingsPort.Key, EntityID: legalSettingsEntity.ID,
		Fields: modelAcceptanceFieldBindings(legalSettingsPort, legalSettingsFieldIDs),
	}

	placements := map[string]sitebuilder.Placement{
		"background": {ID: "vitasil-background", Build: sitebuilder.BuildRef{FeatureKey: "background", Version: versions["background"]}, Slot: "background"},
		"header": {
			ID: "vitasil-header", Build: sitebuilder.BuildRef{FeatureKey: "header", Version: versions["header"]}, Slot: "header",
			DataBindings: []sitebuilder.DataInputBinding{bindings["header.header"], bindings["header.navigation"]},
		},
		"hero": {
			ID: "vitasil-hero", Build: sitebuilder.BuildRef{FeatureKey: "hero", Version: versions["hero"]}, Slot: "main",
			DataBindings: []sitebuilder.DataInputBinding{bindings["hero.hero"]},
		},
		"main-data": {
			ID: "vitasil-main-data", Build: sitebuilder.BuildRef{FeatureKey: "main-data", Version: versions["main-data"]}, Slot: "main",
			DataBindings: []sitebuilder.DataInputBinding{bindings["main-data.info"], bindings["main-data.advantages"], bindings["main-data.slider"], bindings["main-data.products"]},
		},
		"form": {
			ID: "vitasil-form", Build: sitebuilder.BuildRef{FeatureKey: "form", Version: versions["form"]}, Slot: "main",
			DataBindings: []sitebuilder.DataInputBinding{bindings["form.form"], bindings["form.legalSettings"]},
		},
		"form-page": {
			ID: "vitasil-form-page", Build: sitebuilder.BuildRef{FeatureKey: "form", Version: versions["form"]}, Slot: "main",
			DataBindings: []sitebuilder.DataInputBinding{bindings["form.form"], bindings["form.legalSettings"]},
		},
		"footer": {
			ID: "vitasil-footer", Build: sitebuilder.BuildRef{FeatureKey: "footer", Version: versions["footer"]}, Slot: "footer",
			DataBindings: []sitebuilder.DataInputBinding{bindings["footer.footer"], bindings["footer.links"], bindings["footer.contacts"]},
		},
		"catalog": {
			ID: "vitasil-catalog", Build: sitebuilder.BuildRef{FeatureKey: "catalog", Version: versions["catalog"]}, Slot: "main",
			Arguments: map[string]sitebuilder.ArgumentBinding{
				"title":  {Source: "literal", Value: json.RawMessage(`"Каталог Vitasil QA"`)},
				"action": {Source: "literal", Value: json.RawMessage(`{"label":"Связаться QA","to":"/"}`)},
			},
			DataBindings: []sitebuilder.DataInputBinding{bindings["catalog.products"]},
		},
		"not-found": {
			ID: "vitasil-not-found", Build: sitebuilder.BuildRef{FeatureKey: "not-found", Version: versions["not-found"]}, Slot: "main",
			DataBindings: []sitebuilder.DataInputBinding{bindings["not-found.notFound"]},
		},
	}

	config := sitebuilder.SiteConfig{
		FormatVersion: 3,
		Features:      features,
		DataModel:     &model,
		SystemPages:   map[string]string{"404": "not-found"},
		Pages: []sitebuilder.Page{
			{ID: "home", Name: "Vitasil QA home", Kind: "page", Placements: []sitebuilder.Placement{placements["background"], placements["header"], placements["hero"], placements["main-data"], placements["form"], placements["footer"]}},
			{ID: "catalog", Name: "Vitasil QA catalog", Kind: "collection-list", Placements: []sitebuilder.Placement{placements["catalog"]}},
			{ID: "form", Name: "Vitasil QA form", Kind: "page", Placements: []sitebuilder.Placement{placements["form-page"]}},
			{ID: "not-found", Name: "Vitasil QA not found", Kind: "page", Placements: []sitebuilder.Placement{placements["not-found"]}},
		},
		RouteTree: sitebuilder.RouteTreeNode{ID: "site", Type: "site", Children: []sitebuilder.RouteTreeNode{
			{ID: "home-route", Type: "path", Path: "/", PageID: "home"},
			{ID: "catalog-route", Type: "path", Path: "/catalog", PageID: "catalog"},
			{ID: "form-route", Type: "path", Path: "/form", PageID: "form"},
			{ID: "not-found-route", Type: "catchAll", Name: "path", PageID: "not-found"},
		}},
	}
	rawConfig, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	var configMap map[string]any
	if err := json.Unmarshal(rawConfig, &configMap); err != nil {
		t.Fatal(err)
	}
	snapshot := modelAcceptanceSaveAndDeploySnapshot(t, env, configMap, "Vitasil all current feature placements")
	snapshotID, err := uuid.Parse(snapshot["id"].(string))
	if err != nil {
		t.Fatalf("parse isolated snapshot ID: %v", err)
	}
	actor := modelAcceptanceActor(t, env)
	contentWorkspace, err := env.snapshots.ModelContentWorkspace(context.Background(), snapshotID)
	if err != nil {
		t.Fatalf("read auto-generated LegalSettings singleton draft: %v", err)
	}
	var legalSettingsRecordID string
	for _, existing := range contentWorkspace.Records {
		if existing.EntityID == legalSettingsEntity.ID {
			legalSettingsRecordID = existing.ID
			break
		}
	}
	if legalSettingsRecordID == "" {
		t.Fatalf("snapshot creation did not generate the form LegalSettings singleton draft: %+v", contentWorkspace)
	}
	value := func(featureKey, inputKey string, values map[string]any) map[string]any {
		return modelAcceptanceValues(fieldKeys[featureKey+"."+inputKey], values)
	}
	products := []sitebuilder.ModelContentRecord{
		{EntityID: productEntity.ID, SortOrder: 0, Values: modelAcceptanceValues(productFieldKeys, map[string]any{"/title": "Шиповник Vitasil QA", "/category": "Напитки QA", "/price": "100 ₽", "/description": "Шиповник description QA", "/imageSrc": mediaImage.ID.String()})},
		{EntityID: productEntity.ID, SortOrder: 1, Values: modelAcceptanceValues(productFieldKeys, map[string]any{"/title": "Мёд Vitasil QA", "/category": "Мёд QA", "/price": "200 ₽", "/description": "Мёд description QA", "/imageSrc": mediaImage.ID.String()})},
	}
	initial := []sitebuilder.ModelContentRecord{
		products[0], products[1],
		{EntityID: entities["header.header"].ID, Values: value("header", "header", map[string]any{"/brand": "Vitasil header QA", "/brandTo": "/", "/navigationLabel": "Навигация Vitasil QA"})},
		{EntityID: entities["header.navigation"].ID, SortOrder: 0, Values: value("header", "navigation", map[string]any{"/label": "Каталог QA", "/to": "/catalog", "/end": true})},
		{EntityID: entities["hero.hero"].ID, Values: value("hero", "hero", map[string]any{
			"/title": "Vitasil hero QA", "/subtitle": "Hero description QA", "/actionLabel": "В каталог QA", "/actionTo": "/catalog",
			"/artwork/cow": mediaImage.ID.String(), "/artwork/wheatBundle": mediaImage.ID.String(),
			"/artwork/wheatStalk": mediaImage.ID.String(), "/artwork/wheatSprig": mediaImage.ID.String(),
		})},
		{EntityID: entities["main-data.info"].ID, Values: value("main-data", "info", map[string]any{"/title": "Vitasil info QA"})},
		{EntityID: entities["main-data.advantages"].ID, SortOrder: 0, Values: value("main-data", "advantages", map[string]any{"/icon": "quality", "/title": "Преимущество QA", "/description": "Преимущество description QA"})},
		{EntityID: entities["main-data.slider"].ID, Values: value("main-data", "slider", map[string]any{"/eyebrow": "Ассортимент QA", "/title": "Слайдер QA", "/previousLabel": "Назад QA", "/nextLabel": "Вперёд QA", "/itemsLabel": "Позиции QA", "/catalogActionLabel": "Каталог QA", "/catalogActionTo": "/catalog"})},
		{EntityID: entities["footer.footer"].ID, Values: value("footer", "footer", map[string]any{"/brand": "Vitasil footer QA", "/description": "Footer description QA", "/contactTitle": "Контакты QA", "/navigationTitle": "Ссылки QA", "/navigationLabel": "Навигация футера QA", "/copyright": "Copyright QA"})},
		{EntityID: entities["footer.links"].ID, SortOrder: 0, Values: value("footer", "links", map[string]any{"/label": "Главная QA", "/to": "/"})},
		{EntityID: entities["footer.contacts"].ID, SortOrder: 0, Values: value("footer", "contacts", map[string]any{"/label": "Почта QA", "/value": "qa@example.test", "/to": "mailto:qa@example.test"})},
		{EntityID: entities["form.form"].ID, Values: value("form", "form", map[string]any{"/title": "Форма Vitasil QA", "/nameLabel": "Имя QA", "/emailLabel": "Почта QA", "/consentLabel": "Согласен QA", "/consentLinkLabel": "условиями QA", "/consentLinkTo": "/personal-data-consent", "/submitLabel": "Отправить QA", "/successPrefix": "Заявка отправлена QA"})},
		{ID: legalSettingsRecordID, EntityID: legalSettingsEntity.ID, Values: modelAcceptanceValues(legalSettingsFieldKeys, map[string]any{"/consentVersion": "legal-qa-2026-11"})},
		{EntityID: entities["not-found.notFound"].ID, Values: value("not-found", "notFound", map[string]any{"/title": "Страница не найдена QA", "/description": "Проверьте адрес QA", "/backToHomeLabel": "На главную QA", "/backToHomeTo": "/"})},
	}
	draft, err := env.snapshots.SaveModelContentWorkspace(context.Background(), actor, snapshotID, sitebuilder.ModelContentDraftInput{Revision: contentWorkspace.Revision, Records: initial})
	if err != nil {
		t.Fatalf("save all Vitasil model-backed records: %v", err)
	}
	recordIDs := make(map[string][]string)
	productIDsByTitle := make(map[string]string)
	for _, record := range draft.Records {
		recordIDs[record.EntityID] = append(recordIDs[record.EntityID], record.ID)
		if record.EntityID == productEntity.ID {
			productIDsByTitle[record.Values[productFieldKeys["/title"]].(string)] = record.ID
		}
	}
	if len(productIDsByTitle) != 2 {
		t.Fatalf("expected two shared Product records, got %v", productIDsByTitle)
	}
	selectOne := func(placementID, inputKey, entityKey string) sitebuilder.ModelContentSelection {
		ids := recordIDs[entities[entityKey].ID]
		if len(ids) != 1 {
			t.Fatalf("expected one model record for %s, got %v", entityKey, ids)
		}
		return sitebuilder.ModelContentSelection{PlacementID: placementID, InputKey: inputKey, Mode: "ids", RecordIDs: ids}
	}
	selections := []sitebuilder.ModelContentSelection{
		selectOne("vitasil-header", "header", "header.header"),
		selectOne("vitasil-header", "navigation", "header.navigation"),
		selectOne("vitasil-hero", "hero", "hero.hero"),
		selectOne("vitasil-main-data", "info", "main-data.info"),
		selectOne("vitasil-main-data", "slider", "main-data.slider"),
		{PlacementID: "vitasil-main-data", InputKey: "advantages", Mode: "ids", RecordIDs: recordIDs[entities["main-data.advantages"].ID]},
		{PlacementID: "vitasil-main-data", InputKey: "products", Mode: "ids", RecordIDs: []string{productIDsByTitle["Мёд Vitasil QA"]}},
		selectOne("vitasil-footer", "footer", "footer.footer"),
		{PlacementID: "vitasil-footer", InputKey: "links", Mode: "ids", RecordIDs: recordIDs[entities["footer.links"].ID]},
		{PlacementID: "vitasil-footer", InputKey: "contacts", Mode: "ids", RecordIDs: recordIDs[entities["footer.contacts"].ID]},
		selectOne("vitasil-form", "form", "form.form"),
		selectOne("vitasil-form-page", "form", "form.form"),
		selectOne("vitasil-not-found", "notFound", "not-found.notFound"),
		{PlacementID: "vitasil-catalog", InputKey: "products", Mode: "ids", RecordIDs: []string{productIDsByTitle["Шиповник Vitasil QA"], productIDsByTitle["Мёд Vitasil QA"]}},
	}
	selected, err := env.snapshots.SaveModelContentWorkspace(context.Background(), actor, snapshotID, sitebuilder.ModelContentDraftInput{Revision: draft.Revision, Records: draft.Records, Selections: selections})
	if err != nil {
		t.Fatalf("save Vitasil editorial record selections: %v", err)
	}
	_, publishErr := env.snapshots.PublishModelContentWorkspace(context.Background(), actor, snapshotID, selected.Revision)
	if errors.Is(publishErr, sitebuilder.ErrConflict) && strings.Contains(publishErr.Error(), "required media renditions are still processing") {
		for {
			processed, processErr := env.media.ProcessOne(context.Background())
			if processErr != nil {
				t.Fatalf("process Vitasil content media renditions before publication: %v", processErr)
			}
			if !processed {
				break
			}
		}
		_, publishErr = env.snapshots.PublishModelContentWorkspace(context.Background(), actor, snapshotID, selected.Revision)
	}
	if publishErr != nil {
		t.Fatalf("publish Vitasil model-backed records: %v", publishErr)
	}

	branchID := uuid.MustParse(string(env.devBranch))
	branchBefore, err := env.snapshots.Branch(context.Background(), branchID)
	if err != nil || branchBefore.SnapshotID == nil || branchBefore.SnapshotID.String() != snapshotID.String() {
		t.Fatalf("dev test branch does not point to the isolated Vitasil snapshot before browser flow: branch=%+v err=%v", branchBefore, err)
	}
	assertVitasilPublicGzip(t, env.devServer.URL)
	modelAcceptanceRunAllPlacementsBrowser(t, env, snapshotID.String(), branchID.String(), node, module, browser)
	if lighthouse := strings.TrimSpace(os.Getenv("LIGHTHOUSE_BIN")); lighthouse != "" {
		basePath := strings.TrimSpace(os.Getenv("LIGHTHOUSE_OUTPUT_PATH"))
		if basePath == "" {
			t.Fatal("LIGHTHOUSE_OUTPUT_PATH must name a disposable report path when LIGHTHOUSE_BIN is set")
		}
		warmRepetitions := 1
		if raw := strings.TrimSpace(os.Getenv("LIGHTHOUSE_REPETITIONS")); raw != "" {
			parsed, parseErr := strconv.Atoi(raw)
			if parseErr != nil || parsed < 1 || parsed > 5 {
				t.Fatalf("LIGHTHOUSE_REPETITIONS must be an integer from 1 to 5; got %q", raw)
			}
			warmRepetitions = parsed
		}
		coldRepetitions := 1
		if raw := strings.TrimSpace(os.Getenv("LIGHTHOUSE_COLD_REPETITIONS")); raw != "" {
			parsed, parseErr := strconv.Atoi(raw)
			if parseErr != nil || parsed < 1 || parsed > 5 {
				t.Fatalf("LIGHTHOUSE_COLD_REPETITIONS must be an integer from 1 to 5; got %q", raw)
			}
			coldRepetitions = parsed
		}
		cacheStates := []string{"cold", "warm"}
		if os.Getenv("LIGHTHOUSE_PRERENDER") == "0" {
			cacheStates = []string{"none"}
		}
		routes := []struct{ key, path string }{{"home", "/"}, {"catalog", "/catalog"}, {"form", "/form"}}
		if rawRoutes := strings.TrimSpace(os.Getenv("LIGHTHOUSE_ROUTES")); rawRoutes != "" {
			available := make(map[string]struct{}, len(routes))
			for _, route := range routes {
				available[route.key] = struct{}{}
			}
			selected := make([]struct{ key, path string }, 0, len(routes))
			for _, key := range strings.Split(rawRoutes, ",") {
				key = strings.TrimSpace(key)
				if key == "" {
					continue
				}
				if _, ok := available[key]; !ok {
					t.Fatalf("LIGHTHOUSE_ROUTES contains unsupported route %q", key)
				}
				for _, route := range routes {
					if route.key == key {
						selected = append(selected, route)
						break
					}
				}
			}
			if len(selected) == 0 {
				t.Fatal("LIGHTHOUSE_ROUTES must select home, catalog, and/or detail")
			}
			routes = selected
		}
		for _, route := range routes {
			for _, profile := range []string{"mobile", "desktop"} {
				for _, cacheState := range cacheStates {
					repetitions := warmRepetitions
					if cacheState == "cold" {
						repetitions = coldRepetitions
					}
					var warmServer *httptest.Server
					var warmLogs bytes.Buffer
					if cacheState == "warm" {
						warmServer = newPublicIntegrationServer(t, env.routerDeps, env.devBranch, lighthouseTestLogger(&warmLogs))
						response, warmErr := http.Get(warmServer.URL + route.path)
						if warmErr != nil {
							warmServer.Close()
							t.Fatalf("warm Lighthouse %s cache: %v", route.key, warmErr)
						}
						_ = response.Body.Close()
						if response.StatusCode != http.StatusOK || response.Header.Get("X-Engine-Prerender") != "hit" {
							warmServer.Close()
							t.Fatalf("warm Lighthouse %s cache returned status=%d prerender=%q", route.key, response.StatusCode, response.Header.Get("X-Engine-Prerender"))
						}
						if !strings.Contains(warmLogs.String(), `cache_result=miss`) {
							warmServer.Close()
							t.Fatalf("warm Lighthouse %s cache primer did not record an initial miss: %s", route.key, warmLogs.String())
						}
					}
					performanceScores := make([]float64, 0, repetitions)
					for repetition := 1; repetition <= repetitions; repetition++ {
						server := warmServer
						logs := &warmLogs
						if cacheState == "cold" || cacheState == "none" {
							logs = &bytes.Buffer{}
							server = newPublicIntegrationServer(t, env.routerDeps, env.devBranch, lighthouseTestLogger(logs))
						}
						hitsBefore := strings.Count(logs.String(), `cache_result=hit`)
						outputBase := strings.TrimSuffix(basePath, ".json") + fmt.Sprintf("-%s-%s-%s-r%02d", route.key, profile, cacheState, repetition)
						outputPath := outputBase + ".report.json"
						args := []string{server.URL + route.path, "--output=json,html", "--output-path=" + outputBase,
							"--chrome-path=" + browser, "--chrome-flags=--headless --no-sandbox", "--only-categories=performance,accessibility,best-practices,seo", "--save-assets", "--quiet"}
						if profile == "desktop" {
							args = append(args, "--preset=desktop")
						}
						ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
						command := exec.CommandContext(ctx, lighthouse, args...)
						output, err := command.CombinedOutput()
						cancel()
						if cacheState != "warm" {
							server.Close()
						}
						if err != nil {
							t.Fatalf("Lighthouse %s %s %s run %d failed: %v\n%s", route.key, profile, cacheState, repetition, err, output)
						}
						cacheRecords := strings.Count(logs.String(), `cache_result=hit`) - hitsBefore
						if cacheState == "warm" && cacheRecords < 1 {
							t.Fatalf("Lighthouse %s %s warm run %d did not hit HTML cache: %s", route.key, profile, repetition, logs.String())
						}
						if cacheState == "cold" && !strings.Contains(logs.String(), `cache_result=miss`) {
							t.Fatalf("Lighthouse %s %s cold run %d did not record a cache miss: %s", route.key, profile, repetition, logs.String())
						}
						report, err := os.ReadFile(outputPath)
						if err != nil {
							t.Fatalf("read Lighthouse %s %s %s report: %v", route.key, profile, cacheState, err)
						}
						var evidence struct {
							Categories map[string]struct {
								Score *float64 `json:"score"`
							} `json:"categories"`
						}
						if err := json.Unmarshal(report, &evidence); err != nil {
							t.Fatalf("decode Lighthouse %s %s %s report: %v", route.key, profile, cacheState, err)
						}
						performance := lighthouseScore(evidence.Categories["performance"].Score)
						performanceScores = append(performanceScores, performance)
						t.Logf("Lighthouse prerender=%t %s %s %s run %d cache_hits=%d scores: performance=%.2f accessibility=%.2f best-practices=%.2f seo=%.2f report=%s", os.Getenv("LIGHTHOUSE_PRERENDER") != "0", route.key, profile, cacheState, repetition, cacheRecords,
							performance, lighthouseScore(evidence.Categories["accessibility"].Score),
							lighthouseScore(evidence.Categories["best-practices"].Score), lighthouseScore(evidence.Categories["seo"].Score), outputPath)
					}
					if cacheState == "warm" && os.Getenv("LIGHTHOUSE_PRERENDER") != "0" {
						sort.Float64s(performanceScores)
						median := performanceScores[len(performanceScores)/2]
						if median < 1 {
							t.Fatalf("Lighthouse %s %s %s median performance %.2f is below the required 1.00; scores=%v", route.key, profile, cacheState, median, performanceScores)
						}
						t.Logf("Lighthouse %s %s %s median performance=%.2f runs=%v", route.key, profile, cacheState, median, performanceScores)
					}
					if warmServer != nil {
						warmServer.Close()
					}
				}
			}
		}
	}
	branchAfter, err := env.snapshots.Branch(context.Background(), branchID)
	if err != nil || branchAfter.SnapshotID == nil || branchAfter.SnapshotID.String() != snapshotID.String() || branchAfter.Revision != branchBefore.Revision {
		t.Fatalf("browser feature-placement workflow changed the branch assignment: before=%+v after=%+v err=%v", branchBefore, branchAfter, err)
	}
	t.Logf("browser acceptance exercised current Vitasil builds: %s", strings.Join(featureKeys, ", "))
}

func assertVitasilPublicGzip(t *testing.T, baseURL string) {
	t.Helper()
	runtimeBody := assertGzipPublicText(t, baseURL, "/api/v2/runtime?path=%2F", "application/json")
	var composition struct {
		Placements []struct {
			EntryURL  string   `json:"entryUrl"`
			StyleURLs []string `json:"styleUrls"`
		} `json:"placements"`
	}
	if err := json.Unmarshal(runtimeBody, &composition); err != nil {
		t.Fatalf("decode gzip runtime composition: %v", err)
	}
	if len(composition.Placements) == 0 {
		t.Fatal("gzip runtime composition has no feature placements")
	}
	assertGzipPublicText(t, baseURL, composition.Placements[0].EntryURL, "javascript")
	if len(composition.Placements[0].StyleURLs) == 0 {
		t.Fatal("gzip feature composition has no stylesheet URL")
	}
	assertGzipPublicText(t, baseURL, composition.Placements[0].StyleURLs[0], "text/css")
	assertGzipPublicText(t, baseURL, "/", "text/html")
	if strings.TrimSpace(os.Getenv("LIGHTHOUSE_BIN")) != "" || os.Getenv("VITASIL_BROWSER_INDEXABLE") == "1" {
		assertGzipPublicText(t, baseURL, "/sitemap.xml", "application/xml")
	}
	t.Log("public gzip verified for runtime composition, feature JavaScript/CSS, HTML, and sitemap where indexable")
}

func lighthouseTestLogger(output *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(output, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func makeVitasilResponsiveTestPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 1600, 1200))
	for y := 0; y < 1200; y++ {
		for x := 0; x < 1600; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 251), G: uint8(y % 239), B: uint8((x + y) % 233), A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		t.Fatalf("encode responsive image fixture: %v", err)
	}
	return encoded.Bytes()
}

func assertGzipPublicText(t *testing.T, baseURL, resourcePath, expectedType string) []byte {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, baseURL+resourcePath, nil)
	if err != nil {
		t.Fatalf("build gzip request for %s: %v", resourcePath, err)
	}
	request.Header.Set("Accept-Encoding", "gzip")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("request gzip resource %s: %v", resourcePath, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("gzip resource %s returned HTTP %d", resourcePath, response.StatusCode)
	}
	if !strings.Contains(strings.ToLower(response.Header.Get("Content-Type")), expectedType) {
		t.Fatalf("resource %s Content-Type = %q, want %q", resourcePath, response.Header.Get("Content-Type"), expectedType)
	}
	if !strings.Contains(response.Header.Get("Vary"), "Accept-Encoding") {
		t.Fatalf("resource %s Vary omits Accept-Encoding: %q", resourcePath, response.Header.Get("Vary"))
	}
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read resource %s: %v", resourcePath, err)
	}
	body := raw
	if response.Header.Get("Content-Encoding") == "gzip" {
		reader, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			t.Fatalf("open gzip resource %s: %v", resourcePath, err)
		}
		body, err = io.ReadAll(reader)
		if err != nil {
			t.Fatalf("read gzip resource %s: %v", resourcePath, err)
		}
		if err := reader.Close(); err != nil {
			t.Fatalf("close gzip resource %s: %v", resourcePath, err)
		}
	} else if len(raw) >= 512 {
		t.Fatalf("large text resource %s (%d bytes) was not gzip encoded", resourcePath, len(raw))
	}
	if len(body) == 0 {
		t.Fatalf("gzip resource %s decoded to an empty body", resourcePath)
	}
	return body
}

func lighthouseScore(value *float64) float64 {
	if value == nil {
		return -1
	}
	return *value
}

func modelAcceptanceRunAllPlacementsBrowser(t *testing.T, env *testEnv, snapshotID, branchID, node, module, browser string) {
	t.Helper()
	script := `import { pathToFileURL } from "node:url";
const { chromium } = await import(pathToFileURL(process.env.PLAYWRIGHT_TEST_MODULE).href);
const browser = await chromium.launch({ executablePath: process.env.PLAYWRIGHT_TEST_BROWSER, headless: true, args: ["--no-sandbox"] });
const base = process.env.VITASIL_PUBLIC_BASE_URL;
const errors = [];
const expected = {
  "/": ["background", "header", "hero", "main-data", "form", "footer"],
  "/catalog": ["catalog"],
  "/form": ["form"],
  "/missing-vitasil-page": ["not-found"],
};
const slots = { background: "background", header: "header", hero: "main", "main-data": "main", form: "main", footer: "footer", catalog: "main", "not-found": "main" };
const sentinel = {
		header: "Каталог QA", hero: "Vitasil hero QA", "main-data": "Vitasil info QA",
  form: "Форма Vitasil QA", footer: "Vitasil footer QA", catalog: "Каталог Vitasil QA",
  "not-found": "Страница не найдена QA",
};
try {
  const context = await browser.newContext({ viewport: { width: 1280, height: 900 } });
  const page = await context.newPage();
  page.on("pageerror", error => errors.push("pageerror: " + error.message));
	page.on("console", message => {
	  if (message.type() !== "error") return;
	  const location = message.location();
	  const path = location.url ? new URL(location.url).pathname : "";
	  if (location.url && new URL(location.url).hostname === "cdn.example.test") return;
	  // Chromium probes /favicon.ico before app metadata is installed. It also
	  // reports the intentional 404 document status for the custom not-found route.
	  if (path === "/favicon.ico" || path === "/missing-vitasil-page") return;
	  errors.push("console: " + message.text() + " at " + JSON.stringify(location));
	});
	page.on("response", response => {
	  if (response.status() >= 400 && response.request().resourceType() !== "document" && new URL(response.url()).pathname !== "/favicon.ico") {
	    errors.push("resource response: " + response.status() + " " + response.url());
	  }
	});
	page.on("requestfailed", request => {
	  if (new URL(request.url()).hostname === "cdn.example.test") return;
	  // Responsive picture candidate requests are intentionally cancelled when
	  // the viewport changes between the mobile and desktop assertions.
	  if (request.resourceType() === "image" && request.failure()?.errorText === "net::ERR_ABORTED") return;
	  errors.push("requestfailed: " + request.method() + " " + request.url() + " " + (request.failure()?.errorText || ""));
	});
  for (const [route, keys] of Object.entries(expected)) {
    const response = await page.goto(base + route, { waitUntil: "domcontentloaded" });
    if (!response) throw new Error(route + ": navigation returned no response");
    if (route === "/missing-vitasil-page" ? response.status() !== 404 : !response.ok()) throw new Error(route + ": unexpected HTTP status " + response.status());
    await page.waitForFunction(() => {
      const root = document.getElementById("root");
      return Boolean(root && !root.hidden && !document.getElementById("prerender-root"));
    }, { timeout: 30000 });
    const initialHydration = await page.evaluate(() => [...document.querySelectorAll("[data-engine-placement]")]
      .map(root => ({ id: root.getAttribute("data-engine-placement"), ready: root.hasAttribute("data-runtime-feature-ready") })));
    for (const placement of initialHydration) {
      if (!placement.id || placement.ready) continue;
      await page.locator('[data-engine-placement="' + placement.id + '"]').evaluate(root => root.scrollIntoView({ block: "center" }));
      await page.waitForFunction(id => [...document.querySelectorAll("[data-engine-placement]")]
        .some(root => root.getAttribute("data-engine-placement") === id
          && (root.hasAttribute("data-runtime-feature-ready") || root.hasAttribute("data-runtime-feature-error"))), placement.id, { timeout: 30000 });
    }
    try {
      await page.waitForFunction(() => document.documentElement.dataset.engineReady === "true", { timeout: 30000 });
    } catch (error) {
      const runtimeState = await page.evaluate(() => ({
        engineReady: document.documentElement.dataset.engineReady || "",
        title: document.title,
        placements: [...document.querySelectorAll("[data-engine-feature][data-engine-placement]")].map(root => ({
          feature: root.getAttribute("data-engine-feature"),
          ready: root.hasAttribute("data-runtime-feature-ready"),
          failed: root.hasAttribute("data-runtime-feature-error"),
          text: (root.innerText || root.textContent || "").replace(/\\s+/g, " ").trim().slice(0, 120),
        })),
        rootText: (document.getElementById("root")?.innerText || "").slice(0, 240),
      }));
      throw new Error(route + ": runtime readiness timed out: " + JSON.stringify({ runtimeState, errors: errors.slice(0, 12), message: error.message }));
    }
    const rendered = await page.evaluate(({ keys, slots, sentinel, route }) => {
      const placements = [...document.querySelectorAll("[data-engine-feature][data-engine-placement]")];
      const summaries = placements.map(root => ({
        feature: root.getAttribute("data-engine-feature"),
        id: root.getAttribute("data-engine-placement"),
        ready: root.hasAttribute("data-runtime-feature-ready"),
        slot: root.getAttribute("data-engine-slot") || "",
        text: (root.innerText || root.textContent || "").replace(/\\s+/g, " ").trim(),
      }));
      const byFeature = Object.fromEntries(summaries.map(item => [item.feature, item]));
      const missing = keys.filter(key => !byFeature[key]);
      const unexpected = summaries.map(item => item.feature).filter(key => !keys.includes(key));
      const notReady = summaries.filter(item => !item.ready).map(item => item.feature);
      const wrongSlots = summaries.filter(item => slots[item.feature] !== item.slot).map(item => ({ feature: item.feature, slot: item.slot, expected: slots[item.feature] }));
      const main = document.querySelector("main.runtime-slot--main");
      const wrongParents = placements.filter(root => root.parentElement !== (root.getAttribute("data-engine-slot") === "main" ? main : document.getElementById("root"))).map(root => root.getAttribute("data-engine-feature"));
      const nestedPlacements = placements.some(parent => placements.some(child => parent !== child && parent.contains(child)));
      const extraSlotHosts = [...document.querySelectorAll(".runtime-slot")].filter(host => host !== main).map(host => host.className);
      const duplicateMainHosts = document.querySelectorAll("main.runtime-slot--main").length !== 1;
      const missingSentinels = Object.entries(sentinel).filter(([key, value]) => keys.includes(key) && !(byFeature[key]?.text || "").includes(value)).map(([key, value]) => ({ feature: key, value, text: byFeature[key]?.text || "" }));
      const featureRoot = key => document.querySelector('[data-engine-feature="' + key + '"]');
      const semanticIssues = [];
      if (route === "/") {
        if (!featureRoot("hero")?.querySelector("section h1")) semanticIssues.push("hero section/h1");
        if (!featureRoot("main-data")?.querySelector('section[aria-label] section[aria-labelledby="home-info-title"] h2')) semanticIssues.push("main-data labelled section/heading");
        if (!featureRoot("main-data")?.querySelector('section[aria-labelledby="home-slider-title"] h2')) semanticIssues.push("main-data slider heading");
        if (!featureRoot("form")?.querySelector("section > form")) semanticIssues.push("form section/form");
        if (!featureRoot("footer")?.querySelector("footer")) semanticIssues.push("footer landmark");
      } else if (route === "/catalog") {
        if (!featureRoot("catalog")?.querySelector('section[aria-labelledby="catalog-title"] ul > li article h2')) semanticIssues.push("catalog section/list/article heading");
        if (featureRoot("catalog")?.querySelector('[data-slot="breadcrumb"], nav[aria-label="breadcrumb"]')) semanticIssues.push("catalog breadcrumb remains rendered");
        if (featureRoot("catalog")?.querySelector('ul > li article a')) semanticIssues.push("catalog product card links to a detail page");
      } else if (route === "/form") {
        if (!featureRoot("form")?.querySelector("section > form")) semanticIssues.push("form page section/form");
      } else if (route === "/missing-vitasil-page") {
        if (!featureRoot("not-found")?.querySelector("section[aria-labelledby] h1")) semanticIssues.push("not-found section/h1");
      }
      return { keys, summaries, missing, unexpected, notReady, wrongSlots, wrongParents, nestedPlacements, extraSlotHosts, duplicateMainHosts, missingSentinels, semanticIssues, fileInputs: document.querySelectorAll('input[type="file"]').length, runtimeErrors: document.querySelectorAll(".runtime-feature-error").length, mainCount: document.querySelectorAll("main").length };
    }, { keys, slots, sentinel, route });
    if (rendered.missing.length || rendered.unexpected.length || rendered.notReady.length || rendered.wrongSlots.length || rendered.wrongParents.length || rendered.nestedPlacements || rendered.extraSlotHosts.length || rendered.duplicateMainHosts || rendered.missingSentinels.length || rendered.semanticIssues.length || rendered.runtimeErrors || rendered.fileInputs || rendered.mainCount !== 1) {
      throw new Error(route + ": placements did not match the current manifest/runtime contract: " + JSON.stringify(rendered));
    }
    const responsive = [];
    await page.evaluate(() => window.scrollTo(0, 0));
    for (const width of [360, 390, 768, 1280]) {
      await page.setViewportSize({ width, height: 900 });
      await page.waitForTimeout(40);
      const metrics = await page.evaluate(() => ({
        viewport: window.innerWidth,
        document: document.documentElement.scrollWidth,
        body: document.body.scrollWidth,
        mainCount: document.querySelectorAll("main").length,
        featureRoots: document.querySelectorAll("[data-engine-feature][data-engine-placement]").length,
      }));
      if (metrics.document > width || metrics.body > width || metrics.mainCount !== 1 || metrics.featureRoots !== keys.length) {
        throw new Error(route + ": responsive DOM acceptance failed at " + width + "px: " + JSON.stringify(metrics));
      }
      responsive.push(metrics);
    }
    await page.setViewportSize({ width: 1280, height: 900 });
    if (route === "/catalog") {
      const imageState = async width => {
        await page.setViewportSize({ width, height: 900 });
        await page.waitForTimeout(150);
        return await page.evaluate(() => {
          const picture = document.querySelector('[data-engine-feature="catalog"] picture');
          const image = picture?.querySelector("img");
          return {
            currentSrc: image?.currentSrc || "",
            sourceType: picture?.querySelector("source")?.type || "",
            sizes: picture?.querySelector("source")?.sizes || "",
            srcSet: picture?.querySelector("source")?.srcset || "",
            renderedWidth: image?.getBoundingClientRect().width || 0,
            candidates: (picture?.querySelector("source")?.srcset || "").split(",").filter(Boolean).length,
            intrinsic: [image?.getAttribute("width"), image?.getAttribute("height")],
          };
        });
      };
      const mobileImage = await imageState(360);
      const desktopImage = await imageState(1280);
      if (!mobileImage.currentSrc || !desktopImage.currentSrc || mobileImage.currentSrc === desktopImage.currentSrc || mobileImage.sourceType !== "image/webp" || mobileImage.candidates < 3 || JSON.stringify(mobileImage.intrinsic) !== JSON.stringify(["1200", "900"])) {
        throw new Error("catalog did not select responsive Vitasil WebP renditions by viewport: " + JSON.stringify({ mobileImage, desktopImage }));
      }
      await page.setViewportSize({ width: 1280, height: 900 });
      console.log(JSON.stringify({ route, responsiveImage: { mobile: mobileImage, desktop: desktopImage } }));
    }
    console.log(JSON.stringify({ route, status: response.status(), features: rendered.summaries.map(item => item.feature), fileInputs: rendered.fileInputs, responsive }));
  }

  const adminBase = process.env.VITASIL_ADMIN_BASE_URL;
  await context.addInitScript(({ token }) => localStorage.setItem("we_admin_token", token), { token: process.env.VITASIL_ADMIN_TOKEN });
  await page.goto(adminBase + "/admin/site/data-model?snapshot=" + encodeURIComponent(process.env.VITASIL_SNAPSHOT_ID), { waitUntil: "domcontentloaded" });
  await page.getByRole("heading", { name: "Модель данных", exact: true }).waitFor({ timeout: 20000 });
  let adminState = await page.evaluate(async ({ token, snapshotID, branchID }) => {
    const headers = { Authorization: "Bearer " + token };
    const [snapshotResponse, branchesResponse] = await Promise.all([
      fetch("/api/v2/admin/site/snapshots/" + encodeURIComponent(snapshotID), { headers }),
      fetch("/api/v2/admin/branches", { headers }),
    ]);
    if (!snapshotResponse.ok || !branchesResponse.ok) throw new Error("admin snapshot/branch request failed: " + snapshotResponse.status + "/" + branchesResponse.status);
    const [snapshot, branches] = await Promise.all([snapshotResponse.json(), branchesResponse.json()]);
    const assignment = branches.items?.find(item => item.id === branchID)?.snapshotId;
    const config = snapshot.payload?.config;
    return {
      featureKeys: config?.features?.map(item => item.featureKey).sort() || [],
      placementKeys: config?.pages?.flatMap(page => page.placements || []).map(item => item.build?.featureKey).sort() || [],
      assignment,
      fileInputs: document.querySelectorAll('input[type="file"]').length,
      branchControls: [...document.querySelectorAll('[role="combobox"]')].filter(el => /ветк/i.test(el.getAttribute('aria-label') || '')).length,
      branchAssignments: [...document.querySelectorAll('button')].filter(el => /назначить ветке/i.test(el.textContent || '')).length,
    };
  }, { token: process.env.VITASIL_ADMIN_TOKEN, snapshotID: process.env.VITASIL_SNAPSHOT_ID, branchID: process.env.VITASIL_BRANCH_ID });
  if (JSON.stringify(adminState.featureKeys) !== JSON.stringify(["background", "catalog", "footer", "form", "header", "hero", "main-data", "not-found"].sort())) throw new Error("snapshot admin view omitted a current selected Vitasil feature: " + JSON.stringify(adminState));
  if (adminState.assignment !== process.env.VITASIL_SNAPSHOT_ID || adminState.fileInputs || adminState.branchControls || adminState.branchAssignments) throw new Error("site editor exposed upload/branch controls or changed branch assignment: " + JSON.stringify(adminState));

  await page.goto(adminBase + "/admin/content?snapshot=" + encodeURIComponent(process.env.VITASIL_SNAPSHOT_ID), { waitUntil: "domcontentloaded" });
  await page.getByText("Записи размещений", { exact: true }).waitFor({ timeout: 20000 });
  const contentState = await page.evaluate(() => ({
    fileInputs: document.querySelectorAll('input[type="file"]').length,
    branchControls: [...document.querySelectorAll('[role="combobox"]')].filter(el => /ветк/i.test(el.getAttribute('aria-label') || '')).length,
    branchAssignments: [...document.querySelectorAll('button')].filter(el => /назначить ветке/i.test(el.textContent || '')).length,
  }));
  if (contentState.fileInputs || contentState.branchControls || contentState.branchAssignments) throw new Error("content editor exposed upload or branch-assignment controls: " + JSON.stringify(contentState));
  const branchAfter = await page.evaluate(async ({ token, branchID }) => {
    const response = await fetch("/api/v2/admin/branches", { headers: { Authorization: "Bearer " + token } });
    if (!response.ok) throw new Error("final branch assignment request failed: " + response.status);
    const result = await response.json();
    return result.items?.find(item => item.id === branchID)?.snapshotId;
  }, { token: process.env.VITASIL_ADMIN_TOKEN, branchID: process.env.VITASIL_BRANCH_ID });
  if (branchAfter !== process.env.VITASIL_SNAPSHOT_ID) throw new Error("site/content browsing changed the snapshot-to-branch assignment");
  if (errors.length) throw new Error("browser errors: " + errors.join(" | "));
  console.log(JSON.stringify({ adminFeatures: adminState.featureKeys, adminState, contentState, branchAfter, errors }));
  await context.close();
} finally { await browser.close(); }`
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, node, "--input-type=module", "-e", script)
	command.Env = append(os.Environ(),
		"PLAYWRIGHT_TEST_MODULE="+module,
		"PLAYWRIGHT_TEST_BROWSER="+browser,
		"VITASIL_PUBLIC_BASE_URL="+strings.TrimRight(env.devServer.URL, "/"),
		"VITASIL_ADMIN_BASE_URL="+strings.TrimRight(env.server.URL, "/"),
		"VITASIL_ADMIN_TOKEN="+env.adminToken,
		"VITASIL_SNAPSHOT_ID="+snapshotID,
		"VITASIL_BRANCH_ID="+branchID,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("all current Vitasil placement browser acceptance failed: %v\n%s", err, output)
	}
	t.Logf("all current Vitasil placement browser metrics:\n%s", output)
}

func requireAllPlacementsBrowserDatabase(t *testing.T) {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	parsed, err := url.Parse(raw)
	if raw == "" || err != nil {
		t.Skip("set TEST_DATABASE_URL to the uniquely named local website_engine_vitasil_all_browser_qa_* disposable database")
	}
	databaseName := strings.TrimPrefix(parsed.Path, "/")
	if !strings.HasPrefix(databaseName, "website_engine_vitasil_all_browser_qa_") || (parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "localhost") {
		t.Skip("all-placement Vitasil browser acceptance requires a local website_engine_vitasil_all_browser_qa_* database; refusing any other target")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, err := pgx.Connect(ctx, raw)
	if err != nil {
		t.Fatalf("connect to disposable Vitasil all-placement database: %v", err)
	}
	defer connection.Close(context.Background())
	var currentDatabase string
	var sessions int
	if err := connection.QueryRow(ctx, `SELECT current_database(), (SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid())`).Scan(&currentDatabase, &sessions); err != nil {
		t.Fatalf("verify disposable Vitasil all-placement database: %v", err)
	}
	if currentDatabase != databaseName {
		t.Fatalf("connected database %q differs from requested QA database %q", currentDatabase, databaseName)
	}
	if sessions != 0 {
		t.Fatalf("refusing integration setup: disposable database %q already has %d other active sessions", databaseName, sessions)
	}
	t.Logf("verified disposable database %s has no other sessions before integration setup", currentDatabase)
}
