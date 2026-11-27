package contracts

type ContentPublicationIntentList struct {
	After string `json:"after,omitempty"`
	Limit int    `json:"limit"`
}

type ContentPublicationIntentItem struct {
	Publication   PublicationPublishRequest `json:"publication"`
	StoreRevision int64                     `json:"store_revision"`
}

type ContentPublicationIntentPage struct {
	Items      []ContentPublicationIntentItem `json:"items"`
	NextCursor string                         `json:"next_cursor,omitempty"`
}

type ContentPublicationIntentAck struct {
	Locale          string `json:"locale"`
	ContentRevision int64  `json:"content_revision"`
	StoreRevision   int64  `json:"store_revision"`
	RunID           string `json:"run_id"`
}

func ListContentPublicationIntentsOperation() Operation[ContentPublicationIntentList, ContentPublicationIntentPage] {
	return Operation[ContentPublicationIntentList, ContentPublicationIntentPage]{
		Key: Key{ID: "engine.content.publication-intents.list", Version: 1},
	}
}

func AcknowledgeContentPublicationIntentOperation() Operation[ContentPublicationIntentAck, struct{}] {
	return Operation[ContentPublicationIntentAck, struct{}]{
		Key: Key{ID: "engine.content.publication-intents.acknowledge", Version: 1},
	}
}
