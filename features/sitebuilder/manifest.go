// Package sitebuilder owns feature-build manifests, page composition and
// immutable site snapshots.
package sitebuilder

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"strings"

	"websiteengine/features/media"
	"websiteengine/pkg/platform/validate"
)

const CurrentEngineAPI = "2"

var supportedEngineAPIs = map[string]bool{"1": true, "2": true}

var (
	featureKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,99}$`)
	versionPattern    = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)
	argumentPattern   = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)
	allowedSlots      = map[string]bool{"background": true, "header": true, "main": true, "footer": true}
)

// Manifest is shipped next to each feature's ESM entry point. The JSON
// Schemas are authored by the feature developer and are rendered by the
// engine's form generator; they are never edited in the admin UI.
type Manifest struct {
	FormatVersion int             `json:"formatVersion"`
	Key           string          `json:"key"`
	Name          string          `json:"name"`
	Category      string          `json:"category,omitempty"`
	Version       string          `json:"version"`
	EngineAPI     string          `json:"engineApi"`
	Entry         string          `json:"entry"`
	Styles        []string        `json:"styles,omitempty"`
	SharedStyles  []string        `json:"sharedStyles,omitempty"`
	Slots         []string        `json:"slots"`
	Arguments     json.RawMessage `json:"arguments"`
	Content       ContentContract `json:"content"`
	Form          *FormContract   `json:"form,omitempty"`
}

type ContentContract struct {
	Mode           string          `json:"mode"`
	Schema         json.RawMessage `json:"schema"`
	SettingsSchema json.RawMessage `json:"settingsSchema,omitempty"`
	// Form is accepted for builds created by an earlier engine draft. New
	// manifests declare the submission schema at the top-level form property.
	Form *FormContract `json:"form,omitempty"`
}

// FormContract keeps editable form presentation content separate from the
// schema used to validate public submissions. Older manifests omit this
// block; their content.schema remains the submission schema.
type FormContract struct {
	SubmissionSchema json.RawMessage `json:"submissionSchema"`
}

func (m Manifest) SubmissionSchema() json.RawMessage {
	if m.Form != nil {
		return m.Form.SubmissionSchema
	}
	if m.Content.Form != nil {
		return m.Content.Form.SubmissionSchema
	}
	return nil
}

// ParseManifest decodes and validates a developer-authored feature manifest.
func ParseManifest(raw []byte) (Manifest, error) {
	var m Manifest
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return Manifest{}, fmt.Errorf("decode feature manifest: %w", err)
	}
	if err := requireJSONEnd(dec); err != nil {
		return Manifest{}, fmt.Errorf("decode feature manifest: %w", err)
	}
	if err := m.Validate(); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

func requireJSONEnd(dec *json.Decoder) error {
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("trailing JSON value")
		}
		return err
	}
	return nil
}

func (m Manifest) Validate() error {
	if m.FormatVersion != 1 {
		return errors.New("formatVersion must be 1")
	}
	if !featureKeyPattern.MatchString(m.Key) {
		return errors.New("key must start with a lowercase letter and contain only lowercase letters, numbers, and hyphens")
	}
	if strings.TrimSpace(m.Name) == "" || len([]rune(m.Name)) > 200 {
		return errors.New("name must contain between 1 and 200 characters")
	}
	if len([]rune(m.Category)) > 100 || (m.Category != "" && strings.TrimSpace(m.Category) == "") {
		return errors.New("category must be omitted or contain between 1 and 100 non-whitespace characters")
	}
	if !versionPattern.MatchString(m.Version) {
		return errors.New("version must use semantic version format, for example 1.2.3")
	}
	if !supportedEngineAPIs[m.EngineAPI] {
		return fmt.Errorf("engineApi %q is not supported (current: %q)", m.EngineAPI, CurrentEngineAPI)
	}
	if !validAssetPath(m.Entry) || !strings.HasSuffix(strings.ToLower(m.Entry), ".js") {
		return errors.New("entry must be a safe relative JavaScript asset path")
	}
	if len(m.Slots) == 0 {
		return errors.New("at least one placement slot is required")
	}
	seenSlots := make(map[string]bool, len(m.Slots))
	for _, slot := range m.Slots {
		if !allowedSlots[slot] || seenSlots[slot] {
			return fmt.Errorf("unsupported or duplicate placement slot %q", slot)
		}
		seenSlots[slot] = true
	}
	for _, asset := range append(append([]string(nil), m.Styles...), m.SharedStyles...) {
		if !validAssetPath(asset) || !strings.HasSuffix(strings.ToLower(asset), ".css") {
			return fmt.Errorf("style %q must be a safe relative CSS asset path", asset)
		}
	}
	if hasDuplicates(m.SharedStyles) {
		return errors.New("sharedStyles must not contain duplicates")
	}
	if len(m.Arguments) == 0 {
		return errors.New("arguments schema is required")
	}
	if err := validateObjectSchema(m.Arguments, "arguments"); err != nil {
		return err
	}
	if err := media.ValidateSchemaProfiles(m.Arguments); err != nil {
		return fmt.Errorf("arguments: %w", err)
	}
	switch m.Content.Mode {
	case "single", "collection", "form":
	default:
		return errors.New("content.mode must be single, collection, or form")
	}
	if len(m.Content.Schema) == 0 {
		return errors.New("content.schema is required")
	}
	if err := validateObjectSchema(m.Content.Schema, "content.schema"); err != nil {
		return err
	}
	if err := media.ValidateSchemaProfiles(m.Content.Schema); err != nil {
		return fmt.Errorf("content.schema: %w", err)
	}
	if len(m.Content.SettingsSchema) > 0 {
		if m.Content.Mode != "collection" {
			return errors.New("content.settingsSchema is only valid when content.mode is collection")
		}
		if err := validateObjectSchema(m.Content.SettingsSchema, "content.settingsSchema"); err != nil {
			return err
		}
		if err := media.ValidateSchemaProfiles(m.Content.SettingsSchema); err != nil {
			return fmt.Errorf("content.settingsSchema: %w", err)
		}
	}
	if m.Content.Form != nil {
		if m.Content.Mode != "form" {
			return errors.New("content.form is only valid when content.mode is form")
		}
		if m.Form != nil {
			return errors.New("declare form.submissionSchema in one place only")
		}
		if len(m.Content.Form.SubmissionSchema) == 0 {
			return errors.New("content.form.submissionSchema is required")
		}
		if err := validateObjectSchema(m.Content.Form.SubmissionSchema, "content.form.submissionSchema"); err != nil {
			return err
		}
		if err := media.ValidateSchemaProfiles(m.Content.Form.SubmissionSchema); err != nil {
			return fmt.Errorf("content.form.submissionSchema: %w", err)
		}
	}
	if m.Form != nil {
		if m.Content.Mode != "form" {
			return errors.New("form is only valid when content.mode is form")
		}
		if len(m.Form.SubmissionSchema) == 0 {
			return errors.New("form.submissionSchema is required")
		}
		if err := validateObjectSchema(m.Form.SubmissionSchema, "form.submissionSchema"); err != nil {
			return err
		}
		if err := media.ValidateSchemaProfiles(m.Form.SubmissionSchema); err != nil {
			return fmt.Errorf("form.submissionSchema: %w", err)
		}
	}
	return nil
}

func hasDuplicates(values []string) bool {
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		if seen[value] {
			return true
		}
		seen[value] = true
	}
	return false
}

func validateObjectSchema(raw json.RawMessage, name string) error {
	var shape struct {
		Type       string                     `json:"type"`
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(raw, &shape); err != nil || shape.Type != "object" {
		return fmt.Errorf("%s must be a JSON Schema object with type object", name)
	}
	if _, err := validate.Compile(raw); err != nil {
		return fmt.Errorf("%s is invalid: %w", name, err)
	}
	for field := range shape.Properties {
		if !argumentPattern.MatchString(field) {
			return fmt.Errorf("%s property %q has an invalid field name", name, field)
		}
	}
	return nil
}

func validAssetPath(value string) bool {
	if value == "" || strings.ContainsAny(value, "\\\x00") || strings.HasPrefix(value, "/") {
		return false
	}
	clean := path.Clean(value)
	return clean == value && clean != "." && clean != ".." && !strings.HasPrefix(clean, "../")
}
