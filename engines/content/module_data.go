package content

import recorddata "github.com/Zubrium/cdn/pkg/contracts"

const moduleID = "content"

func RecordDescriptor() recorddata.RecordDescriptor {
	return recorddata.RecordDescriptor{
		OwnerID: moduleID,
		// System-wide records/history and page-snapshot selections use distinct scopes.
		// Mutation batches preserve atomicity across both records.
		Namespaces: []recorddata.RecordNamespace{
			{Name: "content/workspaces", ScopeTypes: []string{"global", "page_snapshot"}, Fields: map[string]recorddata.RecordSelectorField{
				"record_type": {Type: recorddata.RecordSelectorString}, "content_revision": {Type: recorddata.RecordSelectorNumber},
				"locale": {Type: recorddata.RecordSelectorString}, "page_snapshot_id": {Type: recorddata.RecordSelectorString},
			}},
		},
	}
}
