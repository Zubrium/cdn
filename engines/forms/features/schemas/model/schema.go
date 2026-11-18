package model

// Schema is the engine-owned representation of one form definition.
type Schema struct {
	ID      string         `json:"id"`
	Key     string         `json:"key"`
	Version int64          `json:"version"`
	Fields  map[string]any `json:"fields"`
}
