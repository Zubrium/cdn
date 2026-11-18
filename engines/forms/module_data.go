package forms

import core "github.com/Zubrium/cdn/core/presentation"

const moduleID = "forms"

func ModuleDataDescriptor() core.ModuleDataDescriptor {
	return core.ModuleDataDescriptor{
		ModuleID: moduleID,
		Namespaces: []core.ModuleDataNamespace{
			{Name: "forms/schemas", ScopeTypes: []string{"snapshot"}, Fields: map[string]core.SelectorField{
				"form_id": {Type: core.SelectorString}, "revision": {Type: core.SelectorNumber},
			}},
			{Name: "forms/submissions", ScopeTypes: []string{"submission"}, Fields: map[string]core.SelectorField{
				"form_id": {Type: core.SelectorString}, "state": {Type: core.SelectorString}, "created_at": {Type: core.SelectorString},
			}},
		},
	}
}
