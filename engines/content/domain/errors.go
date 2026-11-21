package domain

import (
	"errors"
	"strings"

	"golang.org/x/text/language"
)

var (
	ErrInvalid     = errors.New("invalid Content data")
	ErrNotFound    = errors.New("Content data not found")
	ErrConflict    = errors.New("Content revision conflict")
	ErrUnavailable = errors.New("Content data unavailable")
	ErrTooLarge    = errors.New("Content data exceeds configured limits")
)

type LocaleConfig struct {
	Revision      int64    `json:"revision"`
	Locales       []string `json:"locales"`
	DefaultLocale string   `json:"default_locale"`
}

type ReplaceLocaleConfigRequest struct {
	ExpectedRevision int64    `json:"expected_revision"`
	Locales          []string `json:"locales"`
	DefaultLocale    string   `json:"default_locale"`
	IdempotencyKey   string   `json:"-"`
}

// maxLocaleLength matches the Admin Content API locale schema (maxLength: 64).
// The check is applied after canonicalization because legacy tags may expand.
const maxLocaleLength = 64

// CanonicalLocale resolves BCP 47 casing and preferred language subtags.
func CanonicalLocale(value string) (string, error) {
	if value == "" || strings.TrimSpace(value) != value || !bcp47Characters(value) {
		return "", ErrInvalid
	}
	tag, err := language.Parse(value)
	if err != nil {
		return "", ErrInvalid
	}
	canonicalTag, err := language.BCP47.Canonicalize(tag)
	if err != nil {
		return "", ErrInvalid
	}
	canonical := canonicalTag.String()
	if canonical == "" || len(canonical) > maxLocaleLength || (canonical == "und" && !strings.EqualFold(value, "und")) {
		return "", ErrInvalid
	}
	return canonical, nil
}

// bcp47Characters rejects separators accepted by language.Parse for convenience
// (for example "_"), so stored catalog entries stay canonical BCP 47 tags.
func bcp47Characters(value string) bool {
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '-' {
			continue
		}
		return false
	}
	return true
}

func NormalizeLocaleConfig(request ReplaceLocaleConfigRequest, revision int64) (LocaleConfig, error) {
	if request.ExpectedRevision < 0 || len(request.Locales) == 0 || len(request.Locales) > 128 {
		return LocaleConfig{}, ErrInvalid
	}
	locales := make([]string, len(request.Locales))
	seen := make(map[string]struct{}, len(request.Locales))
	for i, candidate := range request.Locales {
		canonical, err := CanonicalLocale(candidate)
		if err != nil {
			return LocaleConfig{}, err
		}
		if _, exists := seen[canonical]; exists {
			return LocaleConfig{}, ErrInvalid
		}
		seen[canonical] = struct{}{}
		locales[i] = canonical
	}
	defaultLocale, err := CanonicalLocale(request.DefaultLocale)
	if err != nil {
		return LocaleConfig{}, err
	}
	if _, exists := seen[defaultLocale]; !exists {
		return LocaleConfig{}, ErrInvalid
	}
	return LocaleConfig{Revision: revision, Locales: locales, DefaultLocale: defaultLocale}, nil
}
