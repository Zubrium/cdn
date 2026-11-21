package runtime

import (
	"errors"
	"fmt"
	"strings"
)

var ErrBinding = errors.New("invalid model binding")

// Binding is a direct structural binding from a placement port to one stable
// model entity field. /site owns this value; /content owns the records and
// their order. Selections, relation paths and relationIds never cross this
// boundary.
type Binding struct {
	Port        string
	EntityKey   string
	Field       string
	OutputType  string
	RelationIDs []string
	Selection   []string
}

// ValidateBindings checks every binding against the pinned feature manifest.
// It fails closed with no notion of a partial save: the caller applies the
// structural snapshot only after this function succeeds.
func ValidateBindings(manifest Manifest, bindings map[string]Binding) error {
	ports := make(map[string]dataInputPort, len(manifest.DataInputs))
	for _, raw := range manifest.DataInputs {
		port, err := parseDataInput(raw)
		if err != nil {
			return err
		}
		if _, duplicate := ports[port.Key]; duplicate {
			return fmt.Errorf("%w: duplicate port %q", ErrManifest, port.Key)
		}
		ports[port.Key] = port
	}
	if len(bindings) > len(ports) {
		return fmt.Errorf("%w: more bindings than declared ports", ErrBinding)
	}
	for portKey, binding := range bindings {
		if portKey == "" || strings.TrimSpace(portKey) != portKey {
			return fmt.Errorf("%w: empty port", ErrBinding)
		}
		if binding.Port != portKey {
			return fmt.Errorf("%w: port %q does not match binding key", ErrBinding, portKey)
		}
		if len(binding.RelationIDs) > 0 || len(binding.Selection) > 0 {
			return fmt.Errorf("%w: relation traversal and record selection are not structural bindings", ErrBinding)
		}
		if secretKeyPart.MatchString(binding.EntityKey) || secretKeyPart.MatchString(binding.Field) {
			return fmt.Errorf("%w: secret-like binding key", ErrSecret)
		}
		if strings.TrimSpace(binding.EntityKey) == "" || strings.TrimSpace(binding.Field) == "" {
			return fmt.Errorf("%w: entity and field are required for %q", ErrBinding, portKey)
		}
		port, exists := ports[portKey]
		if !exists {
			return fmt.Errorf("%w: unknown port %q", ErrBinding, portKey)
		}
		if !portMatchesField(port, binding.Field, binding.OutputType) {
			return fmt.Errorf("%w: incompatible field %q for port %q", ErrBinding, binding.Field, portKey)
		}
	}
	return nil
}

type dataInputPort struct {
	Key    string
	Kind   string
	Type   string
	Fields map[string]string
}

func parseDataInput(raw []byte) (dataInputPort, error) {
	var input struct {
		Key    string `json:"key"`
		Kind   string `json:"kind"`
		Type   string `json:"type"`
		Fields []struct {
			Path string `json:"path"`
			Type string `json:"type"`
		} `json:"fields"`
	}
	decoder := strictDecoder(raw)
	if len(raw) == 0 || decoder.Decode(&input) != nil {
		return dataInputPort{}, fmt.Errorf("%w: port", ErrManifest)
	}
	if input.Type != "" && !validDataFieldType(input.Type) {
		return dataInputPort{}, fmt.Errorf("%w: port type", ErrManifest)
	}
	fields := make(map[string]string, len(input.Fields))
	for _, field := range input.Fields {
		if field.Path == "" || !validDataFieldType(field.Type) {
			return dataInputPort{}, fmt.Errorf("%w: port field", ErrManifest)
		}
		fields[field.Path] = field.Type
	}
	return dataInputPort{Key: input.Key, Kind: input.Kind, Type: input.Type, Fields: fields}, nil
}

func portMatchesField(port dataInputPort, field, outputType string) bool {
	if !validDataFieldType(outputType) {
		return false
	}
	if declared, exists := port.Fields[field]; exists {
		return declared == outputType
	}
	// A port without declared fields binds only its own scalar output.
	return len(port.Fields) == 0 && port.Kind == "scalar" && port.Type == outputType
}

func validDataFieldType(value string) bool {
	switch value {
	case "text", "longText", "integer", "number", "boolean", "date", "dateTime", "url", "email", "enum", "media":
		return true
	default:
		return false
	}
}
