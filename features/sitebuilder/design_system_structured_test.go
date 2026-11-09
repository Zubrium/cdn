package sitebuilder

import (
	"encoding/json"
	"github.com/google/uuid"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func tokenNumber(n float64) *float64 { return &n }
func structuredTestBuild(t *testing.T, key string, defs []FeatureTokenDefinition) PinnedBuild {
	t.Helper()
	raw, err := json.Marshal(Manifest{
		FormatVersion: 2,
		Key:           key,
		Name:          key,
		Version:       "1.0.0",
		EngineAPI:     "2",
		Entry:         "entry.js",
		Slots:         []string{"main"},
		Arguments:     json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		DataInputs:    []DataInputPort{},
		DesignTokens:  defs,
	})
	if err != nil {
		t.Fatal(err)
	}
	return PinnedBuild{Key: key, Name: key, Manifest: raw}
}

func compareFeatureArchiveVersions(left, right string) int {
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

func TestCompareFeatureArchiveVersions(t *testing.T) {
	for _, test := range []struct {
		left, right string
		want        int
	}{
		{left: "1.0.10", right: "1.0.9", want: 1},
		{left: "1.0.5", right: "1.0.5-rc.2", want: 1},
		{left: "1.0.5-rc.10", right: "1.0.5-rc.2", want: 1},
		{left: "1.0.5+build.1", right: "1.0.5+build.2", want: 0},
	} {
		if got := compareFeatureArchiveVersions(test.left, test.right); got != test.want {
			t.Errorf("compareFeatureArchiveVersions(%q, %q) = %d, want %d", test.left, test.right, got, test.want)
		}
	}
}

func TestStructuredThemeUsesPinnedFeaturesAndResponsiveBaseLinks(t *testing.T) {
	groupID, baseID := uuid.NewString(), uuid.NewString()
	theme := NewDesignSystem()
	theme.Groups = []TokenGroup{{ID: groupID, Name: "Layout"}}
	theme.BaseTokens = []BaseToken{{ID: baseID, Name: "Space", GroupID: groupID, Kind: "length", Value: TokenValue{Number: tokenNumber(12), Unit: "px"}}}
	theme.FeatureValues = map[string]TokenValue{"hero/gap": {BaseTokenID: baseID}}
	theme.Breakpoints = []TokenBreakpoint{{ID: uuid.NewString(), Name: "Wide", MinWidthPx: 1200, BaseValues: map[string]TokenValue{baseID: {Number: tokenNumber(48), Unit: "px"}}}, {ID: uuid.NewString(), Name: "Tablet", MinWidthPx: 700, BaseValues: map[string]TokenValue{baseID: {Number: tokenNumber(24), Unit: "px"}}}}
	build := structuredTestBuild(t, "hero", []FeatureTokenDefinition{{Key: "gap", Label: "Gap", Group: "Layout", Kind: "length", Default: TokenValue{Number: tokenNumber(8), Unit: "px"}}})
	css, err := DesignSystemCSSForBuilds(theme, []PinnedBuild{build}, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(css, "--feature-hero-gap:var(--base-"+baseID+")") {
		t.Fatalf("missing responsive reference: %s", css)
	}
	if strings.Index(css, "min-width:700px") > strings.Index(css, "min-width:1200px") {
		t.Fatalf("breakpoints not ascending: %s", css)
	}
	if theme.Breakpoints[0].MinWidthPx != 1200 {
		t.Fatal("normalizing mutated input")
	}
	if _, err := NormalizeSnapshotDesignSystem(theme, nil); err == nil {
		t.Fatal("token from absent feature accepted")
	}
	theme.BaseTokens[0].Kind = "number"
	theme.BaseTokens[0].Value = TokenValue{Number: tokenNumber(12)}
	if _, err := NormalizeSnapshotDesignSystem(theme, []PinnedBuild{build}); err == nil {
		t.Fatal("incompatible reference accepted")
	}
}

func TestStructuredThemeCSSKeepsResponsiveCascadeAndBaseLinks(t *testing.T) {
	groupID, baseID := uuid.NewString(), uuid.NewString()
	defaultFeatureValue := TokenValue{Number: tokenNumber(4), Unit: "px"}
	theme := NewDesignSystem()
	theme.Groups = []TokenGroup{{ID: groupID, Name: "Layout"}}
	theme.BaseTokens = []BaseToken{{ID: baseID, Name: "Space", GroupID: groupID, Kind: "length", Value: TokenValue{Number: tokenNumber(8), Unit: "px"}}}
	theme.FeatureValues = map[string]TokenValue{"hero/gap": {BaseTokenID: baseID}}
	// Deliberately provide breakpoints out of order. The 1024 px point has no
	// overrides, so CSS must inherit the 600 px feature value while the linked
	// feature token continues to resolve through the responsive base token.
	theme.Breakpoints = []TokenBreakpoint{
		{ID: uuid.NewString(), Name: "Wide", MinWidthPx: 1440, BaseValues: map[string]TokenValue{baseID: {Number: tokenNumber(40), Unit: "px"}}},
		{ID: uuid.NewString(), Name: "Medium", MinWidthPx: 1024},
		{ID: uuid.NewString(), Name: "Tablet", MinWidthPx: 600, BaseValues: map[string]TokenValue{baseID: {Number: tokenNumber(16), Unit: "px"}}, FeatureValues: map[string]TokenValue{"hero/inherited": {Number: tokenNumber(12), Unit: "px"}}},
	}
	build := structuredTestBuild(t, "hero", []FeatureTokenDefinition{
		{Key: "gap", Label: "Gap", Group: "Layout", Kind: "length", Default: defaultFeatureValue},
		{Key: "inherited", Label: "Inherited", Group: "Layout", Kind: "length", Default: defaultFeatureValue},
	})

	css, err := DesignSystemCSSForBuilds(theme, []PinnedBuild{build}, "")
	if err != nil {
		t.Fatal(err)
	}
	want := ":root{--base-" + baseID + ":8px;--feature-hero-gap:var(--base-" + baseID + ");--feature-hero-inherited:4px;}" +
		"@media(min-width:600px){:root{--base-" + baseID + ":16px;--feature-hero-inherited:12px;}}" +
		"@media(min-width:1024px){:root{}}" +
		"@media(min-width:1440px){:root{--base-" + baseID + ":40px;}}"
	if css != want {
		t.Fatalf("unexpected responsive CSS\n got: %s\nwant: %s", css, want)
	}
}

func TestStructuredThemeRejectsInvalidValuesAndDanglingDependencies(t *testing.T) {
	groupID, baseID := uuid.NewString(), uuid.NewString()
	theme := NewDesignSystem()
	theme.Groups = []TokenGroup{{ID: groupID, Name: "Colors"}}
	theme.BaseTokens = []BaseToken{{ID: baseID, Name: "Text", GroupID: groupID, Kind: "color", Value: TokenValue{Color: &TokenColor{Hex: "#ffffff", Opacity: 100}}}}
	invalid := []TokenValue{{Color: &TokenColor{Hex: "red;display:none", Opacity: 100}}, {Color: &TokenColor{Hex: "#ffffff", Opacity: 101}}, {Color: &TokenColor{Hex: "#ffffff", Opacity: 100}, Number: tokenNumber(1)}, {BaseTokenID: baseID}, {Font: "url(evil)"}}
	for _, value := range invalid {
		copy := theme
		copy.BaseTokens = append([]BaseToken(nil), theme.BaseTokens...)
		copy.BaseTokens[0].Value = value
		if _, err := NormalizeSnapshotDesignSystem(copy, nil); err == nil {
			t.Fatalf("accepted invalid value: %+v", value)
		}
	}
	theme.Breakpoints = []TokenBreakpoint{{ID: uuid.NewString(), Name: "One", MinWidthPx: 600}, {ID: uuid.NewString(), Name: "Two", MinWidthPx: 600}}
	if _, err := NormalizeSnapshotDesignSystem(theme, nil); err == nil {
		t.Fatal("duplicate width accepted")
	}
	theme.Breakpoints = nil
	theme.Groups = nil
	if _, err := NormalizeSnapshotDesignSystem(theme, nil); err == nil {
		t.Fatal("missing group accepted")
	}
}
func TestNewDesignSystemIsGenericAndEmpty(t *testing.T) {
	value := NewDesignSystem()
	if value.FormatVersion != 2 || len(value.BaseTokens) != 0 || len(value.Tokens) != 0 || len(value.FontRoles) != 0 {
		t.Fatalf("new theme contains website values: %+v", value)
	}
	css, err := DesignSystemCSSForBuilds(value, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if css != ":root{}" {
		t.Fatalf("non-generic CSS %q", css)
	}
}

func TestNewDesignSystemJSONIncludesEmptyCollections(t *testing.T) {
	raw, err := json.Marshal(NewDesignSystem())
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]json.RawMessage
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"groups", "baseTokens", "breakpoints", "fonts"} {
		var items []json.RawMessage
		if err := json.Unmarshal(value[key], &items); err != nil || items == nil || len(items) != 0 {
			t.Errorf("%s must serialize as an empty array, got %s (error: %v)", key, value[key], err)
		}
	}
	var featureValues map[string]json.RawMessage
	if err := json.Unmarshal(value["featureValues"], &featureValues); err != nil || featureValues == nil || len(featureValues) != 0 {
		t.Errorf("featureValues must serialize as an empty object, got %s (error: %v)", value["featureValues"], err)
	}
}

func TestManifestDesignTokenDefinitionsAreTypedAndSafe(t *testing.T) {
	for _, definition := range []FeatureTokenDefinition{{Key: "unsafe;}body", Label: "X", Group: "Y", Kind: "number", Default: TokenValue{Number: tokenNumber(1)}}, {Key: "safe", Label: "X", Group: "Y", Kind: "length", Default: TokenValue{Number: tokenNumber(1), Unit: "url(evil)"}}, {Key: "safe", Label: "X", Group: "Y", Kind: "number", Default: TokenValue{BaseTokenID: uuid.NewString()}}} {
		if err := ValidateFeatureTokens([]FeatureTokenDefinition{definition}); err == nil {
			t.Fatalf("unsafe definition accepted: %+v", definition)
		}
	}
}

// Opt-in source integration: validates actual ZIP contracts without touching a running API.
func TestExternalFeatureDesignArchives(t *testing.T) {
	dir := os.Getenv("TEST_FEATURE_DIR")
	if dir == "" {
		t.Skip("TEST_FEATURE_DIR is not set")
	}
	paths, err := filepath.Glob(filepath.Join(dir, "*.zip"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no feature ZIPs")
	}
	buildsByKey := map[string]PinnedBuild{}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			archive, err := VerifyArchive(raw, 8<<20)
			if err != nil {
				t.Fatal(err)
			}
			if len(archive.Manifest.DesignTokens) == 0 {
				t.Fatal("real feature has no design contract")
			}
			build := PinnedBuild{Key: archive.Manifest.Key, Name: archive.Manifest.Name, Version: archive.Manifest.Version, Manifest: archive.ManifestRaw}
			current, exists := buildsByKey[build.Key]
			if !exists || compareFeatureArchiveVersions(build.Version, current.Version) > 0 {
				buildsByKey[build.Key] = build
			}
		})
	}
	keys := make([]string, 0, len(buildsByKey))
	for key := range buildsByKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	builds := make([]PinnedBuild, 0, len(keys))
	for _, key := range keys {
		builds = append(builds, buildsByKey[key])
	}
	css, err := DesignSystemCSSForBuilds(NewDesignSystem(), builds, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, build := range builds {
		if !strings.Contains(css, "--feature-"+build.Key+"-") {
			t.Fatalf("missing runtime CSS for %s", build.Key)
		}
	}
	t.Logf("verified %d real ZIP contracts and combined runtime CSS", len(builds))
}

func TestFeatureThemeReconciliationPreservesAndRestoresOverrides(t *testing.T) {
	groupID, baseID := uuid.NewString(), uuid.NewString()
	previous := NewDesignSystem()
	previous.Groups = []TokenGroup{{ID: groupID, Name: "Layout"}}
	previous.BaseTokens = []BaseToken{{ID: baseID, Name: "Spacing", GroupID: groupID, Kind: "length", Value: TokenValue{Number: tokenNumber(12), Unit: "px"}}}
	previous.FeatureValues = map[string]TokenValue{"hero/gap": {BaseTokenID: baseID}, "card/color": {Color: &TokenColor{Hex: "#abcdef", Opacity: 75}}}
	previous.Breakpoints = []TokenBreakpoint{{ID: uuid.NewString(), Name: "Wide", MinWidthPx: 900, BaseValues: map[string]TokenValue{}, FeatureValues: map[string]TokenValue{"hero/gap": {Number: tokenNumber(20), Unit: "px"}}}}
	oldHero := structuredTestBuild(t, "hero", []FeatureTokenDefinition{{Key: "gap", Label: "Gap", Group: "Layout", Kind: "length", Default: TokenValue{Number: tokenNumber(8), Unit: "px"}}})
	oldHero.Version = "1.0.0"
	oldCard := structuredTestBuild(t, "card", []FeatureTokenDefinition{{Key: "color", Label: "Color", Group: "Colors", Kind: "color", Default: TokenValue{Color: &TokenColor{Hex: "#000000", Opacity: 100}}}})
	oldCard.Version = "1.0.0"
	newHero := structuredTestBuild(t, "hero", []FeatureTokenDefinition{{Key: "gap", Label: "Gap", Group: "Layout", Kind: "color", Default: TokenValue{Color: &TokenColor{Hex: "#ff0000", Opacity: 100}}}})
	newHero.Version = "2.0.0"
	migrated, warnings, err := ReconcileSnapshotDesignSystem(previous, []PinnedBuild{oldHero, oldCard}, []PinnedBuild{newHero})
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 3 || len(migrated.FeatureValues) != 0 || len(migrated.LegacyFeatureValues) != 2 || len(migrated.Breakpoints[0].LegacyFeatureValues) != 1 {
		t.Fatalf("overrides were lost: %+v %+v", migrated, warnings)
	}
	if len(previous.FeatureValues) != 2 || len(previous.Breakpoints[0].FeatureValues) != 1 || len(previous.LegacyFeatureValues) != 0 {
		t.Fatal("parent theme was changed")
	}
	css, err := DesignSystemCSSForBuilds(migrated, []PinnedBuild{newHero}, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(css, "abcdef") || strings.Contains(css, "20px") || !strings.Contains(css, "255 0 0") {
		t.Fatalf("inert values leaked to runtime: %s", css)
	}
	// On a later rollback of the feature build, all compatible values return.
	restored, warnings, err := ReconcileSnapshotDesignSystem(migrated, []PinnedBuild{newHero}, []PinnedBuild{oldHero, oldCard})
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 3 || len(restored.LegacyFeatureValues) != 0 || len(restored.Breakpoints[0].LegacyFeatureValues) != 0 || restored.FeatureValues["hero/gap"].BaseTokenID != baseID || restored.FeatureValues["card/color"].Color.Hex != "#abcdef" || *restored.Breakpoints[0].FeatureValues["hero/gap"].Number != 20 {
		t.Fatalf("compatible overrides not restored: %+v %+v", restored, warnings)
	}
}

func TestRetainedOverrideDoesNotReplaceNewerValueAndSurvivesMissingBase(t *testing.T) {
	groupID, baseID := uuid.NewString(), uuid.NewString()
	old := structuredTestBuild(t, "hero", []FeatureTokenDefinition{{Key: "gap", Label: "Gap", Group: "Layout", Kind: "length", Default: TokenValue{Number: tokenNumber(4), Unit: "px"}}})
	old.Version = "1.0.0"
	theme := NewDesignSystem()
	theme.Groups = []TokenGroup{{ID: groupID, Name: "Layout"}}
	theme.BaseTokens = []BaseToken{{ID: baseID, Name: "Gap", GroupID: groupID, Kind: "length", Value: TokenValue{Number: tokenNumber(12), Unit: "px"}}}
	theme.FeatureValues = map[string]TokenValue{"hero/gap": {BaseTokenID: baseID}}
	removed, _, err := ReconcileSnapshotDesignSystem(theme, []PinnedBuild{old}, nil)
	if err != nil {
		t.Fatal(err)
	}
	removed.BaseTokens = nil
	returned, _, err := ReconcileSnapshotDesignSystem(removed, nil, []PinnedBuild{old})
	if err != nil {
		t.Fatal(err)
	}
	if len(returned.LegacyFeatureValues) != 1 || len(returned.FeatureValues) != 0 {
		t.Fatal("missing base reference should be retained, not used")
	}
	returned.BaseTokens = theme.BaseTokens
	returned.FeatureValues = map[string]TokenValue{"hero/gap": {Number: tokenNumber(99), Unit: "px"}}
	again, _, err := ReconcileSnapshotDesignSystem(returned, []PinnedBuild{old}, []PinnedBuild{old})
	if err != nil {
		t.Fatal(err)
	}
	if len(again.LegacyFeatureValues) != 1 || *again.FeatureValues["hero/gap"].Number != 99 {
		t.Fatal("retained value replaced newer override")
	}
}

func TestStructuredThemeRejectsNameAndCSSCollisions(t *testing.T) {
	theme := NewDesignSystem()
	a, b := uuid.NewString(), uuid.NewString()
	theme.Groups = []TokenGroup{{ID: a, Name: " Цвет "}, {ID: b, Name: "цвет"}}
	if _, err := NormalizeSnapshotDesignSystem(theme, nil); err == nil {
		t.Fatal("accepted ambiguous group names")
	}
	theme.Groups = theme.Groups[:1]
	theme.BaseTokens = []BaseToken{{ID: uuid.NewString(), GroupID: a, Name: "Gap", Kind: "number", Value: TokenValue{Number: tokenNumber(1)}}, {ID: uuid.NewString(), GroupID: a, Name: " gap ", Kind: "number", Value: TokenValue{Number: tokenNumber(2)}}}
	if _, err := NormalizeSnapshotDesignSystem(theme, nil); err == nil {
		t.Fatal("accepted duplicate token names in one group")
	}
	first := structuredTestBuild(t, "hero-a", []FeatureTokenDefinition{{Key: "b", Label: "B", Group: "Test", Kind: "number", Default: TokenValue{Number: tokenNumber(1)}}})
	second := structuredTestBuild(t, "hero", []FeatureTokenDefinition{{Key: "a-b", Label: "A", Group: "Test", Kind: "number", Default: TokenValue{Number: tokenNumber(2)}}})
	if _, err := SnapshotFeatureTokens([]PinnedBuild{first, second}); err == nil {
		t.Fatal("accepted colliding namespaced CSS variables")
	}
}
