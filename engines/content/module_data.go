package content

import core "github.com/Zubrium/cdn/core/presentation"

const moduleID = "content"

func ModuleDataDescriptor() core.ModuleDataDescriptor {
	return core.ModuleDataDescriptor{
		ModuleID: moduleID,
		// System-wide records/history and page-snapshot selections use distinct scopes.
		// Mutation batches preserve atomicity across both records.
		Namespaces: []core.ModuleDataNamespace{
			{Name: "content/workspaces", ScopeTypes: []string{"global", "page_snapshot"}, Fields: map[string]core.SelectorField{
				"record_type": {Type: core.SelectorString}, "content_revision": {Type: core.SelectorNumber},
			}},
		},
	}
}
