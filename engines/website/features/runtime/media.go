package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	mediaprofile "github.com/Zubrium/cdn/pkg/contracts/media"
)

var (
	ErrMediaProfile   = errors.New("invalid media declaration")
	mediaFieldSegment = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)
	mediaFieldPathMax = 512
)

// validateMediaProfiles rejects unknown x-media declarations before a feature
// version becomes ready. Profiles never carry commands, paths, URLs, filters
// or resource limits; the shared canonical validator enforces that schema.
// ValidMediaFieldSegment reports whether one path segment can address a
// manifest-declared media input or field.
func ValidMediaFieldSegment(segment string) bool {
	return segment != "" && mediaFieldSegment.MatchString(segment) && !strings.Contains(segment, "..")
}

func validateMediaProfiles(profile map[string]any, requireMediaType bool, mediaType string) error {
	raw, exists := profile["x-media"]
	if !exists {
		return nil
	}
	if requireMediaType && mediaType != "media" {
		return fmt.Errorf("%w: x-media requires a media field", ErrMediaProfile)
	}
	// The manifest profile object is a closed declaration surface. A feature
	// cannot smuggle processor/preset/path overrides beside x-media.
	for key := range profile {
		if key != "x-media" {
			return fmt.Errorf("%w: unknown profile property %q", ErrMediaProfile, key)
		}
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrMediaProfile, err)
	}
	if _, err := mediaprofile.ValidateProfile(encoded); err != nil {
		return fmt.Errorf("%w: %v", ErrMediaProfile, err)
	}
	return nil
}

// validateArgumentMediaProfiles walks the argument schema, never its data
// values, and validates the same pinned profile contract as data inputs.
func validateArgumentMediaProfiles(schema map[string]any, depth int) error {
	if depth > 128 {
		return fmt.Errorf("%w: argument schema depth", ErrMediaProfile)
	}
	if raw, ok := schema["x-media"]; ok {
		if schema["type"] != "string" || schema["format"] != "media" {
			return fmt.Errorf("%w: x-media requires a media argument", ErrMediaProfile)
		}
		if err := validateMediaProfiles(map[string]any{"x-media": raw}, false, ""); err != nil {
			return err
		}
	}
	if properties, ok := schema["properties"].(map[string]any); ok {
		for _, value := range properties {
			if child, ok := value.(map[string]any); ok {
				if err := validateArgumentMediaProfiles(child, depth+1); err != nil {
					return err
				}
			}
		}
	}
	if child, ok := schema["items"].(map[string]any); ok {
		return validateArgumentMediaProfiles(child, depth+1)
	}
	return nil
}

// ResolveMediaProfile returns the canonical x-media declaration pinned by the
// exact feature version in a ready snapshot. A client can only supply the
// snapshot, placement and field path; it cannot select a profile or preset.
func ResolveMediaProfile(data []byte, files map[string]struct{}, fieldPath string) (mediaprofile.Profile, error) {
	if fieldPath == "" || len(fieldPath) > mediaFieldPathMax || !strings.HasPrefix(fieldPath, "/") {
		return mediaprofile.Profile{}, fmt.Errorf("%w: field path", ErrMediaProfile)
	}
	segments := strings.Split(strings.TrimPrefix(fieldPath, "/"), "/")
	if len(segments) < 1 || len(segments) > 2 {
		return mediaprofile.Profile{}, fmt.Errorf("%w: field path", ErrMediaProfile)
	}
	for _, segment := range segments {
		if !mediaFieldSegment.MatchString(segment) || strings.Contains(segment, "..") {
			return mediaprofile.Profile{}, fmt.Errorf("%w: field path", ErrMediaProfile)
		}
	}
	manifest, err := ValidateManifest(data, files)
	if err != nil {
		return mediaprofile.Profile{}, fmt.Errorf("%w: %v", ErrMediaProfile, err)
	}
	for _, raw := range manifest.DataInputs {
		var input struct {
			Key     string            `json:"key"`
			Type    string            `json:"type"`
			Profile map[string]any    `json:"profile"`
			Fields  []json.RawMessage `json:"fields"`
		}
		if json.Unmarshal(raw, &input) != nil || input.Key != segments[0] {
			continue
		}
		if len(segments) == 1 {
			return decodePinnedProfile(input.Profile)
		}
		for _, field := range input.Fields {
			var port struct {
				Path    string         `json:"path"`
				Profile map[string]any `json:"profile"`
			}
			if json.Unmarshal(field, &port) != nil {
				continue
			}
			if strings.TrimPrefix(port.Path, "/") == segments[1] {
				return decodePinnedProfile(port.Profile)
			}
		}
	}
	return mediaprofile.Profile{}, fmt.Errorf("%w: field has no pinned declaration", ErrMediaProfile)
}

func decodePinnedProfile(values map[string]any) (mediaprofile.Profile, error) {
	raw, exists := values["x-media"]
	if !exists {
		return mediaprofile.Profile{}, fmt.Errorf("%w: field has no pinned declaration", ErrMediaProfile)
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return mediaprofile.Profile{}, fmt.Errorf("%w: %v", ErrMediaProfile, err)
	}
	// ValidateProfile decodes into the canonical struct with unknown fields
	// rejected, so equivalent JSON orderings resolve to the same value. The
	// processor version is supplied only by trusted composition configuration
	// when the digest is computed.
	return mediaprofile.ValidateProfile(encoded)
}
