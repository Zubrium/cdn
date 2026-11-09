package sitebuilder

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
)

// packReferences traverses only manifest media fields and explicit site asset
// properties. Unrelated strings, entry IDs, token IDs and manifests are never
// interpreted as file IDs even if their text happens to be a valid UUID.
type packReferences struct {
	ids          map[string]uuid.UUID
	replacements map[string]string
	targets      map[string]bool
	clearTarget  string
	cleared      bool
}

func newPackReferences(replacements map[string]string) *packReferences {
	refs := &packReferences{ids: map[string]uuid.UUID{}, replacements: replacements, targets: map[string]bool{}}
	for _, value := range replacements {
		refs.targets[value] = true
	}
	return refs
}

func (r *packReferences) mediaID(value string) (string, error) {
	if value == "" {
		return value, nil
	}
	if r.clearTarget != "" {
		if value == r.clearTarget {
			r.cleared = true
			return "", nil
		}
		return value, nil
	}
	id, err := uuid.Parse(value)
	if err != nil {
		return "", fmt.Errorf("%w: invalid media ID", ErrInvalid)
	}
	// Encoded UUIDs must match canonical form so lookup and rewrite agree.
	canonical := id.String()
	if r.replacements == nil {
		r.ids[canonical] = id
		return value, nil
	}
	if mapped, ok := r.replacements[canonical]; ok {
		return mapped, nil
	}
	// A schema using allOf can traverse the same value a second time.
	if r.targets[canonical] {
		return canonical, nil
	}
	return "", fmt.Errorf("%w: required media file %s is absent from sitepack", ErrInvalid, canonical)
}

func localPackRef(root map[string]any, pointer string) (map[string]any, bool) {
	if pointer == "#" {
		return root, true
	}
	if !strings.HasPrefix(pointer, "#/") {
		return nil, false
	}
	var current any = root
	for _, part := range strings.Split(strings.TrimPrefix(pointer, "#/"), "/") {
		part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		node, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = node[part]
		if !ok {
			return nil, false
		}
	}
	node, ok := current.(map[string]any)
	return node, ok
}

func (r *packReferences) rewriteSchemaValue(schemaRaw, data json.RawMessage) (json.RawMessage, error) {
	var root map[string]any
	if err := json.Unmarshal(schemaRaw, &root); err != nil {
		return nil, err
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if err := requireJSONEnd(decoder); err != nil {
		return nil, err
	}
	var walk func(map[string]any, any, int) (any, error)
	walk = func(schema map[string]any, value any, depth int) (any, error) {
		if schema == nil || value == nil {
			return value, nil
		}
		if depth > 128 {
			return nil, fmt.Errorf("%w: media schema nesting too deep", ErrInvalid)
		}
		if ref, ok := schema["$ref"].(string); ok {
			referenced, ok := localPackRef(root, ref)
			if !ok {
				return nil, fmt.Errorf("%w: unsupported media schema reference", ErrInvalid)
			}
			next, err := walk(referenced, value, depth+1)
			if err != nil {
				return nil, err
			}
			value = next
		}
		if schema["format"] == "media" {
			text, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("%w: media value must be a string", ErrInvalid)
			}
			return r.mediaID(text)
		}
		if object, ok := value.(map[string]any); ok {
			properties, _ := schema["properties"].(map[string]any)
			for key, item := range object {
				child, _ := properties[key].(map[string]any)
				if child == nil {
					child, _ = schema["additionalProperties"].(map[string]any)
				}
				if child != nil {
					next, err := walk(child, item, depth+1)
					if err != nil {
						return nil, err
					}
					object[key] = next
				}
			}
		}
		if array, ok := value.([]any); ok {
			itemSchema, _ := schema["items"].(map[string]any)
			if itemSchema != nil {
				for index, item := range array {
					next, err := walk(itemSchema, item, depth+1)
					if err != nil {
						return nil, err
					}
					array[index] = next
				}
			}
		}
		// Each branch is used only to find declared media; data validation is
		// performed separately against the complete schema.
		for _, key := range []string{"allOf", "anyOf", "oneOf"} {
			children, _ := schema[key].([]any)
			for _, child := range children {
				node, _ := child.(map[string]any)
				if node != nil {
					next, err := walk(node, value, depth+1)
					if err != nil {
						return nil, err
					}
					value = next
				}
			}
		}
		return value, nil
	}
	transformed, err := walk(root, value, 0)
	if err != nil {
		return nil, err
	}
	return json.Marshal(transformed)
}

func (r *packReferences) metadata(value *SiteMetadata) error {
	if value == nil {
		return nil
	}
	var err error
	if value.FaviconMediaID, err = r.mediaID(value.FaviconMediaID); err != nil {
		return err
	}
	if value.OGImageMediaID, err = r.mediaID(value.OGImageMediaID); err != nil {
		return err
	}
	for key, page := range value.Pages {
		if page.FaviconMediaID != nil {
			id, e := r.mediaID(*page.FaviconMediaID)
			if e != nil {
				return e
			}
			page.FaviconMediaID = &id
		}
		if page.OGImageMediaID != nil {
			id, e := r.mediaID(*page.OGImageMediaID)
			if e != nil {
				return e
			}
			page.OGImageMediaID = &id
		}
		value.Pages[key] = page
	}
	return nil
}

func (r *packReferences) theme(value *DesignSystem) error {
	if value == nil {
		return nil
	}
	var err error
	if value.BrandLogoID, err = r.mediaID(value.BrandLogoID); err != nil {
		return err
	}
	for i := range value.Fonts {
		if value.Fonts[i].MediaID, err = r.mediaID(value.Fonts[i].MediaID); err != nil {
			return err
		}
	}
	return nil
}

func (r *packReferences) transform(index *sitepackIndex) error {
	manifests := map[string]Manifest{}
	for _, build := range index.Payload.Builds {
		if _, exists := manifests[build.Key]; exists {
			return fmt.Errorf("%w: duplicate pinned feature", ErrInvalid)
		}
		var manifest Manifest
		if err := json.Unmarshal(build.Manifest, &manifest); err != nil {
			return err
		}
		if manifest.Key != build.Key || manifest.Version != build.Version {
			return fmt.Errorf("%w: feature identity mismatch", ErrInvalid)
		}
		manifests[build.Key] = manifest
	}
	if err := r.metadata(index.Payload.Metadata); err != nil {
		return err
	}
	if err := r.theme(index.Payload.DesignSystem); err != nil {
		return err
	}
	for pi := range index.Payload.Config.Pages {
		for li := range index.Payload.Config.Pages[pi].Placements {
			placement := &index.Payload.Config.Pages[pi].Placements[li]
			manifest, ok := manifests[placement.Build.FeatureKey]
			if !ok {
				return fmt.Errorf("%w: unknown feature in placement", ErrInvalid)
			}
			var arguments struct {
				Properties map[string]json.RawMessage `json:"properties"`
			}
			if err := json.Unmarshal(manifest.Arguments, &arguments); err != nil {
				return err
			}
			for key, binding := range placement.Arguments {
				if binding.Source != "literal" || len(binding.Value) == 0 {
					continue
				}
				schema, ok := arguments.Properties[key]
				if !ok {
					continue
				}
				next, err := r.rewriteSchemaValue(schema, binding.Value)
				if err != nil {
					return fmt.Errorf("placement %s argument %s: %w", placement.ID, key, err)
				}
				binding.Value = next
				placement.Arguments[key] = binding
			}
		}
	}
	return nil
}

func (r *packReferences) list() []uuid.UUID {
	out := make([]uuid.UUID, 0, len(r.ids))
	for _, id := range r.ids {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}
func checkPackReferences(index *sitepackIndex) ([]uuid.UUID, error) {
	r := newPackReferences(nil)
	if err := r.transform(index); err != nil {
		return nil, err
	}
	return r.list(), nil
}
func rewritePackReferences(index *sitepackIndex, ids map[string]string) error {
	if ids == nil {
		return errors.New("missing sitepack media mapping")
	}
	return newPackReferences(ids).transform(index)
}
