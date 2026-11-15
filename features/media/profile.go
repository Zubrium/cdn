package media

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

const ProcessorVersion = "ffmpeg-web-v1"
const MaxRenditions = 32

var renditionNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

type Rendition struct {
	Name   string `json:"name"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Fit    string `json:"fit"`
}

type VideoProfile struct {
	MaxWidth  int       `json:"maxWidth"`
	MaxHeight int       `json:"maxHeight"`
	Poster    Rendition `json:"poster"`
}

// FieldProfile is the x-media contract stored on one media field in a feature
// manifest. Profiles contain only deterministic processing instructions; UI
// labels and field paths are deliberately excluded so identical profiles can
// share generated objects.
type FieldProfile struct {
	Accept     string        `json:"accept"`
	Renditions []Rendition   `json:"renditions,omitempty"`
	Video      *VideoProfile `json:"video,omitempty"`
}

type Output struct {
	URL      string `json:"url"`
	Width    int    `json:"width,omitempty"`
	Height   int    `json:"height,omitempty"`
	MIMEType string `json:"mimeType"`
}

type ImageSource struct {
	ID          string            `json:"id"`
	Kind        string            `json:"kind"`
	OriginalURL string            `json:"originalUrl"`
	Src         string            `json:"src"`
	SrcSet      string            `json:"srcSet,omitempty"`
	Variants    map[string]Output `json:"variants,omitempty"`
}

type VideoSource struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	OriginalURL string `json:"originalUrl"`
	SourceURL   string `json:"sourceUrl"`
	MIMEType    string `json:"mimeType"`
	PosterURL   string `json:"posterUrl,omitempty"`
}

type RenditionState struct {
	MediaID     string            `json:"mediaId"`
	ProfileHash string            `json:"profileHash"`
	BuildDigest string            `json:"buildDigest,omitempty"`
	Status      string            `json:"status"`
	Outputs     map[string]Output `json:"outputs,omitempty"`
	Error       string            `json:"error,omitempty"`
}

func (p FieldProfile) Validate() error {
	switch p.Accept {
	case "image", "raster-image", "video", "file":
	default:
		return fmt.Errorf("x-media.accept must be image, raster-image, video or file")
	}
	if p.Accept == "video" {
		if p.Video == nil {
			return errors.New("x-media.video is required for video fields")
		}
		if len(p.Renditions) > 0 {
			return errors.New("x-media.renditions cannot be combined with x-media.video")
		}
		if p.Video.MaxWidth < 1 || p.Video.MaxHeight < 1 || p.Video.MaxWidth > 8192 || p.Video.MaxHeight > 8192 {
			return errors.New("x-media.video maxWidth and maxHeight must be between 1 and 8192")
		}
		poster := p.Video.Poster
		if poster.Name != "poster" {
			return errors.New("x-media.video.poster.name must be poster")
		}
		return validateRendition(poster)
	}
	if p.Video != nil {
		return errors.New("x-media.video is only valid for video fields")
	}
	if len(p.Renditions) > 0 && p.Accept != "raster-image" {
		return errors.New("x-media.renditions require accept=raster-image")
	}
	if len(p.Renditions) > MaxRenditions {
		return fmt.Errorf("x-media.renditions accepts at most %d entries", MaxRenditions)
	}
	seen := make(map[string]bool, len(p.Renditions))
	for _, rendition := range p.Renditions {
		if err := validateRendition(rendition); err != nil {
			return err
		}
		if seen[rendition.Name] {
			return fmt.Errorf("duplicate x-media rendition %q", rendition.Name)
		}
		seen[rendition.Name] = true
	}
	return nil
}

func validateRendition(r Rendition) error {
	if !renditionNamePattern.MatchString(r.Name) {
		return fmt.Errorf("invalid x-media rendition name %q", r.Name)
	}
	if r.Width < 1 || r.Height < 1 || r.Width > 8192 || r.Height > 8192 {
		return fmt.Errorf("x-media rendition %q width and height must be between 1 and 8192", r.Name)
	}
	if r.Fit != "contain" && r.Fit != "cover" {
		return fmt.Errorf("x-media rendition %q fit must be contain or cover", r.Name)
	}
	return nil
}

func DecodeFieldProfile(raw json.RawMessage) (FieldProfile, error) {
	var profile FieldProfile
	if err := json.Unmarshal(raw, &profile); err != nil {
		return FieldProfile{}, fmt.Errorf("decode x-media profile: %w", err)
	}
	if err := profile.Validate(); err != nil {
		return FieldProfile{}, err
	}
	return profile, nil
}

func ProfileHash(profile FieldProfile) (string, error) {
	if err := profile.Validate(); err != nil {
		return "", err
	}
	// The fixed struct field order plus the processor version makes this a
	// stable key for immutable S3 objects and safe cross-feature deduplication.
	canonical := struct {
		Processor string       `json:"processor"`
		Profile   FieldProfile `json:"profile"`
	}{ProcessorVersion, profile}
	raw, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

// ProfileAtSchema resolves a JSON Pointer into an object/array content schema.
// Array item schemas are addressed through the literal "items" segment.
func ProfileAtSchema(schema json.RawMessage, pointer string) (FieldProfile, bool, error) {
	var root any
	if err := json.Unmarshal(schema, &root); err != nil {
		return FieldProfile{}, false, err
	}
	node, ok := root.(map[string]any)
	if !ok {
		return FieldProfile{}, false, errors.New("content schema root must be an object")
	}
	trimmed := strings.Trim(pointer, "/")
	if trimmed == "" {
		return FieldProfile{}, false, errors.New("media field path is required")
	}
	for _, segment := range strings.Split(trimmed, "/") {
		segment = strings.ReplaceAll(strings.ReplaceAll(segment, "~1", "/"), "~0", "~")
		if segment == "items" {
			child, _ := node["items"].(map[string]any)
			node, ok = child, child != nil
		} else {
			properties, _ := node["properties"].(map[string]any)
			child, _ := properties[segment].(map[string]any)
			node, ok = child, child != nil
		}
		if !ok {
			return FieldProfile{}, false, fmt.Errorf("media field path %q is not in the feature schema", pointer)
		}
	}
	if node["format"] != "media" {
		return FieldProfile{}, false, fmt.Errorf("schema path %q is not a media field", pointer)
	}
	raw, exists := node["x-media"]
	if !exists {
		return FieldProfile{}, false, nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return FieldProfile{}, false, err
	}
	profile, err := DecodeFieldProfile(encoded)
	return profile, true, err
}

func ValidateSchemaProfiles(schema json.RawMessage) error {
	var root any
	if err := json.Unmarshal(schema, &root); err != nil {
		return err
	}
	return validateSchemaNode(root, "")
}

func validateSchemaNode(value any, path string) error {
	node, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	if raw, exists := node["x-media"]; exists {
		if node["format"] != "media" {
			return fmt.Errorf("x-media at %q requires format=media", path)
		}
		encoded, err := json.Marshal(raw)
		if err != nil {
			return err
		}
		if _, err := DecodeFieldProfile(encoded); err != nil {
			return fmt.Errorf("invalid media profile at %q: %w", path, err)
		}
	}
	if properties, ok := node["properties"].(map[string]any); ok {
		for key, child := range properties {
			childPath := path + "/" + strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1")
			if err := validateSchemaNode(child, childPath); err != nil {
				return err
			}
		}
	}
	if child, ok := node["items"]; ok {
		if err := validateSchemaNode(child, path+"/items"); err != nil {
			return err
		}
	}
	return nil
}
