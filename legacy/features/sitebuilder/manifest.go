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
	versionPattern    = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-((?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)(?:\.(?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*))*))?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)
	argumentPattern   = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)
	allowedSlots      = map[string]bool{"background": true, "header": true, "main": true, "footer": true}
)

// Manifest is shipped at the root of each feature build. It describes feature
// arguments, typed model inputs, public form submission validation, and the
// browser runtime contract. It never defines CMS record forms.
type Manifest struct {
	FormatVersion int                      `json:"formatVersion"`
	Key           string                   `json:"key"`
	Name          string                   `json:"name"`
	Category      string                   `json:"category,omitempty"`
	Version       string                   `json:"version"`
	EngineAPI     string                   `json:"engineApi"`
	Entry         string                   `json:"entry,omitempty"`
	Styles        []string                 `json:"styles,omitempty"`
	SharedStyles  []string                 `json:"sharedStyles,omitempty"`
	Runtime       *RuntimeContract         `json:"runtime,omitempty"`
	Slots         []string                 `json:"slots"`
	Arguments     json.RawMessage          `json:"arguments"`
	RuntimeInput  RuntimeInputContract     `json:"-"` // derived validation shape; never supplied by manifest JSON
	DataInputs    []DataInputPort          `json:"dataInputs"`
	Form          *FormContract            `json:"form,omitempty"`
	DesignTokens  []FeatureTokenDefinition `json:"designTokens,omitempty"`
	Requests      []FeatureRequestSlot     `json:"requests,omitempty"`
}

// RuntimeContract selects an immutable browser runtime ABI for this build.
// An omitted Runtime keeps the original React component ABI and its root-level
// entry/styles fields.
type RuntimeContract struct {
	ABI          string   `json:"abi"`
	Entry        string   `json:"entry"`
	Styles       []string `json:"styles,omitempty"`
	SharedStyles []string `json:"sharedStyles,omitempty"`
}

const (
	RuntimeABIReactComponent = "legacy-react-v1"
	RuntimeABIStaticModule   = "static-module-v1"
	RuntimeABIStaticHTML     = "static-html-v1"
)

func (m Manifest) RuntimeABI() string {
	if m.Runtime == nil {
		return RuntimeABIReactComponent
	}
	return m.Runtime.ABI
}

func (m Manifest) RuntimeEntry() string {
	if m.Runtime != nil {
		return m.Runtime.Entry
	}
	return m.Entry
}

func (m Manifest) RuntimeStyles() []string {
	if m.Runtime != nil {
		return m.Runtime.Styles
	}
	return m.Styles
}

func (m Manifest) RuntimeSharedStyles() []string {
	if m.Runtime != nil {
		return m.Runtime.SharedStyles
	}
	return m.SharedStyles
}

// RuntimeInputContract is generated from typed DataInputs. It validates the
// runtime payload projected from model records; its schema is not a CMS schema.
type RuntimeInputContract struct {
	Mode   string          `json:"mode"`
	Schema json.RawMessage `json:"schema"`
}

// FormContract defines public submission validation. Editable form copy and
// labels arrive through model data inputs.
type FormContract struct {
	SubmissionSchema json.RawMessage `json:"submissionSchema"`
}

func (m Manifest) SubmissionSchema() json.RawMessage {
	if m.Form != nil {
		return m.Form.SubmissionSchema
	}
	return nil
}

// UnmarshalJSON accepts only the current typed-input contract. Older
// per-feature CMS schemas are intentionally unsupported; feature content is
// projected from the snapshot data model.
func (m *Manifest) UnmarshalJSON(raw []byte) error {
	type manifestWire Manifest
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	var version struct {
		FormatVersion int `json:"formatVersion"`
	}
	if err := json.Unmarshal(raw, &version); err != nil {
		return err
	}
	if version.FormatVersion != 2 {
		return errors.New("formatVersion must be 2; legacy feature content schemas are unsupported")
	}
	if _, exists := fields["dataInputs"]; !exists {
		return errors.New("dataInputs is required; use [] when the feature consumes no model data")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var decoded manifestWire
	if err := dec.Decode(&decoded); err != nil {
		return err
	}
	*m = Manifest(decoded)
	content, err := runtimeInputContractFromDataInputs(m.DataInputs, m.Form)
	if err != nil {
		return err
	}
	m.RuntimeInput = content
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
	if err := ValidateFeatureTokens(m.DesignTokens); err != nil {
		return err
	}
	if m.FormatVersion != 2 {
		return errors.New("formatVersion must be 2; formatVersion 1 manifests are no longer supported")
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
	if m.Runtime == nil {
		if !validAssetPath(m.Entry) || !strings.HasSuffix(strings.ToLower(m.Entry), ".js") {
			return errors.New("entry must be a safe relative JavaScript asset path")
		}
	} else {
		if m.Entry != "" || len(m.Styles) > 0 || len(m.SharedStyles) > 0 {
			return errors.New("runtime builds must declare entry, styles, and sharedStyles only inside runtime")
		}
		if !validAssetPath(m.Runtime.Entry) {
			return errors.New("runtime.entry must be a safe relative asset path")
		}
		switch m.Runtime.ABI {
		case RuntimeABIStaticModule:
			if ext := strings.ToLower(path.Ext(m.Runtime.Entry)); ext != ".js" && ext != ".mjs" {
				return errors.New("static-module-v1 runtime.entry must be a JavaScript module")
			}
		case RuntimeABIStaticHTML:
			if ext := strings.ToLower(path.Ext(m.Runtime.Entry)); ext != ".html" && ext != ".htm" {
				return errors.New("static-html-v1 runtime.entry must be an HTML fragment")
			}
		default:
			return fmt.Errorf("unsupported runtime.abi %q", m.Runtime.ABI)
		}
		if hasDuplicates(m.Runtime.Styles) {
			return errors.New("runtime.styles must not contain duplicates")
		}
		if hasDuplicates(m.Runtime.SharedStyles) {
			return errors.New("runtime.sharedStyles must not contain duplicates")
		}
		for _, shared := range m.Runtime.SharedStyles {
			if contains(m.Runtime.Styles, shared) {
				return fmt.Errorf("stylesheet %q cannot be both runtime.styles and runtime.sharedStyles", shared)
			}
		}
		for _, asset := range m.Runtime.Styles {
			if !validAssetPath(asset) || !strings.HasSuffix(strings.ToLower(asset), ".css") {
				return fmt.Errorf("runtime style %q must be a safe relative CSS asset path", asset)
			}
		}
		for _, asset := range m.Runtime.SharedStyles {
			if !validAssetPath(asset) || !strings.HasSuffix(strings.ToLower(asset), ".css") {
				return fmt.Errorf("runtime shared style %q must be a safe relative CSS asset path", asset)
			}
		}
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
	stylePaths := m.Styles
	sharedStylePaths := m.SharedStyles
	if m.Runtime != nil {
		stylePaths = m.Runtime.Styles
		sharedStylePaths = m.Runtime.SharedStyles
	}
	for _, asset := range append(append([]string(nil), stylePaths...), sharedStylePaths...) {
		if !validAssetPath(asset) || !strings.HasSuffix(strings.ToLower(asset), ".css") {
			return fmt.Errorf("style %q must be a safe relative CSS asset path", asset)
		}
	}
	if hasDuplicates(sharedStylePaths) {
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
	if err := validateFeatureRequestSlots(m.Arguments, m.Requests); err != nil {
		return fmt.Errorf("requests: %w", err)
	}
	if m.DataInputs == nil {
		return errors.New("dataInputs must be an array; use [] when the feature consumes no model data")
	}
	if err := validateDataInputPorts(m.DataInputs); err != nil {
		return fmt.Errorf("dataInputs: %w", err)
	}
	if len(m.RuntimeInput.Schema) == 0 {
		content, err := runtimeInputContractFromDataInputs(m.DataInputs, m.Form)
		if err != nil {
			return err
		}
		m.RuntimeInput = content
	}
	switch m.RuntimeInput.Mode {
	case "single", "collection", "form":
	default:
		return errors.New("typed dataInputs do not produce a supported runtime payload shape")
	}
	if len(m.RuntimeInput.Schema) == 0 {
		return errors.New("dataInputs runtime schema is required")
	}
	if err := validateObjectSchema(m.RuntimeInput.Schema, "dataInputs runtime schema"); err != nil {
		return err
	}
	if err := validateRecordIDAnnotations(m.RuntimeInput.Schema); err != nil {
		return fmt.Errorf("dataInputs runtime schema: %w", err)
	}
	if err := media.ValidateSchemaProfiles(m.RuntimeInput.Schema); err != nil {
		return fmt.Errorf("dataInputs runtime schema: %w", err)
	}
	if m.Form != nil {
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
