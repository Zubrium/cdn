// Package repository defines the Core capabilities required by the Loader
// file feature. Implementations are supplied by composition, never SQL here.
package repository

import (
	"context"
	recorddata "github.com/Zubrium/cdn/pkg/contracts"

	core "github.com/Zubrium/cdn/core/presentation"
)

type Identity = core.GroupAuthorizationCapability
type Files = core.StagedFilesCapability

type ModuleData interface {
	Get(context.Context, recorddata.RecordKey) (recorddata.Record, error)
	GetExpired(context.Context, recorddata.RecordKey) (recorddata.Record, error)
	Put(context.Context, recorddata.RecordKey, recorddata.RecordValue, int64) (recorddata.Record, error)
	Delete(context.Context, recorddata.RecordKey, int64) error
	Batch(context.Context, []recorddata.RecordMutation) error
	List(context.Context, string, string, string, string, []recorddata.RecordSelector, int) ([]recorddata.RecordKey, []recorddata.Record, string, error)
	ListExpired(context.Context, string, string, string, string, int) ([]recorddata.RecordKey, []recorddata.Record, string, error)
	MaxPageSize() int
	MaxBatchSize() int
	MaxPayloadBytes() int
	MaxIndexBytes() int
	MaxBatchBytes() int
}
