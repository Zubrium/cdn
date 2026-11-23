package forms

import recorddata "github.com/Zubrium/cdn/pkg/contracts"

const moduleID = "forms"

const globalScopeID = "global"

func RecordDescriptor() recorddata.RecordDescriptor {
	return recorddata.RecordDescriptor{
		OwnerID: moduleID,
		Namespaces: []recorddata.RecordNamespace{
			{Name: "forms/schemas", ScopeTypes: []string{"global"}, Fields: map[string]recorddata.RecordSelectorField{
				"form_id": {Type: recorddata.RecordSelectorString}, "form_key": {Type: recorddata.RecordSelectorString},
				"record_type": {Type: recorddata.RecordSelectorString}, "revision": {Type: recorddata.RecordSelectorNumber},
			}},
			{Name: "forms/submissions", ScopeTypes: []string{"global"}, Fields: map[string]recorddata.RecordSelectorField{
				"form_id": {Type: recorddata.RecordSelectorString}, "form_key": {Type: recorddata.RecordSelectorString},
				"schema_version": {Type: recorddata.RecordSelectorNumber}, "state": {Type: recorddata.RecordSelectorString},
				"created_at": {Type: recorddata.RecordSelectorString},
			}},
			{Name: "forms/sessions", ScopeTypes: []string{"global"}, Fields: map[string]recorddata.RecordSelectorField{
				"form_id": {Type: recorddata.RecordSelectorString}, "schema_version": {Type: recorddata.RecordSelectorNumber},
				"state": {Type: recorddata.RecordSelectorString},
			}},
		},
	}
}
