package sitebuilder

import "fmt"

// DataInputBinding connects one feature input port to a snapshot entity.
// Scalar ports use FieldID. Record and collection ports use Fields to map
// their declared output pointers to stable model field IDs.
type DataInputBinding struct {
	InputKey    string                  `json:"inputKey"`
	EntityID    string                  `json:"entityId"`
	FieldID     string                  `json:"fieldId,omitempty"`
	RelationIDs []string                `json:"relationIds,omitempty"`
	Fields      []DataInputFieldBinding `json:"fields,omitempty"`
	Lookup      *DataInputLookupBinding `json:"lookup,omitempty"`
	DetailSEO   *DetailSEOFieldBindings `json:"detailSEO,omitempty"`
}

// DetailSEOFieldBindings selects published fields from a unique detail-record
// lookup for the page document's head metadata. Values remain model content;
// this only stores stable structural field IDs in the snapshot configuration.
type DetailSEOFieldBindings struct {
	TitleFieldID         string `json:"titleFieldId,omitempty"`
	DescriptionFieldID   string `json:"descriptionFieldId,omitempty"`
	OGTitleFieldID       string `json:"ogTitleFieldId,omitempty"`
	OGDescriptionFieldID string `json:"ogDescriptionFieldId,omitempty"`
	OGImageFieldID       string `json:"ogImageFieldId,omitempty"`
}

type DataInputFieldBinding struct {
	InputPath   string   `json:"inputPath"`
	FieldID     string   `json:"fieldId"`
	RelationIDs []string `json:"relationIds,omitempty"`
}

type DataInputLookupBinding struct {
	Argument string `json:"argument"`
	FieldID  string `json:"fieldId"`
}

// ValidateDataInputBindings validates the complete placement binding set.
// argumentBindings is the placement's existing feature-argument map; a lookup
// is valid only when its argument is bound from a named route path capture.
func ValidateDataInputBindings(model DataModel, ports []DataInputPort, bindings []DataInputBinding, argumentBindings map[string]ArgumentBinding) error {
	if len(ports) == 0 {
		if len(bindings) != 0 {
			return fmt.Errorf("dataBindings cannot be declared when the feature has no dataInputs")
		}
		return nil
	}
	if err := validateDataInputPorts(ports); err != nil {
		return fmt.Errorf("dataInputs: %w", err)
	}
	if err := ValidateDataModel(model); err != nil {
		return fmt.Errorf("dataModel: %w", err)
	}

	portsByKey := make(map[string]DataInputPort, len(ports))
	for _, port := range ports {
		portsByKey[port.Key] = port
	}
	entitiesByID := make(map[string]DataModelEntity, len(model.Entities))
	for _, entity := range model.Entities {
		entitiesByID[entity.ID] = entity
	}

	bindingsByKey := make(map[string]DataInputBinding, len(bindings))
	for _, binding := range bindings {
		if _, exists := portsByKey[binding.InputKey]; !exists {
			return fmt.Errorf("dataBinding references undeclared input %q", binding.InputKey)
		}
		if _, exists := bindingsByKey[binding.InputKey]; exists {
			return fmt.Errorf("dataBinding for input %q is duplicated", binding.InputKey)
		}
		bindingsByKey[binding.InputKey] = binding
	}

	for _, port := range ports {
		binding, exists := bindingsByKey[port.Key]
		if !exists {
			return fmt.Errorf("dataInput %q has no placement binding", port.Key)
		}
		entity, exists := entitiesByID[binding.EntityID]
		if !exists {
			return fmt.Errorf("dataInput %q references unknown entity %q", port.Key, binding.EntityID)
		}
		if err := validateDataInputBinding(port, binding, entity, model, argumentBindings); err != nil {
			return err
		}
	}
	return nil
}

func validateDataInputBinding(port DataInputPort, binding DataInputBinding, entity DataModelEntity, model DataModel, argumentBindings map[string]ArgumentBinding) error {
	if binding.EntityID != entity.ID {
		return fmt.Errorf("dataInput %q entity binding is inconsistent", port.Key)
	}
	if binding.DetailSEO != nil {
		if port.Kind != "record" || binding.Lookup == nil {
			return fmt.Errorf("dataInput %q detailSEO requires a record input with a unique route lookup", port.Key)
		}
		if err := validateDetailSEOFieldBindings(port.Key, entity, *binding.DetailSEO); err != nil {
			return err
		}
	}
	if port.Kind == "scalar" {
		if binding.FieldID == "" {
			return fmt.Errorf("scalar dataInput %q requires one fieldId", port.Key)
		}
		if len(binding.Fields) != 0 {
			return fmt.Errorf("scalar dataInput %q must not declare field mappings", port.Key)
		}
		if binding.Lookup != nil {
			return fmt.Errorf("scalar dataInput %q cannot declare a route lookup", port.Key)
		}
		fieldEntity, wildcardCount, err := dataInputRelationTarget(model, entity.ID, binding.RelationIDs)
		if err != nil {
			return fmt.Errorf("scalar dataInput %q relation path: %w", port.Key, err)
		}
		if wildcardCount != 0 {
			return fmt.Errorf("scalar dataInput %q cannot traverse a multi-target relation", port.Key)
		}
		field, exists := dataModelFieldByID(fieldEntity, binding.FieldID)
		if !exists {
			return fmt.Errorf("scalar dataInput %q references unknown field %q in entity %q", port.Key, binding.FieldID, fieldEntity.ID)
		}
		if field.Type != port.Type {
			return fmt.Errorf("scalar dataInput %q type %q does not match model field type %q", port.Key, port.Type, field.Type)
		}
		if err := validateDataInputEnumOptions(port.Key, "", field, port.Values); err != nil {
			return err
		}
		return nil
	}

	if binding.FieldID != "" {
		return fmt.Errorf("composite dataInput %q must use field mappings, not fieldId", port.Key)
	}
	if len(binding.RelationIDs) != 0 {
		return fmt.Errorf("composite dataInput %q must declare relation paths on its field mappings", port.Key)
	}
	if port.Kind == "collection" && binding.Lookup != nil {
		return fmt.Errorf("collection dataInput %q cannot declare a route lookup", port.Key)
	}
	if port.Kind == "record" && binding.Lookup != nil {
		if err := validateDataInputLookup(port.Key, entity, *binding.Lookup, argumentBindings); err != nil {
			return err
		}
	}

	expectedPaths := make(map[string]DataInputField, len(port.Fields))
	for _, field := range port.Fields {
		segments, err := parseJSONPointer(field.Path)
		if err != nil {
			return fmt.Errorf("dataInput %q has invalid declared field path %q", port.Key, field.Path)
		}
		expectedPaths[pointerKey(segments)] = field
	}
	seenPaths := make(map[string]bool, len(binding.Fields))
	for _, mapping := range binding.Fields {
		segments, err := parseJSONPointer(mapping.InputPath)
		if err != nil {
			return fmt.Errorf("dataInput %q binding has invalid inputPath %q", port.Key, mapping.InputPath)
		}
		wildcardCount := 0
		for _, segment := range segments {
			if segment == "*" {
				wildcardCount++
			}
		}
		path := pointerKey(segments)
		declaredField, exists := expectedPaths[path]
		if !exists {
			return fmt.Errorf("dataInput %q binding has undeclared inputPath %q", port.Key, mapping.InputPath)
		}
		if seenPaths[path] {
			return fmt.Errorf("dataInput %q binding repeats inputPath %q", port.Key, mapping.InputPath)
		}
		seenPaths[path] = true
		if mapping.FieldID == "" {
			return fmt.Errorf("dataInput %q inputPath %q requires a fieldId", port.Key, mapping.InputPath)
		}
		fieldEntity, relationWildcardCount, err := dataInputRelationTarget(model, entity.ID, mapping.RelationIDs)
		if err != nil {
			return fmt.Errorf("dataInput %q inputPath %q relation path: %w", port.Key, mapping.InputPath, err)
		}
		if wildcardCount != relationWildcardCount {
			return fmt.Errorf("dataInput %q inputPath %q has %d array wildcards but its relation path requires %d", port.Key, mapping.InputPath, wildcardCount, relationWildcardCount)
		}
		modelField, exists := dataModelFieldByID(fieldEntity, mapping.FieldID)
		if !exists {
			return fmt.Errorf("dataInput %q inputPath %q references unknown field %q in entity %q", port.Key, mapping.InputPath, mapping.FieldID, fieldEntity.ID)
		}
		if modelField.Type != declaredField.Type {
			return fmt.Errorf("dataInput %q inputPath %q type %q does not match model field type %q", port.Key, mapping.InputPath, declaredField.Type, modelField.Type)
		}
		if err := validateDataInputEnumOptions(port.Key, mapping.InputPath, modelField, declaredField.Values); err != nil {
			return err
		}
	}
	for path, field := range expectedPaths {
		if !seenPaths[path] {
			return fmt.Errorf("dataInput %q is missing a mapping for inputPath %q", port.Key, field.Path)
		}
	}
	return nil
}

func validateDetailSEOFieldBindings(inputKey string, entity DataModelEntity, bindings DetailSEOFieldBindings) error {
	fields := []struct {
		name      string
		fieldID   string
		textTypes []string
	}{
		{name: "titleFieldId", fieldID: bindings.TitleFieldID, textTypes: []string{"text", "longText"}},
		{name: "descriptionFieldId", fieldID: bindings.DescriptionFieldID, textTypes: []string{"text", "longText"}},
		{name: "ogTitleFieldId", fieldID: bindings.OGTitleFieldID, textTypes: []string{"text", "longText"}},
		{name: "ogDescriptionFieldId", fieldID: bindings.OGDescriptionFieldID, textTypes: []string{"text", "longText"}},
		{name: "ogImageFieldId", fieldID: bindings.OGImageFieldID, textTypes: []string{"media"}},
	}
	configured := false
	for _, binding := range fields {
		if binding.fieldID == "" {
			continue
		}
		configured = true
		field, exists := dataModelFieldByID(entity, binding.fieldID)
		if !exists {
			return fmt.Errorf("dataInput %q detailSEO %s references unknown field %q on the bound entity", inputKey, binding.name, binding.fieldID)
		}
		validType := false
		for _, allowed := range binding.textTypes {
			if field.Type == allowed {
				validType = true
				break
			}
		}
		if !validType {
			return fmt.Errorf("dataInput %q detailSEO %s does not support model field type %q", inputKey, binding.name, field.Type)
		}
	}
	if !configured {
		return fmt.Errorf("dataInput %q detailSEO must select at least one metadata field", inputKey)
	}
	return nil
}

// validateDataInputEnumOptions ensures every option offered by a model field
// is representable by the feature's typed input contract. A model may expose a
// narrower set than the feature accepts, but it cannot add values the feature
// does not declare.
func validateDataInputEnumOptions(inputKey, inputPath string, modelField DataModelField, declaredValues []string) error {
	if modelField.Type != "enum" {
		return nil
	}
	allowed := make(map[string]struct{}, len(declaredValues))
	for _, value := range declaredValues {
		allowed[value] = struct{}{}
	}
	for _, option := range modelField.Options {
		if _, exists := allowed[option]; !exists {
			if inputPath == "" {
				return fmt.Errorf("scalar dataInput %q model enum option %q is not declared by the feature input", inputKey, option)
			}
			return fmt.Errorf("dataInput %q inputPath %q model enum option %q is not declared by the feature input", inputKey, inputPath, option)
		}
	}
	return nil
}

// dataInputRelationTarget follows a directed relation path from a binding's
// root entity and returns the entity that owns the mapped field. A wildcard is
// required in the manifest's output path for each multi-target edge.
func dataInputRelationTarget(model DataModel, sourceEntityID string, relationIDs []string) (DataModelEntity, int, error) {
	entities := make(map[string]DataModelEntity, len(model.Entities))
	for _, entity := range model.Entities {
		entities[entity.ID] = entity
	}
	current, ok := entities[sourceEntityID]
	if !ok {
		return DataModelEntity{}, 0, fmt.Errorf("unknown source entity %q", sourceEntityID)
	}
	relations := make(map[string]DataModelRelation, len(model.Relations))
	for _, relation := range model.Relations {
		relations[relation.ID] = relation
	}
	wildcards := 0
	for _, relationID := range relationIDs {
		relation, ok := relations[relationID]
		if !ok {
			return DataModelEntity{}, 0, fmt.Errorf("unknown relation %q", relationID)
		}
		if relation.FromEntityID != current.ID {
			return DataModelEntity{}, 0, fmt.Errorf("relation %q does not start at entity %q", relationID, current.ID)
		}
		if relation.Cardinality == "one-to-many" || relation.Cardinality == "many-to-many" {
			wildcards++
		}
		next, ok := entities[relation.ToEntityID]
		if !ok {
			return DataModelEntity{}, 0, fmt.Errorf("relation %q points to unknown entity %q", relationID, relation.ToEntityID)
		}
		current = next
	}
	return current, wildcards, nil
}

func validateDataInputLookup(inputKey string, entity DataModelEntity, lookup DataInputLookupBinding, argumentBindings map[string]ArgumentBinding) error {
	if !argumentPattern.MatchString(lookup.Argument) {
		return fmt.Errorf("dataInput %q lookup has an invalid argument key", inputKey)
	}
	argument, exists := argumentBindings[lookup.Argument]
	if !exists || argument.Source != "path" || argument.PathArg == "" {
		return fmt.Errorf("dataInput %q lookup argument %q must be bound from a named route path capture", inputKey, lookup.Argument)
	}
	if lookup.FieldID == "" {
		return fmt.Errorf("dataInput %q lookup requires a fieldId", inputKey)
	}
	field, exists := dataModelFieldByID(entity, lookup.FieldID)
	if !exists {
		return fmt.Errorf("dataInput %q lookup references unknown field %q in entity %q", inputKey, lookup.FieldID, entity.ID)
	}
	if !field.Unique {
		return fmt.Errorf("dataInput %q lookup field %q must be unique", inputKey, field.Key)
	}
	return nil
}

func dataModelFieldByID(entity DataModelEntity, fieldID string) (DataModelField, bool) {
	for _, field := range entity.Fields {
		if field.ID == fieldID {
			return field, true
		}
	}
	return DataModelField{}, false
}
