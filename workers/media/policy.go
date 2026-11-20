package media

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"regexp"
	"strings"
	"time"
)

const MaxProfileNameBytes = 128

var (
	ErrInvalidRequest    = errors.New("media_invalid_request")
	ErrPresetUnavailable = errors.New("media_preset_unavailable")
	ErrInvalidPreset     = errors.New("media_invalid_preset")
	profileToken         = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$`)
	sha256Token          = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

// Preset is trusted startup configuration. A preset version is immutable once
// tasks refer to it; runtime callers can only select the exact name and version.
type Preset struct {
	Name              string        `json:"name"`
	Version           string        `json:"version"`
	InputContentTypes []string      `json:"input_content_types"`
	OutputContentType string        `json:"output_content_type"`
	OutputFormat      string        `json:"output_format"`
	FFmpegArgs        []string      `json:"ffmpeg_args"`
	Timeout           time.Duration `json:"timeout"`
	MaxInputBytes     int64         `json:"max_input_bytes"`
	MaxOutputBytes    int64         `json:"max_output_bytes"`
}

// Registry is a defensive immutable copy of the configured allowlist.
type Registry struct{ presets map[string]Preset }

func NewRegistry(configured []Preset) (*Registry, error) {
	if len(configured) == 0 {
		return nil, ErrInvalidPreset
	}
	registry := &Registry{presets: make(map[string]Preset, len(configured))}
	for _, preset := range configured {
		if !validToken(preset.Name) || !validToken(preset.Version) || preset.MaxInputBytes <= 0 || preset.MaxOutputBytes <= 0 ||
			!validContentType(preset.OutputContentType) || activeContentType(preset.OutputContentType) || len(preset.InputContentTypes) == 0 ||
			(preset.OutputFormat != "" && !formatMatchesContentType(preset.OutputFormat, preset.OutputContentType)) {
			return nil, ErrInvalidPreset
		}
		key := presetKey(preset.Name, preset.Version)
		if _, exists := registry.presets[key]; exists {
			return nil, ErrInvalidPreset
		}
		copyPreset := preset
		copyPreset.InputContentTypes = append([]string(nil), preset.InputContentTypes...)
		copyPreset.FFmpegArgs = append([]string(nil), preset.FFmpegArgs...)
		seen := make(map[string]struct{}, len(copyPreset.InputContentTypes))
		for _, contentType := range copyPreset.InputContentTypes {
			if !validContentType(contentType) || activeContentType(contentType) {
				return nil, ErrInvalidPreset
			}
			if _, exists := seen[contentType]; exists {
				return nil, ErrInvalidPreset
			}
			seen[contentType] = struct{}{}
		}
		registry.presets[key] = copyPreset
	}
	return registry, nil
}

func (r *Registry) validExecutionLimits(maxConcurrency int, maxStagedBytes int64) bool {
	if r == nil || maxConcurrency < 1 || maxStagedBytes < 1 {
		return false
	}
	for _, preset := range r.presets {
		if preset.Timeout <= 0 || preset.MaxInputBytes <= 0 || preset.MaxOutputBytes <= 0 ||
			!safeOutputFormat(preset.OutputFormat) || !formatMatchesContentType(preset.OutputFormat, preset.OutputContentType) || len(preset.FFmpegArgs) == 0 {
			return false
		}
		for _, arg := range preset.FFmpegArgs {
			if !validTrustedArg(arg) {
				return false
			}
		}
		perJob := preset.MaxInputBytes + preset.MaxOutputBytes
		if perJob < preset.MaxInputBytes || perJob > maxStagedBytes/int64(maxConcurrency) {
			return false
		}
	}
	return true
}

func (r *Registry) Lookup(name, version string) (Preset, bool) {
	if r == nil {
		return Preset{}, false
	}
	preset, ok := r.presets[presetKey(name, version)]
	if !ok {
		return Preset{}, false
	}
	preset.InputContentTypes = append([]string(nil), preset.InputContentTypes...)
	preset.FFmpegArgs = append([]string(nil), preset.FFmpegArgs...)
	return preset, true
}

// ValidateRequest rejects arbitrary/unconfigured profile versions and values
// that could otherwise be interpreted as paths or commands.
func (r *Registry) ValidateRequest(request Request) (Preset, error) {
	if request.TaskID == "" || request.SourceFileID == "" || !validToken(request.Profile) || !validToken(request.ProfileVersion) ||
		!validToken(request.IdempotencyKey) {
		return Preset{}, ErrInvalidRequest
	}
	preset, ok := r.Lookup(request.Profile, request.ProfileVersion)
	if !ok {
		return Preset{}, ErrPresetUnavailable
	}
	return preset, nil
}

// Fingerprint is stable for the exact source checksum and preset version.
// Length-prefixed fields avoid delimiter ambiguity.
func Fingerprint(sourceSHA256 string, request Request) (string, error) {
	if !sha256Token.MatchString(sourceSHA256) || !validToken(request.SourceFileID) || !validToken(request.Profile) || !validToken(request.ProfileVersion) {
		return "", ErrInvalidRequest
	}
	canonical, err := json.Marshal([]string{sourceSHA256, request.SourceFileID, request.Profile, request.ProfileVersion})
	if err != nil {
		return "", ErrInvalidRequest
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

// ValidateSource applies configured byte and MIME limits before a runner is
// given source bytes. The actual Core authorization remains in Core's file API.
func ValidateSource(preset Preset, contentType string, size int64) error {
	if size <= 0 || size > preset.MaxInputBytes || !validContentType(contentType) {
		return ErrInvalidRequest
	}
	for _, allowed := range preset.InputContentTypes {
		if contentType == allowed {
			return nil
		}
	}
	return ErrInvalidRequest
}

// ValidateOutput ensures a completed output is bounded and agrees with the
// selected preset; it does not activate or persist an artifact.
func ValidateOutput(preset Preset, result Result) error {
	if !validToken(result.OutputFileID) || result.Size <= 0 || result.Size > preset.MaxOutputBytes ||
		!sha256Token.MatchString(result.Checksum) || result.ContentType != preset.OutputContentType {
		return ErrInvalidRequest
	}
	return nil
}

func activeContentType(value string) bool {
	switch value {
	case "text/html", "application/xhtml+xml", "image/svg+xml", "application/xml", "text/xml", "application/javascript", "text/javascript":
		return true
	default:
		return false
	}
}

func formatMatchesContentType(format, contentType string) bool {
	switch format {
	case "webp":
		return contentType == "image/webp"
	case "jpeg":
		return contentType == "image/jpeg"
	case "png":
		return contentType == "image/png"
	case "avif":
		return contentType == "image/avif"
	case "gif":
		return contentType == "image/gif"
	case "mp4":
		return contentType == "video/mp4"
	case "webm":
		return contentType == "video/webm"
	default:
		return false
	}
}

func validToken(value string) bool {
	return len(value) > 0 && len(value) <= MaxProfileNameBytes && profileToken.MatchString(value) && !strings.Contains(value, "..")
}

func validContentType(value string) bool {
	if len(value) == 0 || len(value) > 127 || strings.ContainsAny(value, "\r\n; ") {
		return false
	}
	parsed, parameters, err := mime.ParseMediaType(value)
	return err == nil && parsed == value && len(parameters) == 0 && !strings.Contains(value, "*")
}

func presetKey(name, version string) string { return fmt.Sprintf("%s\x00%s", name, version) }
