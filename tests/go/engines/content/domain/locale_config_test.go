package domain_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Zubrium/cdn/engines/content/domain"
)

func TestCanonicalLocaleUsesBCP47CasingAndRejectsMalformedTags(t *testing.T) {
	for _, test := range []struct {
		input string
		want  string
	}{
		{input: "en-US", want: "en-US"},
		{input: "EN-us", want: "en-US"},
		{input: "fr", want: "fr"},
		{input: "und", want: "und"},
	} {
		canonical, err := domain.CanonicalLocale(test.input)
		if err != nil || canonical != test.want {
			t.Fatalf("CanonicalLocale(%q) = %q, %v; want %q", test.input, canonical, err, test.want)
		}
	}
	oversizedTag := "x-" + strings.TrimSuffix(strings.Repeat("aaaa-", 20), "-")
	for _, input := range []string{"", " en", "en ", "en_US", "not a locale", "zz-ZZ", oversizedTag} {
		if canonical, err := domain.CanonicalLocale(input); !errors.Is(err, domain.ErrInvalid) || canonical != "" {
			t.Fatalf("CanonicalLocale(%q) = %q, %v; want invalid", input, canonical, err)
		}
	}
}

func TestNormalizeLocaleConfigCanonicalizesDeduplicatesAndRequiresDefault(t *testing.T) {
	config, err := domain.NormalizeLocaleConfig(domain.ReplaceLocaleConfigRequest{
		ExpectedRevision: 3,
		Locales:          []string{"EN-us", "fr"},
		DefaultLocale:    "en-us",
	}, 4)
	if err != nil {
		t.Fatalf("NormalizeLocaleConfig() error = %v", err)
	}
	want := domain.LocaleConfig{Revision: 4, Locales: []string{"en-US", "fr"}, DefaultLocale: "en-US"}
	if !reflect.DeepEqual(config, want) {
		t.Fatalf("NormalizeLocaleConfig() = %#v, want %#v", config, want)
	}

	for name, request := range map[string]domain.ReplaceLocaleConfigRequest{
		"empty catalog":                    {ExpectedRevision: 0, DefaultLocale: "en"},
		"default outside catalog":          {ExpectedRevision: 0, Locales: []string{"en", "fr"}, DefaultLocale: "de"},
		"duplicate after canonicalization": {ExpectedRevision: 0, Locales: []string{"EN-us", "en-US"}, DefaultLocale: "en"},
		"negative expected revision":       {ExpectedRevision: -1, Locales: []string{"en"}, DefaultLocale: "en"},
	} {
		if _, err := domain.NormalizeLocaleConfig(request, 1); !errors.Is(err, domain.ErrInvalid) {
			t.Fatalf("%s: error = %v, want invalid", name, err)
		}
	}
	catalog := make([]string, 129)
	for index := range catalog {
		catalog[index] = "en"
	}
	if _, err := domain.NormalizeLocaleConfig(domain.ReplaceLocaleConfigRequest{Locales: catalog, DefaultLocale: "en"}, 1); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("oversized catalog error = %v, want invalid", err)
	}
}
