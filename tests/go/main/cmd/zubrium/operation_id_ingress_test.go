package main

import (
	"strings"
	"testing"
)

func schemaReferencesOperationID(contract openAPIDocument, schema openAPIBuildSchema, seen map[string]bool) bool {
	if _, exists := schema.Properties["operation_id"]; exists {
		return true
	}
	if _, exists := schema.Properties["operationId"]; exists {
		return true
	}
	if schema.Ref != "" {
		const prefix = "#/components/schemas/"
		name := strings.TrimPrefix(schema.Ref, prefix)
		if strings.HasPrefix(schema.Ref, prefix) && !seen[name] {
			seen[name] = true
			if resolved, ok := contract.Components.Schemas[name]; ok {
				if schemaReferencesOperationID(contract, resolved, seen) {
					return true
				}
			}
		}
	}
	for _, nested := range append(append(schema.AllOf, schema.OneOf...), schema.AnyOf...) {
		if schemaReferencesOperationID(contract, nested, seen) {
			return true
		}
	}
	return false
}

func TestHTTPRequestSchemasNeverAcceptDataBusOperationID(t *testing.T) {
	for _, name := range []string{
		"admin-identity.yaml", "admin-notifications.yaml", "content.yaml", "coordinator.yaml",
		"files.yaml", "forms.yaml", "router.yaml", "website.yaml",
	} {
		t.Run(name, func(t *testing.T) {
			contract := loadOpenAPIContract(t, name)
			for path, item := range contract.Paths {
				for method, operation := range item.operations() {
					if operation == nil || operation.RequestBody == nil {
						continue
					}
					for mediaType, content := range operation.RequestBody.Content {
						if schemaReferencesOperationID(contract, content.Schema, map[string]bool{}) {
							t.Errorf("%s %s request body (%s) exposes operation_id/operationId; DataBus operation IDs are compiled server-side", method, path, mediaType)
						}
					}
				}
			}
		})
	}
}
