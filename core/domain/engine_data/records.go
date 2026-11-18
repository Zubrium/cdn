// Package engine_data defines the opaque record storage contract shared by
// registered engines. Core never interprets an engine's payload fields.
package engine_data

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

var (
	ErrNotFound = errors.New("engine data record not found")
	ErrConflict = errors.New("engine data revision conflict")
	ErrInvalid  = errors.New("invalid engine data operation")
	ErrLimit    = errors.New("engine data limit exceeded")
)

type Key struct {
	Namespace string
	ScopeType string
	ScopeID   string
	RecordKey string
}

type Value struct {
	Payload        json.RawMessage
	PayloadVersion int
	IndexData      json.RawMessage
	TTL            time.Duration
}

type Record struct {
	Payload        json.RawMessage
	PayloadVersion int
	IndexData      json.RawMessage
	Revision       int64
	ExpiresAt      *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type Mutation struct {
	Key              Key
	ExpectedRevision int64
	Value            *Value
}

// ContainmentSelector is a bounded set of parameterized JSONB containment
// alternatives prepared by the application layer.
type ContainmentSelector struct{ Alternatives []json.RawMessage }

type Repository interface {
	Get(context.Context, string, Key) (Record, error)
	GetExpired(context.Context, string, Key) (Record, error)
	Put(context.Context, string, Key, Value, int64, int, int) (Record, error)
	Delete(context.Context, string, Key, int64) error
	Batch(context.Context, string, []Mutation, int, int, int, int) error
	List(context.Context, string, string, string, string, string, []ContainmentSelector, int) ([]Key, []Record, string, error)
	ListExpired(context.Context, string, string, string, string, string, int) ([]Key, []Record, string, error)
}
