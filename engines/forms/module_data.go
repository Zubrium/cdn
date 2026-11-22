package forms

import core "github.com/Zubrium/cdn/core/presentation"

const moduleID = "forms"

const globalScopeID = "global"

func ModuleDataDescriptor() core.ModuleDataDescriptor {
	return core.ModuleDataDescriptor{
		ModuleID: moduleID,
		Namespaces: []core.ModuleDataNamespace{
			{Name: "forms/schemas", ScopeTypes: []string{"global"}, Fields: map[string]core.SelectorField{
				"form_id": {Type: core.SelectorString}, "form_key": {Type: core.SelectorString},
				"record_type": {Type: core.SelectorString}, "revision": {Type: core.SelectorNumber},
			}},
			{Name: "forms/submissions", ScopeTypes: []string{"global"}, Fields: map[string]core.SelectorField{
				"form_id": {Type: core.SelectorString}, "form_key": {Type: core.SelectorString},
				"schema_version": {Type: core.SelectorNumber}, "state": {Type: core.SelectorString},
				"created_at": {Type: core.SelectorString},
			}},
			{Name: "forms/sessions", ScopeTypes: []string{"global"}, Fields: map[string]core.SelectorField{
				"form_id": {Type: core.SelectorString}, "schema_version": {Type: core.SelectorNumber},
				"state": {Type: core.SelectorString},
			}},
		},
	}
}
