package pkg

const PayloadVersion = 1

// PayloadV1 is the versioned JSON document persisted through Core engine data.
type PayloadV1 struct {
	ID      string         `json:"id"`
	Key     string         `json:"key"`
	Version int64          `json:"version"`
	Fields  map[string]any `json:"fields"`
}
