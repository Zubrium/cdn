package integration

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"

	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"

	"websiteengine/features/sitebuilder"
)

type externalFeatureManifest struct {
	Key     string `json:"key"`
	Version string `json:"version"`
}

func compareExternalFeatureVersions(left, right string) int {
	parse := func(value string) ([3]uint64, string, bool) {
		value = strings.SplitN(value, "+", 2)[0]
		parts := strings.SplitN(value, "-", 2)
		core, prerelease := parts[0], ""
		if len(parts) == 2 {
			prerelease = parts[1]
		}
		segments := strings.Split(core, ".")
		if len(segments) != 3 {
			return [3]uint64{}, "", false
		}
		var numbers [3]uint64
		for index, segment := range segments {
			number, err := strconv.ParseUint(segment, 10, 64)
			if err != nil {
				return [3]uint64{}, "", false
			}
			numbers[index] = number
		}
		return numbers, prerelease, true
	}
	leftNumbers, leftPrerelease, leftOK := parse(left)
	rightNumbers, rightPrerelease, rightOK := parse(right)
	if !leftOK || !rightOK {
		return strings.Compare(left, right)
	}
	for index := range leftNumbers {
		if leftNumbers[index] < rightNumbers[index] {
			return -1
		}
		if leftNumbers[index] > rightNumbers[index] {
			return 1
		}
	}
	if leftPrerelease == rightPrerelease {
		return 0
	}
	if leftPrerelease == "" {
		return 1
	}
	if rightPrerelease == "" {
		return -1
	}
	leftIDs, rightIDs := strings.Split(leftPrerelease, "."), strings.Split(rightPrerelease, ".")
	for index := 0; index < len(leftIDs) && index < len(rightIDs); index++ {
		leftNumber, leftErr := strconv.ParseUint(leftIDs[index], 10, 64)
		rightNumber, rightErr := strconv.ParseUint(rightIDs[index], 10, 64)
		switch {
		case leftErr == nil && rightErr == nil && leftNumber < rightNumber:
			return -1
		case leftErr == nil && rightErr == nil && leftNumber > rightNumber:
			return 1
		case leftErr == nil && rightErr != nil:
			return -1
		case leftErr != nil && rightErr == nil:
			return 1
		case leftErr != nil && rightErr != nil:
			if comparison := strings.Compare(leftIDs[index], rightIDs[index]); comparison != 0 {
				return comparison
			}
		}
	}
	if len(leftIDs) < len(rightIDs) {
		return -1
	}
	if len(leftIDs) > len(rightIDs) {
		return 1
	}
	return 0
}

func TestCompareExternalFeatureVersions(t *testing.T) {
	for _, test := range []struct {
		left, right string
		want        int
	}{
		{left: "1.0.10", right: "1.0.9", want: 1},
		{left: "1.0.5", right: "1.0.5-rc.2", want: 1},
		{left: "1.0.5-rc.10", right: "1.0.5-rc.2", want: 1},
		{left: "1.0.5+build.1", right: "1.0.5+build.2", want: 0},
	} {
		if got := compareExternalFeatureVersions(test.left, test.right); got != test.want {
			t.Errorf("compareExternalFeatureVersions(%q, %q) = %d, want %d", test.left, test.right, got, test.want)
		}
	}
}

func readExternalFeatureArchive(t *testing.T, dir, key string) ([]byte, string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var selectedPath, selectedVersion string
	prefix := key + "-"
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), prefix) || !strings.HasSuffix(entry.Name(), ".zip") {
			continue
		}
		versionPart := strings.TrimSuffix(strings.TrimPrefix(entry.Name(), prefix), ".zip")
		if len(versionPart) > 0 && versionPart[0] >= '0' && versionPart[0] <= '9' {
			if selectedPath == "" || compareExternalFeatureVersions(versionPart, selectedVersion) > 0 {
				selectedPath = filepath.Join(dir, entry.Name())
				selectedVersion = versionPart
			}
		}
	}
	if selectedPath == "" {
		t.Fatalf("no %s feature archive found in %s", key, dir)
	}
	archive, err := os.ReadFile(selectedPath)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatalf("open %s archive: %v", key, err)
	}
	for _, file := range reader.File {
		if file.Name != "manifest.json" {
			continue
		}
		manifestFile, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		defer manifestFile.Close()
		var manifest externalFeatureManifest
		if err := json.NewDecoder(manifestFile).Decode(&manifest); err != nil {
			t.Fatalf("decode %s manifest: %v", key, err)
		}
		if manifest.Key != key || manifest.Version == "" {
			t.Fatalf("unexpected %s archive manifest: %+v", key, manifest)
		}
		if manifest.Version != selectedVersion {
			t.Fatalf("%s archive filename version %s differs from manifest version %s", key, selectedVersion, manifest.Version)
		}
		return archive, manifest.Version
	}
	t.Fatalf("%s archive is missing manifest.json", key)
	return nil, ""
}

func externalFeatureArchiveExists(t *testing.T, dir, key string) bool {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), key+"-") && strings.HasSuffix(entry.Name(), ".zip") {
			return true
		}
	}
	return false
}

func TestExternalVitasilFeatureBuildsRegister(t *testing.T) {
	dir := os.Getenv("TEST_FEATURE_DIR")
	if dir == "" {
		t.Skip("TEST_FEATURE_DIR is not set")
	}
	env := newTestEnv(t)
	expected := []string{"background", "catalog", "footer", "form", "header", "hero", "legal-documents", "main-data", "not-found"}
	for _, key := range expected {
		t.Run(key, func(t *testing.T) {
			archive, version := readExternalFeatureArchive(t, dir, key)
			if os.Getenv("EXPECT_STATIC_FEATURES") == "1" {
				verified, err := sitebuilder.VerifyArchive(archive, int64(len(archive)))
				if err != nil {
					t.Fatal(err)
				}
				if verified.Manifest.RuntimeABI() != sitebuilder.RuntimeABIStaticModule {
					t.Fatalf("%s@%s uses runtime ABI %q, want %q", key, version, verified.Manifest.RuntimeABI(), sitebuilder.RuntimeABIStaticModule)
				}
				if len(verified.Manifest.RuntimeSharedStyles()) == 0 {
					t.Fatalf("%s@%s does not declare its shared core stylesheet", key, version)
				}
			}
			status, build, _ := env.rawRequest(http.MethodPost, "/api/v2/admin/feature-builds", env.adminToken, bytes.NewReader(archive), "application/zip")
			if status != http.StatusCreated && status != http.StatusOK {
				t.Fatalf("register %s@%s: status=%d body=%v", key, version, status, build)
			}
			if build["key"] != key || build["version"] != version {
				t.Fatalf("registered identity differs from manifest: %v", build)
			}
			featureID := build["featureId"].(string)
			var registeredKey string
			if err := env.pool.QueryRow(context.Background(), `SELECT key FROM features WHERE id=$1`, featureID).Scan(&registeredKey); err != nil {
				t.Fatal(err)
			}
			if registeredKey != key {
				t.Fatalf("feature build registered as %q, want %q", registeredKey, key)
			}
			var pointerColumns int
			if err := env.pool.QueryRow(context.Background(), `SELECT count(*) FROM information_schema.columns WHERE table_schema='public' AND table_name IN ('features','feature_entries') AND column_name IN ('draft_version_id','dev_version_id','prod_version_id')`).Scan(&pointerColumns); err != nil {
				t.Fatal(err)
			}
			if pointerColumns != 0 {
				t.Fatalf("clean schema contains %d global feature/content pointer columns", pointerColumns)
			}
		})
	}
}

func TestExternalVitasilDesignTokensFollowSnapshotBuildSelection(t *testing.T) {
	dir := os.Getenv("TEST_FEATURE_DIR")
	if dir == "" {
		t.Skip("TEST_FEATURE_DIR is not set")
	}
	env := newTestEnv(t)
	keys := []string{"background", "catalog", "footer", "form", "header", "hero", "legal-documents", "main-data", "not-found"}
	features := make([]any, 0, len(keys))
	versions := make(map[string]string, len(keys))
	manifests := make([]sitebuilder.Manifest, 0, len(keys))
	for _, key := range keys {
		archive, version := readExternalFeatureArchive(t, dir, key)
		status, build, _ := env.rawRequest(http.MethodPost, "/api/v2/admin/feature-builds", env.adminToken, bytes.NewReader(archive), "application/zip")
		if status != http.StatusCreated && status != http.StatusOK {
			t.Fatalf("register %s@%s: status=%d body=%v", key, version, status, build)
		}
		versions[key] = version
		manifests = append(manifests, modelAcceptanceManifestFromArchive(t, archive))
		features = append(features, map[string]any{"featureKey": key, "version": version})
	}
	config := singlePageConfig("header", versions["header"])
	page := config["pages"].([]any)[0].(map[string]any)
	page["placements"].([]any)[0].(map[string]any)["slot"] = "header"
	config["features"] = features
	config = modelAcceptanceBindManifestInputs(t, config, manifests)
	snapshot := saveAndDeploySnapshot(t, env, config, "Vitasil design tokens")
	status, workspace, _ := env.request(http.MethodGet, "/api/v2/admin/site/snapshots/"+snapshot["id"].(string)+"/design-system", env.adminToken, nil)
	if status != http.StatusOK {
		t.Fatalf("read snapshot design tokens: status=%d body=%v", status, workspace)
	}
	definitions, ok := workspace["featureTokens"].([]any)
	if !ok || len(definitions) == 0 {
		t.Fatalf("snapshot should expose tokens declared by selected Vitasil builds: %v", workspace)
	}
	counts := make(map[string]int, len(keys))
	headerFocusFound := false
	for _, raw := range definitions {
		definition, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("unexpected feature token: %T", raw)
		}
		key, _ := definition["featureKey"].(string)
		counts[key]++
		if key == "header" {
			token, _ := definition["definition"].(map[string]any)
			if token["key"] == "color-focus" {
				headerFocusFound = true
			}
		}
	}
	for _, key := range keys {
		if counts[key] == 0 {
			t.Errorf("snapshot did not expose %s design tokens: counts=%v", key, counts)
		}
	}
	if counts["unselected-fixture"] != 0 || len(counts) != len(keys) {
		t.Fatalf("snapshot exposed tokens outside its selected feature set: %v", counts)
	}
	if !headerFocusFound {
		t.Fatal("current Vitasil header build does not expose its color-focus token")
	}
	t.Run("published browser applies Vitasil header token overrides", func(t *testing.T) {
		if os.Getenv("PRERENDER_TEST_NODE") == "" || os.Getenv("PRERENDER_TEST_MODULE") == "" || os.Getenv("PRERENDER_TEST_BROWSER") == "" {
			t.Skip("set PRERENDER_TEST_NODE/MODULE/BROWSER for browser-computed design token acceptance")
		}
		parentID, err := uuid.Parse(snapshot["id"].(string))
		if err != nil {
			t.Fatal(err)
		}
		baseID := uuid.NewString()
		design := sitebuilder.NewDesignSystem()
		design.Groups = []sitebuilder.TokenGroup{{ID: uuid.NewString(), Name: "QA brand"}}
		design.BaseTokens = []sitebuilder.BaseToken{{ID: baseID, Name: "Focus", GroupID: design.Groups[0].ID, Kind: "color", Value: sitebuilder.TokenValue{Color: &sitebuilder.TokenColor{Hex: "#123456", Opacity: 100}}}}
		design.FeatureValues = map[string]sitebuilder.TokenValue{"header/color-focus": {BaseTokenID: baseID}}
		design.Breakpoints = []sitebuilder.TokenBreakpoint{{ID: uuid.NewString(), Name: "Desktop", MinWidthPx: 1024, BaseValues: map[string]sitebuilder.TokenValue{baseID: {Color: &sitebuilder.TokenColor{Hex: "#abcdef", Opacity: 100}}}, FeatureValues: map[string]sitebuilder.TokenValue{}}}
		actor := testActor(t, env)
		child, err := env.snapshots.CreateSnapshot(context.Background(), actor, sitebuilder.CreateSnapshotInput{Name: "Vitasil responsive tokens", ParentID: &parentID, DesignSystem: &design})
		if err != nil {
			t.Fatalf("create child snapshot with Vitasil design tokens: %v", err)
		}
		branchID, err := uuid.Parse(string(env.devBranch))
		if err != nil {
			t.Fatal(err)
		}
		branch, err := env.snapshots.Branch(context.Background(), branchID)
		if err != nil {
			t.Fatal(err)
		}
		branch, err = env.snapshots.AssignBranch(context.Background(), actor, branchID, child.ID, branch.Revision)
		if err != nil {
			t.Fatalf("explicitly assign Vitasil child to disposable branch: %v", err)
		}
		assertResponsiveDesignTokensInBrowser(t, env, branch, baseID, "--feature-header-color-focus", []designTokenBrowserCase{
			{Width: 500, Color: "rgb(18, 52, 86)"},
			{Width: 1280, Color: "rgb(171, 205, 239)"},
		})
	})
}

// This opt-in test uses a real feature archive supplied by the caller. The
// regular integration suite remains independent of any particular site.
