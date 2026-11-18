// Package repository defines the Core capabilities required by the Loader
// file feature. Implementations are supplied by composition, never SQL here.
package repository

import (
	"context"

	core "github.com/Zubrium/cdn/core/presentation"
)

type Identity = core.GroupAuthorizationCapability
type Files = core.StagedFilesCapability

type ModuleData interface {
	Get(context.Context, core.ModuleDataKey) (core.ModuleDataRecord, error)
	GetExpired(context.Context, core.ModuleDataKey) (core.ModuleDataRecord, error)
	Put(context.Context, core.ModuleDataKey, core.ModuleDataValue, int64) (core.ModuleDataRecord, error)
	Delete(context.Context, core.ModuleDataKey, int64) error
	Batch(context.Context, []core.ModuleDataMutation) error
	List(context.Context, string, string, string, string, []core.ModuleDataSelector, int) ([]core.ModuleDataKey, []core.ModuleDataRecord, string, error)
	ListExpired(context.Context, string, string, string, string, int) ([]core.ModuleDataKey, []core.ModuleDataRecord, string, error)
	MaxPageSize() int
	MaxBatchSize() int
	MaxPayloadBytes() int
	MaxIndexBytes() int
	MaxBatchBytes() int
}
