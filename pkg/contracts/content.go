package contracts

import (
	"encoding/json"
	"errors"
	"strings"
)

var ErrInvalidPublishedContentRequest = errors.New("invalid published content request")

type PublishedContentReadRequest struct {
	PageSnapshotID string `json:"page_snapshot_id"`
	Locale         string `json:"locale"`
}

type PublishedContentReadResponse struct {
	Payload json.RawMessage `json:"payload"`
}

func PublishedContentReadOperation() Operation[PublishedContentReadRequest, PublishedContentReadResponse] {
	return Operation[PublishedContentReadRequest, PublishedContentReadResponse]{
		Key: Key{ID: "content.published.read", Version: 1},
		Validate: func(request PublishedContentReadRequest) error {
			if request.PageSnapshotID == "" || len(request.PageSnapshotID) > 128 || strings.TrimSpace(request.PageSnapshotID) != request.PageSnapshotID {
				return ErrInvalidPublishedContentRequest
			}
			if request.Locale == "" || len(request.Locale) > 35 || strings.TrimSpace(request.Locale) != request.Locale {
				return ErrInvalidPublishedContentRequest
			}
			for _, char := range request.Locale {
				if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '-' || char == '_' {
					continue
				}
				return ErrInvalidPublishedContentRequest
			}
			return nil
		},
	}
}
