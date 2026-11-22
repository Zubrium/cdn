package pkg

// PayloadV2 is the global-scope schema document. Version 1 remains declared
// for the historical payload format; no migration or upcaster is provided.
const PayloadVersion = 2

// PayloadV1 is the versioned JSON document persisted through Core engine data.
type PayloadV1 struct {
	ID      string         `json:"id"`
	Key     string         `json:"key"`
	Version int64          `json:"version"`
	Fields  map[string]any `json:"fields"`
}

// PayloadV2 stores one global form schema. It deliberately carries no
// snapshot_id: form identity is the globally unique key and generated ID.
type PayloadV2 struct {
	ID      string         `json:"id"`
	Key     string         `json:"key"`
	Version int64          `json:"version"`
	Fields  map[string]any `json:"fields"`
}

// KeyIndexV1 maps a unique schema key to the schema's stable generated ID.
type KeyIndexV1 struct {
	ID string `json:"id"`
}

// KeyIndexV2 maps a globally unique schema key to the schema's stable ID.
type KeyIndexV2 struct {
	ID string `json:"id"`
}
