package content

import core "github.com/Zubrium/cdn/core/presentation"

const moduleID = "content"

func ModuleDataDescriptor() core.ModuleDataDescriptor {
	return core.ModuleDataDescriptor{
		ModuleID: moduleID,
		// The current workspace, publication history, and fork marker share one
		// namespace and snapshot scope so Core Batch can CAS them atomically.
		Namespaces: []core.ModuleDataNamespace{
			{Name: "content/workspaces", ScopeTypes: []string{"snapshot"}, Fields: map[string]core.SelectorField{
				"record_type": {Type: core.SelectorString}, "content_revision": {Type: core.SelectorNumber},
			}},
		},
	}
}
