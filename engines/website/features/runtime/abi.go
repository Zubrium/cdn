package runtime

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"regexp"
	"strings"
)

// ABI is the only feature runtime ABI accepted by this build. The previous
// versioned name is deliberately not accepted: there is no compatibility alias.
const ABI = "static-lifecycle"

const (
	ManifestFormatVersion = 2
	maxManifestBytes      = 1 << 20
	maxOrigins            = 32
	maxDataInputs         = 64
	maxSlots              = 32
)

var (
	ErrABI        = errors.New("unsupported feature ABI")
	ErrManifest   = errors.New("invalid feature manifest")
	ErrAsset      = errors.New("invalid feature asset")
	ErrOrigin     = errors.New("invalid feature origin")
	ErrSecret     = errors.New("secret-like value in public feature contract")
	semverPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)
	originPattern = regexp.MustCompile(`^https://[A-Za-z0-9.-]+(:[0-9]{1,5})?$`)
	secretKeyPart = regexp.MustCompile(`(?i)(password|passwd|secret|token|credential|api[_-]?key|private[_-]?key|authorization|cookie)`)
)

// Manifest is the trusted, versioned description of one feature build. Unknown
// JSON fields are rejected so a new field cannot silently change semantics.
type Manifest struct {
	FormatVersion int               `json:"formatVersion"`
	Key           string            `json:"key"`
	Name          string            `json:"name"`
	Version       string            `json:"version"`
	EngineAPI     string            `json:"engineApi"`
	Slots         []string          `json:"slots"`
	Arguments     map[string]any    `json:"arguments"`
	DataInputs    []json.RawMessage `json:"dataInputs"`
	Content       json.RawMessage   `json:"content"`
	Form          json.RawMessage   `json:"form"`
	Runtime       RuntimeSpec       `json:"runtime"`
	Requests      json.RawMessage   `json:"requests"`
}

// RuntimeSpec pins the ESM entry, its styles and the browser network surface.
type RuntimeSpec struct {
	ABI          string   `json:"abi"`
	Entry        string   `json:"entry"`
	Styles       []string `json:"styles"`
	SharedStyles []string `json:"sharedStyles"`
	Origins      []string `json:"origins"`
	SDK          []string `json:"sdk"`
}

// ValidateManifest parses and validates a manifest.json against the target ABI
// and the already-verified set of archive file paths. It never executes code.
func ValidateManifest(data []byte, files map[string]struct{}) (Manifest, error) {
	if len(data) == 0 || len(data) > maxManifestBytes {
		return Manifest{}, fmt.Errorf("%w: manifest size", ErrManifest)
	}
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return Manifest{}, fmt.Errorf("%w: %v", ErrManifest, err)
	}
	var manifest Manifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("%w: %v", ErrManifest, err)
	}
	if err := ensureNoTrailingJSON(decoder); err != nil {
		return Manifest{}, fmt.Errorf("%w: %v", ErrManifest, err)
	}
	if err := validateManifestFields(manifest, files); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func validateManifestFields(manifest Manifest, files map[string]struct{}) error {
	if manifest.FormatVersion != ManifestFormatVersion ||
		manifest.Key == "" || strings.TrimSpace(manifest.Key) != manifest.Key ||
		manifest.Name == "" || strings.TrimSpace(manifest.Name) != manifest.Name ||
		manifest.Version == "" || !semverPattern.MatchString(manifest.Version) ||
		manifest.EngineAPI == "" || strings.TrimSpace(manifest.EngineAPI) != manifest.EngineAPI {
		return fmt.Errorf("%w: mandatory metadata", ErrManifest)
	}
	if manifest.Slots == nil || len(manifest.Slots) == 0 || len(manifest.Slots) > maxSlots {
		return fmt.Errorf("%w: slots", ErrManifest)
	}
	for _, slot := range manifest.Slots {
		if slot == "" || strings.TrimSpace(slot) != slot {
			return fmt.Errorf("%w: slot", ErrManifest)
		}
	}
	if manifest.Arguments == nil {
		return fmt.Errorf("%w: arguments", ErrManifest)
	}
	if err := rejectSecretKeys(manifest.Arguments); err != nil {
		return err
	}
	if err := validateArgumentMediaProfiles(manifest.Arguments, 0); err != nil {
		return err
	}
	if manifest.DataInputs == nil || len(manifest.DataInputs) > maxDataInputs {
		return fmt.Errorf("%w: dataInputs", ErrManifest)
	}
	inputKeys := make(map[string]struct{}, len(manifest.DataInputs))
	for _, raw := range manifest.DataInputs {
		var input struct {
			Key     string            `json:"key"`
			Kind    string            `json:"kind"`
			Type    string            `json:"type"`
			Profile map[string]any    `json:"profile"`
			Fields  []json.RawMessage `json:"fields"`
		}
		decoder := strictDecoder(raw)
		if len(raw) == 0 || decoder.Decode(&input) != nil ||
			input.Key == "" || strings.TrimSpace(input.Key) != input.Key {
			return fmt.Errorf("%w: dataInput", ErrManifest)
		}
		if input.Kind != "scalar" && input.Kind != "record" && input.Kind != "collection" {
			return fmt.Errorf("%w: dataInput kind", ErrManifest)
		}
		if input.Type != "" && !validDataFieldType(input.Type) {
			return fmt.Errorf("%w: dataInput type", ErrManifest)
		}
		if _, exists := inputKeys[input.Key]; exists {
			return fmt.Errorf("%w: duplicate dataInput key", ErrManifest)
		}
		inputKeys[input.Key] = struct{}{}
		if err := rejectSecretKeys(input.Profile); err != nil {
			return err
		}
		if err := validateMediaProfiles(input.Profile, input.Type != "", input.Type); err != nil {
			return err
		}
		for _, field := range input.Fields {
			var port struct {
				Path    string         `json:"path"`
				Type    string         `json:"type"`
				Profile map[string]any `json:"profile"`
			}
			portDecoder := strictDecoder(field)
			if len(field) == 0 || portDecoder.Decode(&port) != nil ||
				port.Path == "" || strings.TrimSpace(port.Path) != port.Path ||
				!validDataFieldType(port.Type) {
				return fmt.Errorf("%w: dataInput field", ErrManifest)
			}
			if err := rejectSecretKeys(port.Profile); err != nil {
				return err
			}
			if err := validateMediaProfiles(port.Profile, true, port.Type); err != nil {
				return err
			}
		}
	}
	if len(manifest.Content) > 0 && string(manifest.Content) != "null" {
		var object map[string]json.RawMessage
		if json.Unmarshal(manifest.Content, &object) != nil || object == nil {
			return fmt.Errorf("%w: content must be an object", ErrManifest)
		}
		for key := range object {
			if secretKeyPart.MatchString(key) {
				return fmt.Errorf("%w: %q", ErrSecret, key)
			}
		}
	}
	if len(manifest.Form) > 0 && string(manifest.Form) != "null" {
		var form struct {
			SubmissionSchema json.RawMessage `json:"submissionSchema"`
		}
		decoder := json.NewDecoder(bytes.NewReader(manifest.Form))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&form) != nil {
			return fmt.Errorf("%w: form contract", ErrManifest)
		}
		var schema map[string]json.RawMessage
		if json.Unmarshal(form.SubmissionSchema, &schema) != nil || schema == nil {
			return fmt.Errorf("%w: form submissionSchema", ErrManifest)
		}
	}
	if len(manifest.Requests) > 0 && string(manifest.Requests) != "null" {
		var requests []json.RawMessage
		if json.Unmarshal(manifest.Requests, &requests) != nil {
			return fmt.Errorf("%w: requests", ErrManifest)
		}
		for _, raw := range requests {
			var request struct {
				Origin string         `json:"origin"`
				Ports  []string       `json:"ports"`
				Schema map[string]any `json:"schema"`
			}
			decoder := json.NewDecoder(bytes.NewReader(raw))
			decoder.DisallowUnknownFields()
			if decoder.Decode(&request) != nil {
				return fmt.Errorf("%w: request contract", ErrManifest)
			}
			if err := validateOrigin(request.Origin); err != nil {
				return err
			}
		}
	}
	if err := validateRuntimeSpec(manifest.Runtime, files); err != nil {
		return err
	}
	return nil
}

func validateRuntimeSpec(spec RuntimeSpec, files map[string]struct{}) error {
	if spec.ABI != ABI {
		return fmt.Errorf("%w: %q", ErrABI, spec.ABI)
	}
	if !validAssetPath(spec.Entry) || (path.Ext(spec.Entry) != ".js" && path.Ext(spec.Entry) != ".mjs") {
		return fmt.Errorf("%w: ESM entry", ErrAsset)
	}
	assets := append([]string{spec.Entry}, spec.Styles...)
	assets = append(assets, spec.SharedStyles...)
	for _, asset := range assets {
		if !validAssetPath(asset) {
			return fmt.Errorf("%w: path", ErrAsset)
		}
		if _, ok := files[asset]; !ok {
			return fmt.Errorf("%w: %s is missing from the build", ErrAsset, asset)
		}
	}
	for _, asset := range append(append([]string{}, spec.Styles...), spec.SharedStyles...) {
		if path.Ext(asset) != ".css" {
			return fmt.Errorf("%w: stylesheet", ErrAsset)
		}
	}
	if len(spec.Origins) > maxOrigins {
		return fmt.Errorf("%w: too many origins", ErrOrigin)
	}
	for _, origin := range spec.Origins {
		if err := validateOrigin(origin); err != nil {
			return err
		}
	}
	if len(spec.SDK) > maxOrigins {
		return fmt.Errorf("%w: too many SDK exports", ErrManifest)
	}
	for _, name := range spec.SDK {
		if name == "" || strings.TrimSpace(name) != name {
			return fmt.Errorf("%w: SDK export", ErrManifest)
		}
	}
	return nil
}

func validateOrigin(value string) error {
	if !originPattern.MatchString(value) {
		return fmt.Errorf("%w: %q", ErrOrigin, value)
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("%w: %q", ErrOrigin, value)
	}
	if host := parsed.Hostname(); host == "localhost" || netIPLiteral(host) {
		return fmt.Errorf("%w: %q", ErrOrigin, value)
	}
	return nil
}

func netIPLiteral(host string) bool {
	return strings.Count(host, ":") > 0 || regexp.MustCompile(`^[0-9.]+$`).MatchString(host)
}

func validAssetPath(value string) bool {
	return value != "" && strings.TrimSpace(value) == value &&
		!strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "\\") &&
		!strings.Contains(value, "..") && !strings.Contains(value, "\\") &&
		!strings.Contains(value, ":") && !strings.Contains(value, "//")
}

func rejectSecretKeys(values map[string]any) error {
	for key, value := range values {
		if secretKeyPart.MatchString(key) {
			return fmt.Errorf("%w: %q", ErrSecret, key)
		}
		if nested, ok := value.(map[string]any); ok {
			if err := rejectSecretKeys(nested); err != nil {
				return err
			}
		}
	}
	return nil
}

func rejectDuplicateJSONKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var scan func() error
	scan = func() error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := make(map[string]struct{})
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return err
				}
				key, ok := keyToken.(string)
				if !ok {
					return errors.New("invalid object key")
				}
				if _, duplicate := seen[key]; duplicate {
					return fmt.Errorf("duplicate JSON key %q", key)
				}
				seen[key] = struct{}{}
				if err := scan(); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		case '[':
			for decoder.More() {
				if err := scan(); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		default:
			return errors.New("unexpected JSON delimiter")
		}
	}
	if err := scan(); err != nil {
		return err
	}
	return ensureNoTrailingJSON(decoder)
}

func ensureNoTrailingJSON(decoder *json.Decoder) error {
	if _, err := decoder.Token(); err != io.EOF {
		return errors.New("trailing JSON data")
	}
	return nil
}

func strictDecoder(data []byte) *json.Decoder {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	return decoder
}
