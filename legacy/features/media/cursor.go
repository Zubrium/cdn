package media

import (
	"encoding/base64"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// cursor carries the position of the last item of the current page. The sort
// order decides which of the key fields is filled in.
type cursor struct {
	Sort      string    `json:"sort,omitempty"`
	CreatedAt time.Time `json:"created_at,omitempty"`
	Name      string    `json:"name,omitempty"`
	Size      int64     `json:"size,omitempty"`
	ID        uuid.UUID `json:"id,omitempty"`
}

func encodeCursor(c cursor) string {
	raw, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeCursor(raw string) (cursor, bool) {
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return cursor{}, false
	}
	var c cursor
	if err := json.Unmarshal(decoded, &c); err != nil {
		return cursor{}, false
	}
	if c.ID == uuid.Nil {
		return cursor{}, false
	}
	return c, true
}
