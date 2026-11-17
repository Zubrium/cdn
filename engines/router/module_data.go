package router

import core "github.com/Zubrium/cdn/core/platform"

const moduleID = "router"

func ModuleDataDescriptor() core.ModuleDataDescriptor {
	return core.ModuleDataDescriptor{
		ModuleID: moduleID,
		Namespaces: []core.ModuleDataNamespace{{
			Name: "router/routes",
			ScopeTypes: []string{"branch"},
			Fields: map[string]core.SelectorField{
				"branch_id": {Type: core.SelectorString},
				"host":      {Type: core.SelectorString},
				"path":      {Type: core.SelectorString},
				"state":     {Type: core.SelectorString},
			},
		}},
	}
}
