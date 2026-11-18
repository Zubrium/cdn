package content

import core "github.com/Zubrium/cdn/core/presentation"

const moduleID = "content"

func ModuleDataDescriptor() core.ModuleDataDescriptor {
	return core.ModuleDataDescriptor{
		ModuleID: moduleID,
		Namespaces: []core.ModuleDataNamespace{
			{Name: "content/models", ScopeTypes: []string{"snapshot"}, Fields: map[string]core.SelectorField{
				"model_id": {Type: core.SelectorString}, "model_key": {Type: core.SelectorString},
			}},
			{Name: "content/records", ScopeTypes: []string{"snapshot"}, Fields: map[string]core.SelectorField{
				"model_id": {Type: core.SelectorString}, "record_id": {Type: core.SelectorString},
			}},
			{Name: "content/bindings", ScopeTypes: []string{"snapshot"}, Fields: map[string]core.SelectorField{
				"feature_id": {Type: core.SelectorString}, "placement_id": {Type: core.SelectorString},
			}},
		},
	}
}
