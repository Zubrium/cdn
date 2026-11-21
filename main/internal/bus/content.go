package bus

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/Zubrium/cdn/pkg/contracts"
)

var ErrInvalidPublishedContentPayload = errors.New("invalid published content payload")

// PublishedContentReader is implemented only by the owning Content component.
// The payload remains opaque to the bus and to the Router transport.
type PublishedContentReader func(context.Context, string, string) (json.RawMessage, error)

func RegisterPublishedContent(dispatcher *Dispatcher, reader PublishedContentReader) error {
	if dispatcher == nil || reader == nil {
		return contracts.ErrInvalidOperation
	}
	return Register(dispatcher, contracts.PublishedContentReadOperation(), func(ctx context.Context, request contracts.PublishedContentReadRequest) (contracts.PublishedContentReadResponse, error) {
		if err := contracts.PublishedContentReadOperation().Validate(request); err != nil {
			return contracts.PublishedContentReadResponse{}, err
		}
		payload, err := reader(ctx, request.PageSnapshotID, request.Locale)
		if err != nil {
			return contracts.PublishedContentReadResponse{}, err
		}
		if len(payload) == 0 || !json.Valid(payload) {
			return contracts.PublishedContentReadResponse{}, ErrInvalidPublishedContentPayload
		}
		return contracts.PublishedContentReadResponse{Payload: payload}, nil
	})
}
