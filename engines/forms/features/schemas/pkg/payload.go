package pkg

const PayloadVersion = 1

// PayloadV1 is the versioned JSON document persisted through Core engine data.
type PayloadV1 struct {
	ID      string         `json:"id"`
	Key     string         `json:"key"`
	Version int64          `json:"version"`
	Fields  map[string]any `json:"fields"`
}

// KeyIndexV1 maps a unique schema key to the schema's stable generated ID.
type KeyIndexV1 struct {
	ID string `json:"id"`
}
