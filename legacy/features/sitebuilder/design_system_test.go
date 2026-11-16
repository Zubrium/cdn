package sitebuilder

import (
	"strings"
	"testing"
)

func legacyDesignSystemFixture() DesignSystem {
	return DesignSystem{
		FormatVersion: 1,
		Tokens: map[string]string{
			"--brand-color": "#123456",
			"--page-gutter": "clamp(1rem, 5vw, 5rem)",
		},
		MobileTokens: map[string]string{"--page-gutter": "1rem"},
		Fonts:        []DesignFont{},
		FontRoles:    map[string]string{"body": "Editorial Sans"},
	}
}

func TestLegacyDesignSystemPreservesOnlyDeclaredGenericValues(t *testing.T) {
	value := legacyDesignSystemFixture()
	normalized, err := NormalizeDesignSystem(value)
	if err != nil {
		t.Fatalf("normalize generic legacy theme: %v", err)
	}
	if len(normalized.Tokens) != 2 || normalized.Tokens["--brand-color"] != "#123456" {
		t.Fatalf("declared values changed: %+v", normalized.Tokens)
	}
	if len(normalized.MobileTokens) != 1 || normalized.MobileTokens["--page-gutter"] != "1rem" {
		t.Fatalf("declared responsive values changed: %+v", normalized.MobileTokens)
	}
	if len(value.Tokens) != 2 {
		t.Fatal("normalizing mutated the caller's map")
	}
	empty, err := NormalizeDesignSystem(DesignSystem{FormatVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(empty.Tokens) != 0 || len(empty.MobileTokens) != 0 || len(empty.FontRoles) != 0 {
		t.Fatalf("legacy format injected engine defaults: %+v", empty)
	}
}

func TestLegacyDesignSystemRejectsUnsafeCSSAndNames(t *testing.T) {
	cases := []DesignSystem{
		{FormatVersion: 1, Tokens: map[string]string{"brand-color": "red"}},
		{FormatVersion: 1, Tokens: map[string]string{"--brand-color": "#fff; color:red"}},
		{FormatVersion: 1, Tokens: map[string]string{"--background": "url(https://example.test/tracker)"}},
		{FormatVersion: 1, MobileTokens: map[string]string{"--layout": "expression(alert(1))"}},
		{FormatVersion: 1, FontRoles: map[string]string{"body; color": "Arial"}},
		{FormatVersion: 1, Tokens: map[string]string{"--font-body": "Arial"}, FontRoles: map[string]string{"body": "Arial"}},
	}
	for index, value := range cases {
		if _, err := NormalizeDesignSystem(value); err == nil {
			t.Errorf("case %d accepted unsafe legacy values: %+v", index, value)
		}
	}
}

func TestLegacyDesignSystemCSSHasNoVitasilDefaults(t *testing.T) {
	css, err := legacyDesignSystemFixture().CSS("")
	if err != nil {
		t.Fatalf("generate stylesheet: %v", err)
	}
	for _, expected := range []string{
		"--brand-color:#123456",
		"--font-body:\"Editorial Sans\",sans-serif",
		"@media(max-width:48rem)",
		"--page-gutter:1rem",
	} {
		if !strings.Contains(css, expected) {
			t.Errorf("stylesheet is missing %q: %s", expected, css)
		}
	}
	for _, forbidden := range []string{"--color-paper", "--background:", "--font-alumni", "Alumni Sans SC"} {
		if strings.Contains(css, forbidden) {
			t.Errorf("generic stylesheet contains legacy site default %q: %s", forbidden, css)
		}
	}
}

func TestNormalizeLegacyDesignSystemSupportsMultipleFacesForOneFamily(t *testing.T) {
	value := legacyDesignSystemFixture()
	value.Fonts = []DesignFont{
		{Family: "Editorial Sans", MediaID: "11111111-1111-4111-8111-111111111111", Weight: "100 900", Style: "normal", UnicodeRange: "U+0000-00FF"},
		{Family: "Editorial Sans", MediaID: "22222222-2222-4222-8222-222222222222", Weight: "100 900", Style: "normal", UnicodeRange: "U+0400-04FF"},
	}
	if _, err := NormalizeDesignSystem(value); err != nil {
		t.Fatalf("Unicode subsets of one variable face should be allowed: %v", err)
	}
	css, err := value.CSS("")
	if err != nil {
		t.Fatalf("generate font stylesheet: %v", err)
	}
	if strings.Count(css, "unicode-range:") != 2 {
		t.Fatalf("stylesheet should keep both subset declarations: %s", css)
	}

	value.Fonts[1].UnicodeRange = value.Fonts[0].UnicodeRange
	if _, err := NormalizeDesignSystem(value); err == nil {
		t.Fatal("duplicate font face was accepted")
	}

	value.Fonts[1].UnicodeRange = "U+0041; color:red"
	if _, err := NormalizeDesignSystem(value); err == nil {
		t.Fatal("unsafe unicode range was accepted")
	}
}
