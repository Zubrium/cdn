package sitebuilder

import "encoding/json"

// retiredSitepackContract detects the previous per-feature CMS payload before
// strict decoding so the client receives a direct incompatibility diagnostic.
// Sitepack format 2 carries only snapshot structure and model-backed content.
func retiredSitepackContract(raw []byte) bool {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		return false
	}
	for _, key := range []string{"content", "publishedContent", "contentRelations", "drafts"} {
		if _, exists := root[key]; exists {
			return true
		}
	}
	return false
}
