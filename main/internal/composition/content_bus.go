package composition

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/Zubrium/cdn/pkg/contracts"
)

// busPublishedContentReader is the Router-side typed adapter. It never keeps a
// direct Content component reference: every published-content read crosses the
// sealed process-local dispatcher and uses the compiled operation allowlist.
type busPublishedContentReader struct {
	dispatcher contracts.Dispatcher
}

func (reader busPublishedContentReader) ReadPublishedContent(ctx context.Context, pageSnapshotID, locale string) (contracts.PublishedContent, error) {
	if reader.dispatcher == nil {
		return contracts.PublishedContent{}, contracts.ErrDispatcherUnavailable
	}
	response, err := contracts.Call(ctx, reader.dispatcher, contracts.PublishedContentReadOperation(), contracts.PublishedContentReadRequest{
		PageSnapshotID: pageSnapshotID, Locale: locale,
	})
	if err != nil {
		return contracts.PublishedContent{}, err
	}
	var content contracts.PublishedContent
	if err := json.Unmarshal(response.Payload, &content); err != nil {
		return contracts.PublishedContent{}, errors.New("invalid published content response")
	}
	return content, nil
}

var _ interface {
	ReadPublishedContent(context.Context, string, string) (contracts.PublishedContent, error)
} = busPublishedContentReader{}
