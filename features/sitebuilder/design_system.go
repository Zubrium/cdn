package sitebuilder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// DesignSystem is the snapshot-scoped design theme. V2 values are structured;
// V1 remains a generic, restricted compatibility representation.
type DesignSystem struct {
	FormatVersion       int                       `json:"formatVersion"`
	Tokens              map[string]string         `json:"tokens"`
	MobileTokens        map[string]string         `json:"mobileTokens"`
	Fonts               []DesignFont              `json:"fonts"`
	FontRoles           map[string]string         `json:"fontRoles"`
	BrandLogoID         string                    `json:"brandLogoMediaId,omitempty"`
	Groups              []TokenGroup              `json:"groups"`
	BaseTokens          []BaseToken               `json:"baseTokens"`
	FeatureValues       map[string]TokenValue     `json:"featureValues"`
	LegacyFeatureValues []LegacyFeatureTokenValue `json:"legacyFeatureValues,omitempty"`
	Breakpoints         []TokenBreakpoint         `json:"breakpoints"`
}

type DesignFont struct {
	Family       string `json:"family"`
	MediaID      string `json:"mediaId"`
	Weight       string `json:"weight"`
	Style        string `json:"style"`
	UnicodeRange string `json:"unicodeRange,omitempty"`
}

var (
	cssValuePattern          = regexp.MustCompile(`^[A-Za-z0-9 .,%()/_+#-]+$`)
	legacyCSSPropertyPattern = regexp.MustCompile(`^--[a-z][a-z0-9_-]{0,79}$`)
	legacyFontRolePattern    = regexp.MustCompile(`^[a-z][a-z0-9-]{0,79}$`)
	familyPattern            = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 _-]{0,79}$`)
	weightPattern            = regexp.MustCompile(`^(100|200|300|400|500|600|700|800|900)( (100|200|300|400|500|600|700|800|900))?$`)
	unicodePattern           = regexp.MustCompile(`(?i)^U\+[0-9A-F?]{1,6}(?:-[0-9A-F?]{1,6})?(?:\s*,\s*U\+[0-9A-F?]{1,6}(?:-[0-9A-F?]{1,6})?)*$`)
)

func NormalizeDesignSystem(value DesignSystem) (DesignSystem, error) {
	if value.FormatVersion == 2 {
		return normalizeStructuredDesign(value, nil, false)
	}
	if value.FormatVersion != 1 {
		return DesignSystem{}, errors.New("design system formatVersion must be 1 or 2")
	}

	// Format 1 is retained as a generic read/import format only. It has no
	// engine-owned token catalogue or site-specific defaults.
	if value.Tokens == nil {
		value.Tokens = map[string]string{}
	} else {
		value.Tokens = cloneStringMap(value.Tokens)
	}
	if value.MobileTokens == nil {
		value.MobileTokens = map[string]string{}
	} else {
		value.MobileTokens = cloneStringMap(value.MobileTokens)
	}
	if value.Fonts == nil {
		value.Fonts = []DesignFont{}
	}
	if value.FontRoles == nil {
		value.FontRoles = map[string]string{}
	} else {
		value.FontRoles = cloneStringMap(value.FontRoles)
	}
	if value.BrandLogoID != "" {
		if _, err := uuid.Parse(value.BrandLogoID); err != nil {
			return DesignSystem{}, errors.New("brandLogoMediaId must reference a media object")
		}
	}

	for _, values := range []map[string]string{value.Tokens, value.MobileTokens} {
		for key, raw := range values {
			if !legacyCSSPropertyPattern.MatchString(key) {
				return DesignSystem{}, fmt.Errorf("invalid legacy CSS custom property %q", key)
			}
			if strings.TrimSpace(raw) == "" {
				delete(values, key)
				continue
			}
			lower := strings.ToLower(raw)
			if !cssValuePattern.MatchString(raw) || strings.Contains(lower, "url(") || strings.Contains(lower, "expression") || strings.Contains(raw, "\\") {
				return DesignSystem{}, fmt.Errorf("%s contains an unsupported CSS value", key)
			}
		}
	}

	faces := map[string]bool{}
	for i, font := range value.Fonts {
		if !familyPattern.MatchString(font.Family) {
			return DesignSystem{}, fmt.Errorf("font %d has an invalid family name", i)
		}
		if _, err := uuid.Parse(font.MediaID); err != nil {
			return DesignSystem{}, fmt.Errorf("font %q must reference a media object", font.Family)
		}
		if !weightPattern.MatchString(font.Weight) {
			return DesignSystem{}, fmt.Errorf("font %q has an invalid weight range", font.Family)
		}
		if font.Style != "normal" && font.Style != "italic" {
			return DesignSystem{}, fmt.Errorf("font %q style must be normal or italic", font.Family)
		}
		if font.UnicodeRange != "" && !unicodePattern.MatchString(font.UnicodeRange) {
			return DesignSystem{}, fmt.Errorf("font %q has an invalid unicode range", font.Family)
		}
		face := font.Family + "\x00" + font.Weight + "\x00" + font.Style + "\x00" + font.UnicodeRange
		if faces[face] {
			return DesignSystem{}, fmt.Errorf("font face %q at weight %s (%s) is duplicated", font.Family, font.Weight, font.Style)
		}
		faces[face] = true
	}
	for role, family := range value.FontRoles {
		if !legacyFontRolePattern.MatchString(role) || !familyPattern.MatchString(family) {
			return DesignSystem{}, fmt.Errorf("font role %q has an invalid name or family", role)
		}
		if _, collision := value.Tokens["--font-"+role]; collision {
			return DesignSystem{}, fmt.Errorf("font role %q conflicts with a CSS custom property", role)
		}
	}
	return value, nil
}

func cloneStringMap(values map[string]string) map[string]string {
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func decodeDesignSystem(raw json.RawMessage) (DesignSystem, error) {
	var value DesignSystem
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return DesignSystem{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return DesignSystem{}, errors.New("design system contains trailing JSON")
		}
		return DesignSystem{}, err
	}
	return value, nil
}

func validateDesignSystemMedia(ctx context.Context, queryer metadataQueryer, value DesignSystem) error {
	ids := []struct{ id, mime string }{}
	if value.BrandLogoID != "" {
		ids = append(ids, struct{ id, mime string }{value.BrandLogoID, "image/"})
	}
	for _, font := range value.Fonts {
		ids = append(ids, struct{ id, mime string }{font.MediaID, "font/woff2"})
	}
	seen := map[string]bool{}
	for _, item := range ids {
		validationKey := item.id + "\x00" + item.mime
		if seen[validationKey] {
			continue
		}
		seen[validationKey] = true
		id, err := uuid.Parse(item.id)
		if err != nil {
			return err
		}
		var mime, visibility string
		if err := queryer.QueryRow(ctx, `SELECT content_type,visibility FROM media_objects WHERE id=$1 AND status='ready'`, id).Scan(&mime, &visibility); err != nil {
			return fmt.Errorf("system asset %s is unavailable", item.id)
		}
		if visibility != "public" {
			return fmt.Errorf("system asset %s must be public before it can be used in a website theme", item.id)
		}
		if item.mime == "image/" {
			if !strings.HasPrefix(mime, "image/") {
				return fmt.Errorf("brand logo %s must be an image", item.id)
			}
		} else if mime != item.mime {
			return fmt.Errorf("font %s must use %s", item.id, item.mime)
		}
	}
	return nil
}

func (value DesignSystem) CSS(themeBaseURL string) (string, error) {
	if value.FormatVersion == 2 {
		return DesignSystemCSSForBuilds(value, nil, themeBaseURL)
	}
	normalized, err := NormalizeDesignSystem(value)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	tokens := make([]string, 0, len(normalized.Tokens))
	for key := range normalized.Tokens {
		tokens = append(tokens, key)
	}
	sort.Strings(tokens)
	b.WriteString(":root{")
	for _, key := range tokens {
		fmt.Fprintf(&b, "%s:%s;", key, normalized.Tokens[key])
	}
	roles := make([]string, 0, len(normalized.FontRoles))
	for role := range normalized.FontRoles {
		roles = append(roles, role)
	}
	sort.Strings(roles)
	for _, role := range roles {
		family := normalized.FontRoles[role]
		if family != "system-ui" && family != "serif" && family != "sans-serif" && family != "monospace" {
			family = `"` + family + `"`
		}
		fmt.Fprintf(&b, "--font-%s:%s,sans-serif;", role, family)
	}
	b.WriteByte('}')
	mobile := make([]string, 0, len(normalized.MobileTokens))
	for key := range normalized.MobileTokens {
		mobile = append(mobile, key)
	}
	sort.Strings(mobile)
	if len(mobile) > 0 {
		b.WriteString("@media(max-width:48rem){:root{")
		for _, key := range mobile {
			fmt.Fprintf(&b, "%s:%s;", key, normalized.MobileTokens[key])
		}
		b.WriteString("}}")
	}
	for _, font := range normalized.Fonts {
		root := strings.TrimRight(themeBaseURL, "/")
		if root == "" {
			root = "/api/v2/system-assets"
		}
		fmt.Fprintf(&b, `@font-face{font-family:"%s";font-style:%s;font-weight:%s;font-display:swap;src:url("%s/%s") format("woff2");`, font.Family, font.Style, font.Weight, root, font.MediaID)
		if font.UnicodeRange != "" {
			fmt.Fprintf(&b, "unicode-range:%s;", font.UnicodeRange)
		}
		b.WriteByte('}')
	}
	return b.String(), nil
}

type designPreview struct {
	value   DesignSystem
	expires time.Time
}

func (s *SnapshotService) CreateDesignPreview(ctx context.Context, raw json.RawMessage) (string, error) {
	if s.previewTTL <= 0 {
		return "", fmt.Errorf("design preview TTL is not configured")
	}
	value, err := decodeDesignSystem(raw)
	if err != nil {
		return "", fmt.Errorf("%w: decode design system: %v", ErrInvalid, err)
	}
	value, err = NormalizeDesignSystem(value)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if err := validateDesignSystemMedia(ctx, s.pool, value); err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	token := uuid.NewString()
	s.previewMu.Lock()
	if s.previews == nil {
		s.previews = map[string]designPreview{}
	}
	s.previews[token] = designPreview{value: value, expires: time.Now().Add(s.previewTTL)}
	s.previewMu.Unlock()
	return token, nil
}

func (s *SnapshotService) designPreview(token string) (DesignSystem, bool) {
	s.previewMu.Lock()
	defer s.previewMu.Unlock()
	preview, ok := s.previews[token]
	if !ok {
		return DesignSystem{}, false
	}
	if time.Now().After(preview.expires) {
		delete(s.previews, token)
		return DesignSystem{}, false
	}
	return preview.value, true
}

// DesignPreview is the read-only counterpart used by the public CSS handler.
func (s *SnapshotService) DesignPreview(token string) (DesignSystem, bool) {
	return s.designPreview(token)
}

func (s *SnapshotService) ThemeForSnapshot(ctx context.Context, id uuid.UUID) (DesignSystem, error) {
	var raw []byte
	if err := s.pool.QueryRow(ctx, `SELECT payload FROM site_snapshots WHERE id=$1`, id).Scan(&raw); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return DesignSystem{}, ErrNotFound
		}
		return DesignSystem{}, err
	}
	var payload SnapshotPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return DesignSystem{}, err
	}
	if payload.DesignSystem == nil {
		return DesignSystem{}, ErrNotFound
	}
	return NormalizeDesignSystem(*payload.DesignSystem)
}
