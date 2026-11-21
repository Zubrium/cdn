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

// CanonicalLocale resolves BCP 47 casing and preferred language subtags.
func CanonicalLocale(value string) (string, error) {
	if value == "" || strings.TrimSpace(value) != value {
		return "", ErrInvalid
	}
	tag, err := language.Parse(value)
	if err != nil {
		return "", ErrInvalid
	}
	canonical := language.BCP47.Canonicalize(tag).String()
	if canonical == "" || (canonical == "und" && !strings.EqualFold(value, "und")) {
		return "", ErrInvalid
	}
	return canonical, nil
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
