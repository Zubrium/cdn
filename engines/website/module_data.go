package website

import recorddata "github.com/Zubrium/cdn/pkg/contracts"

const moduleID = "website"

// RecordDescriptor declares Website-owned persistent namespaces. All
// records use TTL 0; operational expiry is modeled in the payload itself.
func RecordDescriptor() recorddata.RecordDescriptor {
	return recorddata.RecordDescriptor{
		OwnerID: moduleID,
		Namespaces: []recorddata.RecordNamespace{
			{Name: "features/catalog", ScopeTypes: []string{"global"}, Fields: map[string]recorddata.RecordSelectorField{
				"record_type": {Type: recorddata.RecordSelectorString}, "feature_id": {Type: recorddata.RecordSelectorString}, "version": {Type: recorddata.RecordSelectorString}, "state": {Type: recorddata.RecordSelectorString},
			}},
			{Name: "features/imports", ScopeTypes: []string{"job"}, Fields: map[string]recorddata.RecordSelectorField{
				"record_type": {Type: recorddata.RecordSelectorString}, "state": {Type: recorddata.RecordSelectorString}, "source_file_id": {Type: recorddata.RecordSelectorString}, "feature_id": {Type: recorddata.RecordSelectorString},
				"revision": {Type: recorddata.RecordSelectorNumber},
			}},
			{Name: "website/content-models", ScopeTypes: []string{"global"}, Fields: map[string]recorddata.RecordSelectorField{
				"record_type": {Type: recorddata.RecordSelectorString},
				"revision":    {Type: recorddata.RecordSelectorNumber}, "digest": {Type: recorddata.RecordSelectorString},
			}},
			{Name: "website/snapshots", ScopeTypes: []string{"global"}, Fields: map[string]recorddata.RecordSelectorField{
				"record_type": {Type: recorddata.RecordSelectorString}, "state": {Type: recorddata.RecordSelectorString}, "revision": {Type: recorddata.RecordSelectorNumber},
				"parent_id": {Type: recorddata.RecordSelectorString}, "page_id": {Type: recorddata.RecordSelectorString}, "snapshot_id": {Type: recorddata.RecordSelectorString}, "action": {Type: recorddata.RecordSelectorString},
				"lifecycle_state": {Type: recorddata.RecordSelectorString}, "digest": {Type: recorddata.RecordSelectorString},
			}},
			{Name: "website/page-templates", ScopeTypes: []string{"global"}, Fields: map[string]recorddata.RecordSelectorField{
				"record_type": {Type: recorddata.RecordSelectorString}, "template_id": {Type: recorddata.RecordSelectorString},
				"revision": {Type: recorddata.RecordSelectorNumber}, "digest": {Type: recorddata.RecordSelectorString},
			}},
			{Name: "website/builds", ScopeTypes: []string{"job"}, Fields: map[string]recorddata.RecordSelectorField{
				"record_type": {Type: recorddata.RecordSelectorString}, "state": {Type: recorddata.RecordSelectorString}, "snapshot_id": {Type: recorddata.RecordSelectorString},
				"revision": {Type: recorddata.RecordSelectorNumber},
			}},
			{Name: "website/media", ScopeTypes: []string{"job"}, Fields: map[string]recorddata.RecordSelectorField{
				"record_type": {Type: recorddata.RecordSelectorString}, "state": {Type: recorddata.RecordSelectorString}, "source_file_id": {Type: recorddata.RecordSelectorString},
				"profile_digest": {Type: recorddata.RecordSelectorString}, "processor_version": {Type: recorddata.RecordSelectorString},
				"revision": {Type: recorddata.RecordSelectorNumber},
			}},
		},
	}
}
