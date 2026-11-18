package forms

import core "github.com/Zubrium/cdn/core/presentation"

const moduleID = "forms"

func ModuleDataDescriptor() core.ModuleDataDescriptor {
	return core.ModuleDataDescriptor{
		ModuleID: moduleID,
		Namespaces: []core.ModuleDataNamespace{
			{Name: "forms/schemas", ScopeTypes: []string{"snapshot"}, Fields: map[string]core.SelectorField{
				"form_id": {Type: core.SelectorString}, "form_key": {Type: core.SelectorString},
				"record_type": {Type: core.SelectorString}, "revision": {Type: core.SelectorNumber},
			}},
			{Name: "forms/submissions", ScopeTypes: []string{"submission"}, Fields: map[string]core.SelectorField{
				"form_id": {Type: core.SelectorString}, "schema_version": {Type: core.SelectorNumber},
				"state": {Type: core.SelectorString}, "created_at": {Type: core.SelectorString},
			}},
			{Name: "forms/sessions", ScopeTypes: []string{"visitor"}, Fields: map[string]core.SelectorField{
				"snapshot_id": {Type: core.SelectorString}, "form_id": {Type: core.SelectorString},
				"schema_version": {Type: core.SelectorNumber}, "state": {Type: core.SelectorString},
			}},
		},
	}
}
