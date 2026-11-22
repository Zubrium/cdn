package router

import core "github.com/Zubrium/cdn/core/presentation"

const moduleID = "router"

func ModuleDataDescriptor() core.ModuleDataDescriptor {
	return core.ModuleDataDescriptor{
		ModuleID: moduleID,
		Namespaces: []core.ModuleDataNamespace{{
			Name:       "router/routes",
			ScopeTypes: []string{"branch"},
			Fields: map[string]core.SelectorField{
				"record_type": {Type: core.SelectorString},
				"route_id":    {Type: core.SelectorString},
				"host":        {Type: core.SelectorString},
				"path":        {Type: core.SelectorString},
				"state":       {Type: core.SelectorString},
				// Locale CAS revisions are opaque durable metadata, not a
				// visitor-selectable index. They are encoded as one bounded
				// string because Core projections are scalar-only.
				"locale_revisions": {Type: core.SelectorString},
			},
		}},
	}
}
