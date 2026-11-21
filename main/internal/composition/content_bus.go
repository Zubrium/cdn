package composition

import (
	"context"
	"encoding/json"
	"errors"

	contentapi "github.com/Zubrium/cdn/engines/content/presentation"
	"github.com/Zubrium/cdn/pkg/contracts"
)

// busPublishedContentReader is the Router-side typed adapter. It never keeps a
// direct Content component reference: every published-content read crosses the
// sealed process-local dispatcher and uses the compiled operation allowlist.
type busPublishedContentReader struct {
	dispatcher contracts.Dispatcher
}

func (reader busPublishedContentReader) ReadPublishedContent(ctx context.Context, pageSnapshotID, locale string) (contentapi.PublishedContent, error) {
	if reader.dispatcher == nil {
		return contentapi.PublishedContent{}, contracts.ErrDispatcherUnavailable
	}
	response, err := contracts.Call(ctx, reader.dispatcher, contracts.PublishedContentReadOperation(), contracts.PublishedContentReadRequest{
		PageSnapshotID: pageSnapshotID, Locale: locale,
	})
	if err != nil {
		return contentapi.PublishedContent{}, err
	}
	var content contentapi.PublishedContent
	if err := json.Unmarshal(response.Payload, &content); err != nil {
		return contentapi.PublishedContent{}, errors.New("invalid published content response")
	}
	return content, nil
}

var _ interface {
	ReadPublishedContent(context.Context, string, string) (contentapi.PublishedContent, error)
} = busPublishedContentReader{}
