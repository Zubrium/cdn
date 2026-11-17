package website

import core "github.com/Zubrium/cdn/core/platform"

const moduleID = "website"

// ModuleDataDescriptor declares Website-owned persistent namespaces. All
// records use TTL 0; operational expiry is modeled in the payload itself.
func ModuleDataDescriptor() core.ModuleDataDescriptor {
	return core.ModuleDataDescriptor{
		ModuleID: moduleID,
		Namespaces: []core.ModuleDataNamespace{
			{Name: "features/catalog", ScopeTypes: []string{"global"}, Fields: map[string]core.SelectorField{
				"feature_id": {Type: core.SelectorString}, "version": {Type: core.SelectorString}, "state": {Type: core.SelectorString},
			}},
			{Name: "features/imports", ScopeTypes: []string{"job"}, Fields: map[string]core.SelectorField{
				"state": {Type: core.SelectorString}, "source_file_id": {Type: core.SelectorString}, "feature_id": {Type: core.SelectorString},
			}},
			{Name: "website/snapshots", ScopeTypes: []string{"site"}, Fields: map[string]core.SelectorField{
				"state": {Type: core.SelectorString}, "revision": {Type: core.SelectorNumber},
			}},
			{Name: "website/builds", ScopeTypes: []string{"job"}, Fields: map[string]core.SelectorField{
				"state": {Type: core.SelectorString}, "snapshot_id": {Type: core.SelectorString},
			}},
			{Name: "website/media", ScopeTypes: []string{"job"}, Fields: map[string]core.SelectorField{
				"state": {Type: core.SelectorString}, "source_file_id": {Type: core.SelectorString}, "profile": {Type: core.SelectorString},
			}},
		},
	}
}
