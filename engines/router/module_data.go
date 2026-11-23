package router

import recorddata "github.com/Zubrium/cdn/pkg/contracts"

const moduleID = "router"

func RecordDescriptor() recorddata.RecordDescriptor {
	return recorddata.RecordDescriptor{
		OwnerID: moduleID,
		Namespaces: []recorddata.RecordNamespace{{
			Name:       "router/routes",
			ScopeTypes: []string{"branch"},
			Fields: map[string]recorddata.RecordSelectorField{
				"record_type": {Type: recorddata.RecordSelectorString},
				"route_id":    {Type: recorddata.RecordSelectorString},
				"host":        {Type: recorddata.RecordSelectorString},
				"path":        {Type: recorddata.RecordSelectorString},
				"state":       {Type: recorddata.RecordSelectorString},
				// Locale CAS revisions are opaque durable metadata, not a
				// visitor-selectable index. They are encoded as one bounded
				// string because Core projections are scalar-only.
				"locale_revisions": {Type: recorddata.RecordSelectorString},
			},
		}},
	}
}
