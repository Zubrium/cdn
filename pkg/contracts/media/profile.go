// Package media defines the canonical, versioned x-media profile shared by
// Website (manifest resolution) and worker.media (execution). It contains no
// commands, paths, URLs, codec options or resource limits.
package media

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"

	httpjson "github.com/Zubrium/cdn/pkg/json"
)

const (
	MaxRenditions = 8
	MaxFieldBytes = 16 << 10
)

var (
	ErrProfile           = errors.New("invalid x-media profile")
	renditionNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
)

// Rendition is one bounded, named output of a raster-image profile.
type Rendition struct {
	Name   string `json:"name"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Fit    string `json:"fit"`
}

// Video declares bounded dimensions and the fixed poster rendition.
type Video struct {
	MaxWidth  int    `json:"maxWidth"`
	MaxHeight int    `json:"maxHeight"`
	Poster    string `json:"poster"`
}

// Profile is the complete canonical declaration; field order is part of its
// digest and must not be changed without a new payload version.
type Profile struct {
	Accept     string      `json:"accept"`
	Renditions []Rendition `json:"renditions,omitempty"`
	Video      *Video      `json:"video,omitempty"`
}

// ValidateProfile strictly decodes and validates one x-media declaration.
func ValidateProfile(raw json.RawMessage) (Profile, error) {
	if len(raw) == 0 || len(raw) > MaxFieldBytes {
		return Profile{}, ErrProfile
	}
	var profile Profile
	if err := httpjson.DecodeBytes(raw, &profile); err != nil {
		return Profile{}, fmt.Errorf("%w: %v", ErrProfile, err)
	}
	switch profile.Accept {
	case "image", "raster-image", "video", "file":
	default:
		return Profile{}, fmt.Errorf("%w: accept", ErrProfile)
	}
	switch profile.Accept {
	case "raster-image":
		if profile.Video != nil || len(profile.Renditions) == 0 || len(profile.Renditions) > MaxRenditions {
			return Profile{}, fmt.Errorf("%w: renditions", ErrProfile)
		}
		seen := make(map[string]struct{}, len(profile.Renditions))
		for _, rendition := range profile.Renditions {
			if !renditionNamePattern.MatchString(rendition.Name) ||
				rendition.Width < 1 || rendition.Width > 8192 || rendition.Height < 1 || rendition.Height > 8192 ||
				(rendition.Fit != "contain" && rendition.Fit != "cover") {
				return Profile{}, fmt.Errorf("%w: rendition", ErrProfile)
			}
			if _, exists := seen[rendition.Name]; exists {
				return Profile{}, fmt.Errorf("%w: duplicate rendition", ErrProfile)
			}
			seen[rendition.Name] = struct{}{}
		}
	case "video":
		if profile.Video == nil || len(profile.Renditions) != 0 || profile.Video.MaxWidth < 1 || profile.Video.MaxWidth > 8192 ||
			profile.Video.MaxHeight < 1 || profile.Video.MaxHeight > 8192 || profile.Video.Poster != "poster" {
			return Profile{}, fmt.Errorf("%w: video", ErrProfile)
		}
	default:
		if profile.Video != nil || len(profile.Renditions) != 0 {
			return Profile{}, fmt.Errorf("%w: transformation", ErrProfile)
		}
	}
	return profile, nil
}

// Canonical returns the deterministic bytes used for profile_digest. The
// processor version is included so a processor change always creates a new
// job identity even when the manifest profile is unchanged.
func Canonical(profile Profile, processorVersion string) ([]byte, error) {
	if processorVersion == "" || len(processorVersion) > 128 {
		return nil, ErrProfile
	}
	validated, err := ValidateProfile(mustProfileJSON(profile))
	if err != nil {
		return nil, err
	}
	payload := struct {
		Profile          Profile `json:"profile"`
		ProcessorVersion string  `json:"processor_version"`
	}{Profile: validated, ProcessorVersion: processorVersion}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, ErrProfile
	}
	return encoded, nil
}

// Digest returns the canonical hex digest of one profile/processor pair.
func Digest(profile Profile, processorVersion string) (string, error) {
	canonical, err := Canonical(profile, processorVersion)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

// Outputs returns the exact, ordered output names declared by a profile.
func Outputs(profile Profile) []string {
	switch profile.Accept {
	case "raster-image":
		names := make([]string, 0, len(profile.Renditions))
		for _, rendition := range profile.Renditions {
			names = append(names, rendition.Name)
		}
		return names
	case "video":
		return []string{"video", "poster"}
	default:
		return []string{"original"}
	}
}

func mustProfileJSON(profile Profile) json.RawMessage {
	raw, err := json.Marshal(profile)
	if err != nil {
		return nil
	}
	return raw
}
