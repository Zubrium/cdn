package sitebuilder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type TokenGroup struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type TokenColor struct {
	Hex     string  `json:"hex"`
	Opacity float64 `json:"opacity"`
}
type TokenShadow struct {
	X      float64    `json:"x"`
	Y      float64    `json:"y"`
	Blur   float64    `json:"blur"`
	Spread float64    `json:"spread"`
	Color  TokenColor `json:"color"`
	Inset  bool       `json:"inset"`
}
type TokenValue struct {
	Number      *float64     `json:"number,omitempty"`
	Unit        string       `json:"unit,omitempty"`
	Color       *TokenColor  `json:"color,omitempty"`
	Font        string       `json:"font,omitempty"`
	Shadow      *TokenShadow `json:"shadow,omitempty"`
	BaseTokenID string       `json:"baseTokenId,omitempty"`
}
type BaseToken struct {
	ID      string     `json:"id"`
	Name    string     `json:"name"`
	GroupID string     `json:"groupId"`
	Kind    string     `json:"kind"`
	Value   TokenValue `json:"value"`
}
type FeatureTokenDefinition struct {
	Key     string     `json:"key"`
	Label   string     `json:"label"`
	Group   string     `json:"group"`
	Kind    string     `json:"kind"`
	Default TokenValue `json:"default"`
}
type SnapshotFeatureToken struct {
	FeatureKey  string                 `json:"featureKey"`
	FeatureName string                 `json:"featureName"`
	Definition  FeatureTokenDefinition `json:"definition"`
}

// LegacyFeatureTokenValue preserves an override after a feature or token is
// removed, or after a token changes kind. It never becomes CSS until a future
// compatible build explicitly makes it active again.
type LegacyFeatureTokenValue struct {
	FeatureKey  string     `json:"featureKey"`
	FeatureName string     `json:"featureName"`
	TokenKey    string     `json:"tokenKey"`
	Label       string     `json:"label"`
	Kind        string     `json:"kind"`
	Version     string     `json:"version"`
	Value       TokenValue `json:"value"`
}

type TokenBreakpoint struct {
	ID                  string                    `json:"id"`
	Name                string                    `json:"name"`
	MinWidthPx          int                       `json:"minWidthPx"`
	BaseValues          map[string]TokenValue     `json:"baseValues"`
	FeatureValues       map[string]TokenValue     `json:"featureValues"`
	LegacyFeatureValues []LegacyFeatureTokenValue `json:"legacyFeatureValues,omitempty"`
}

var tokenKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,79}$`)
var tokenHexPattern = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func NewDesignSystem() DesignSystem {
	return DesignSystem{FormatVersion: 2, Groups: []TokenGroup{}, BaseTokens: []BaseToken{}, FeatureValues: map[string]TokenValue{}, Breakpoints: []TokenBreakpoint{}, Fonts: []DesignFont{}, FontRoles: map[string]string{}}
}

func finite(n float64) bool        { return !math.IsNaN(n) && !math.IsInf(n, 0) }
func validTokenName(s string) bool { return strings.TrimSpace(s) != "" && len([]rune(s)) <= 160 }
func validateTokenColor(c TokenColor) error {
	if !tokenHexPattern.MatchString(c.Hex) || !finite(c.Opacity) || c.Opacity < 0 || c.Opacity > 100 {
		return errors.New("color requires a six-digit HEX and opacity between 0 and 100")
	}
	return nil
}
func tokenValueCSS(kind string, v TokenValue, fonts map[string]bool) (string, error) {
	if v.BaseTokenID != "" {
		return "", errors.New("literal value cannot contain a base token reference")
	}
	populated := 0
	if v.Number != nil {
		populated++
	}
	if v.Color != nil {
		populated++
	}
	if v.Font != "" {
		populated++
	}
	if v.Shadow != nil {
		populated++
	}
	if populated != 1 {
		return "", errors.New("provide exactly one typed token value")
	}
	number := func(n float64) string { return strconv.FormatFloat(n, 'f', -1, 64) }
	switch kind {
	case "color":
		if v.Color == nil || v.Number != nil || v.Font != "" || v.Shadow != nil || v.Unit != "" {
			break
		}
		if err := validateTokenColor(*v.Color); err != nil {
			return "", err
		}
		rgb, _ := strconv.ParseUint(v.Color.Hex[1:], 16, 32)
		return fmt.Sprintf("rgb(%d %d %d / %s%%)", rgb>>16, (rgb>>8)&255, rgb&255, number(v.Color.Opacity)), nil
	case "length", "number":
		if v.Number == nil || !finite(*v.Number) {
			break
		}
		if kind == "number" {
			if v.Unit != "" {
				break
			}
			return number(*v.Number), nil
		}
		if !map[string]bool{"px": true, "rem": true, "em": true, "%": true, "vw": true, "vh": true, "svh": true, "ch": true}[v.Unit] {
			break
		}
		return number(*v.Number) + v.Unit, nil
	case "font":
		if v.Font == "" || v.Unit != "" || (!fonts[v.Font] && v.Font != "system-ui" && v.Font != "serif" && v.Font != "sans-serif" && v.Font != "monospace") {
			break
		}
		if !familyPattern.MatchString(v.Font) {
			break
		}
		if v.Font == "system-ui" || v.Font == "serif" || v.Font == "sans-serif" || v.Font == "monospace" {
			return v.Font, nil
		}
		return strconv.Quote(v.Font), nil
	case "shadow":
		if v.Shadow == nil || v.Unit != "" {
			break
		}
		s := v.Shadow
		if !finite(s.X) || !finite(s.Y) || !finite(s.Blur) || !finite(s.Spread) || s.Blur < 0 {
			break
		}
		if err := validateTokenColor(s.Color); err != nil {
			return "", err
		}
		color, _ := tokenValueCSS("color", TokenValue{Color: &s.Color}, fonts)
		prefix := ""
		if s.Inset {
			prefix = "inset "
		}
		return fmt.Sprintf("%s%spx %spx %spx %spx %s", prefix, number(s.X), number(s.Y), number(s.Blur), number(s.Spread), color), nil
	}
	return "", fmt.Errorf("invalid structured %s token value", kind)
}

func ValidateFeatureTokens(definitions []FeatureTokenDefinition) error {
	seen := map[string]bool{}
	for _, d := range definitions {
		if !tokenKeyPattern.MatchString(d.Key) || seen[d.Key] || !validTokenName(d.Label) || !validTokenName(d.Group) {
			return fmt.Errorf("invalid or duplicate feature design token %q", d.Key)
		}
		seen[d.Key] = true
		// Font defaults can use only system families; uploaded families belong to a snapshot.
		if _, err := tokenValueCSS(d.Kind, d.Default, nil); err != nil {
			return fmt.Errorf("design token %s: %w", d.Key, err)
		}
	}
	return nil
}
func SnapshotFeatureTokens(builds []PinnedBuild) ([]SnapshotFeatureToken, error) {
	result := []SnapshotFeatureToken{}
	cssNames := map[string]string{}
	for _, build := range builds {
		if !featureKeyPattern.MatchString(build.Key) {
			return nil, errors.New("invalid feature key for design tokens")
		}
		var manifest Manifest
		if err := json.Unmarshal(build.Manifest, &manifest); err != nil {
			return nil, err
		}
		if err := ValidateFeatureTokens(manifest.DesignTokens); err != nil {
			return nil, err
		}
		for _, d := range manifest.DesignTokens {
			key := build.Key + "/" + d.Key
			cssName := "--feature-" + build.Key + "-" + d.Key
			if previous, exists := cssNames[cssName]; exists {
				return nil, fmt.Errorf("feature design tokens %q and %q have the same CSS name", previous, key)
			}
			cssNames[cssName] = key
			result = append(result, SnapshotFeatureToken{FeatureKey: build.Key, FeatureName: build.Name, Definition: d})
		}
	}
	return result, nil
}
func NormalizeSnapshotDesignSystem(value DesignSystem, builds []PinnedBuild) (DesignSystem, error) {
	if value.FormatVersion == 1 {
		return NormalizeDesignSystem(value)
	}
	return normalizeStructuredDesign(value, builds, true)
}
func normalizeStructuredDesign(value DesignSystem, builds []PinnedBuild, checkFeatures bool) (DesignSystem, error) {
	if value.FormatVersion != 2 {
		return DesignSystem{}, errors.New("design system formatVersion must be 2")
	}
	if len(value.Tokens) > 0 || len(value.MobileTokens) > 0 || len(value.FontRoles) > 0 {
		return DesignSystem{}, errors.New("structured design system cannot contain CSS values or legacy font roles")
	}
	// Clone before sorting or normalizing maps so validation never mutates a caller's snapshot.
	raw, err := json.Marshal(value)
	if err != nil {
		return DesignSystem{}, err
	}
	var cloned DesignSystem
	if err = json.Unmarshal(raw, &cloned); err != nil {
		return DesignSystem{}, err
	}
	value = cloned
	if value.Groups == nil {
		value.Groups = []TokenGroup{}
	}
	if value.BaseTokens == nil {
		value.BaseTokens = []BaseToken{}
	}
	if value.FeatureValues == nil {
		value.FeatureValues = map[string]TokenValue{}
	}
	if value.Breakpoints == nil {
		value.Breakpoints = []TokenBreakpoint{}
	}
	if value.Fonts == nil {
		value.Fonts = []DesignFont{}
	}
	ids := map[string]bool{}
	register := func(id string) bool {
		if _, err := uuid.Parse(id); err != nil || id == uuid.Nil.String() || ids[id] {
			return false
		}
		ids[id] = true
		return true
	}
	groups := map[string]bool{}
	groupNames := map[string]bool{}
	for _, group := range value.Groups {
		name := strings.ToLower(strings.TrimSpace(group.Name))
		if !register(group.ID) || !validTokenName(group.Name) || groupNames[name] {
			return DesignSystem{}, errors.New("invalid or duplicate design group")
		}
		groups[group.ID] = true
		groupNames[name] = true
	}
	fonts := map[string]bool{}
	for _, font := range value.Fonts {
		if !familyPattern.MatchString(font.Family) || !weightPattern.MatchString(font.Weight) || (font.Style != "normal" && font.Style != "italic") || (font.UnicodeRange != "" && !unicodePattern.MatchString(font.UnicodeRange)) {
			return DesignSystem{}, errors.New("invalid font face")
		}
		if _, err := uuid.Parse(font.MediaID); err != nil {
			return DesignSystem{}, errors.New("font must reference a file")
		}
		face := font.Family + font.Weight + font.Style + font.UnicodeRange
		if fonts[face] {
			return DesignSystem{}, errors.New("duplicate font face")
		}
		fonts[face] = true
		fonts[font.Family] = true
	}
	if value.BrandLogoID != "" {
		if _, err := uuid.Parse(value.BrandLogoID); err != nil {
			return DesignSystem{}, errors.New("brand logo must reference a file")
		}
	}
	bases := map[string]BaseToken{}
	baseNames := map[string]bool{}
	for _, base := range value.BaseTokens {
		name := base.GroupID + "/" + strings.ToLower(strings.TrimSpace(base.Name))
		if !register(base.ID) || !validTokenName(base.Name) || !groups[base.GroupID] || baseNames[name] {
			return DesignSystem{}, errors.New("invalid base token or missing group")
		}
		if _, err := tokenValueCSS(base.Kind, base.Value, fonts); err != nil {
			return DesignSystem{}, fmt.Errorf("%s: %w", base.Name, err)
		}
		bases[base.ID] = base
		baseNames[name] = true
	}
	definitions, err := SnapshotFeatureTokens(builds)
	if err != nil {
		return DesignSystem{}, err
	}
	features := map[string]FeatureTokenDefinition{}
	for _, d := range definitions {
		features[d.FeatureKey+"/"+d.Definition.Key] = d.Definition
	}
	check := func(kind string, v TokenValue) error {
		if v.BaseTokenID != "" {
			base, ok := bases[v.BaseTokenID]
			if !ok || base.Kind != kind {
				return errors.New("base token reference is missing or has an incompatible type")
			}
			if v.Number != nil || v.Color != nil || v.Font != "" || v.Shadow != nil || v.Unit != "" {
				return errors.New("reference must not contain a literal")
			}
			return nil
		}
		_, err := tokenValueCSS(kind, v, fonts)
		return err
	}
	checkFeaturesMap := func(values map[string]TokenValue) error {
		for key, v := range values {
			definition, ok := features[key]
			if !ok {
				if checkFeatures {
					return fmt.Errorf("feature token %q is not in this snapshot", key)
				}
				continue
			}
			if err := check(definition.Kind, v); err != nil {
				return fmt.Errorf("%s: %w", definition.Label, err)
			}
		}
		return nil
	}
	if err := checkFeaturesMap(value.FeatureValues); err != nil {
		return DesignSystem{}, err
	}
	if err := validateLegacyFeatureValues(value.LegacyFeatureValues); err != nil {
		return DesignSystem{}, err
	}
	widths := map[int]bool{}
	for index := range value.Breakpoints {
		bp := &value.Breakpoints[index]
		if bp.BaseValues == nil {
			bp.BaseValues = map[string]TokenValue{}
		}
		if bp.FeatureValues == nil {
			bp.FeatureValues = map[string]TokenValue{}
		}
		if !register(bp.ID) || !validTokenName(bp.Name) || bp.MinWidthPx < 0 || widths[bp.MinWidthPx] {
			return DesignSystem{}, errors.New("invalid breakpoint or duplicate minimum width")
		}
		widths[bp.MinWidthPx] = true
		for id, v := range bp.BaseValues {
			base, ok := bases[id]
			if !ok {
				return DesignSystem{}, errors.New("breakpoint references a missing base token")
			}
			if _, err := tokenValueCSS(base.Kind, v, fonts); err != nil {
				return DesignSystem{}, err
			}
		}
		if err := checkFeaturesMap(bp.FeatureValues); err != nil {
			return DesignSystem{}, err
		}
		if err := validateLegacyFeatureValues(bp.LegacyFeatureValues); err != nil {
			return DesignSystem{}, err
		}
	}
	sort.Slice(value.Breakpoints, func(i, j int) bool { return value.Breakpoints[i].MinWidthPx < value.Breakpoints[j].MinWidthPx })
	return value, nil
}
func DesignSystemCSSForBuilds(value DesignSystem, builds []PinnedBuild, themeBaseURL string) (string, error) {
	if value.FormatVersion == 1 {
		return value.CSS(themeBaseURL)
	}
	value, err := NormalizeSnapshotDesignSystem(value, builds)
	if err != nil {
		return "", err
	}
	definitions, err := SnapshotFeatureTokens(builds)
	if err != nil {
		return "", err
	}
	fonts := map[string]bool{}
	for _, font := range value.Fonts {
		fonts[font.Family] = true
	}
	var b strings.Builder
	baseName := func(id string) string { return "--base-" + id }
	css := func(kind string, v TokenValue) string {
		if v.BaseTokenID != "" {
			return "var(" + baseName(v.BaseTokenID) + ")"
		}
		s, _ := tokenValueCSS(kind, v, fonts)
		return s
	}
	b.WriteString(":root{")
	for _, token := range value.BaseTokens {
		fmt.Fprintf(&b, "%s:%s;", baseName(token.ID), css(token.Kind, token.Value))
	}
	for _, d := range definitions {
		v, ok := value.FeatureValues[d.FeatureKey+"/"+d.Definition.Key]
		if !ok {
			v = d.Definition.Default
		}
		fmt.Fprintf(&b, "--feature-%s-%s:%s;", d.FeatureKey, d.Definition.Key, css(d.Definition.Kind, v))
	}
	b.WriteByte('}')
	for _, font := range value.Fonts {
		root := strings.TrimRight(themeBaseURL, "/")
		if root == "" {
			root = "/api/v2/system-assets"
		}
		fmt.Fprintf(&b, `@font-face{font-family:%s;font-style:%s;font-weight:%s;font-display:swap;src:url("%s/%s") format("woff2");`, strconv.Quote(font.Family), font.Style, font.Weight, root, font.MediaID)
		if font.UnicodeRange != "" {
			fmt.Fprintf(&b, "unicode-range:%s;", font.UnicodeRange)
		}
		b.WriteByte('}')
	}
	for _, bp := range value.Breakpoints {
		fmt.Fprintf(&b, "@media(min-width:%dpx){:root{", bp.MinWidthPx)
		for _, base := range value.BaseTokens {
			if v, ok := bp.BaseValues[base.ID]; ok {
				fmt.Fprintf(&b, "%s:%s;", baseName(base.ID), css(base.Kind, v))
			}
		}
		for _, d := range definitions {
			if v, ok := bp.FeatureValues[d.FeatureKey+"/"+d.Definition.Key]; ok {
				fmt.Fprintf(&b, "--feature-%s-%s:%s;", d.FeatureKey, d.Definition.Key, css(d.Definition.Kind, v))
			}
		}
		b.WriteString("}}")
	}
	return b.String(), nil
}

type SnapshotDesignWorkspace struct {
	Snapshot      SnapshotRecord         `json:"snapshot"`
	DesignSystem  DesignSystem           `json:"designSystem"`
	FeatureTokens []SnapshotFeatureToken `json:"featureTokens"`
}

func (s *SnapshotService) SnapshotDesignWorkspace(ctx context.Context, id uuid.UUID) (SnapshotDesignWorkspace, error) {
	record, payload, err := s.snapshotPayload(ctx, id)
	if err != nil {
		return SnapshotDesignWorkspace{}, err
	}
	value := NewDesignSystem()
	if payload.DesignSystem != nil {
		value = *payload.DesignSystem
	}
	definitions, err := SnapshotFeatureTokens(payload.Builds)
	if err != nil {
		return SnapshotDesignWorkspace{}, err
	}
	value, err = NormalizeSnapshotDesignSystem(value, payload.Builds)
	if err != nil {
		return SnapshotDesignWorkspace{}, err
	}
	return SnapshotDesignWorkspace{Snapshot: record, DesignSystem: value, FeatureTokens: definitions}, nil
}
func (s *SnapshotService) SnapshotThemeStylesheet(ctx context.Context, id uuid.UUID) (string, error) {
	_, payload, err := s.snapshotPayload(ctx, id)
	if err != nil {
		return "", err
	}
	return snapshotThemeStylesheet(payload)
}

// SnapshotThemeStylesheetForBranch returns public theme CSS only when the
// requested snapshot is still assigned to the enabled public branch. The
// assignment check and payload read share one SQL statement, so an ID-only
// lookup cannot expose an unassigned snapshot.
func (s *SnapshotService) SnapshotThemeStylesheetForBranch(ctx context.Context, branchID, snapshotID uuid.UUID) (string, error) {
	var raw []byte
	err := s.pool.QueryRow(ctx, `SELECT s.payload
		FROM site_environments e
		JOIN site_snapshots s ON s.id=e.active_snapshot_id
		WHERE e.key=$1 AND e.enabled AND e.active_snapshot_id=$2`, branchID.String(), snapshotID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	var payload SnapshotPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", err
	}
	return snapshotThemeStylesheet(payload)
}

func snapshotThemeStylesheet(payload SnapshotPayload) (string, error) {
	if payload.DesignSystem == nil {
		return "", ErrNotFound
	}
	return DesignSystemCSSForBuilds(*payload.DesignSystem, payload.Builds, "/api/v2/system-assets")
}
