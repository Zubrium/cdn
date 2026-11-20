// Package archive implements bounded ZIP inspection and the fail-closed
// boundary for importing archives into Core.
package archive

import (
	"context"

	core "github.com/Zubrium/cdn/core/presentation"
)

const (
	DataModuleID       = "worker.archive"
	DataNamespace      = "imports"
	DataScopeType      = "task"
	DataPayloadVersion = 2
	OperationID        = "feature-import"
)

type Descriptor struct{}

func (Descriptor) ComponentDescriptor() core.ComponentDescriptor { return ComponentDescriptor() }
func (Descriptor) DataDescriptor() core.ModuleDataDescriptor     { return DataDescriptor() }

var _ core.ComponentDescriptorProvider = Descriptor{}

func ComponentDescriptor() core.ComponentDescriptor {
	return core.ComponentDescriptor{ID: DataModuleID, Kind: "worker", Version: 1,
		Operations: []core.ComponentOperation{{ID: OperationID, ContractVersion: 1}}}
}

func DataDescriptor() core.ModuleDataDescriptor {
	return core.ModuleDataDescriptor{ModuleID: DataModuleID, Namespaces: []core.ModuleDataNamespace{{
		Name: DataNamespace, ScopeTypes: []string{DataScopeType},
	}}}
}

// Request contains opaque Core identifiers prepared by trusted composition.
// It never accepts access groups, filenames, or storage paths from visitors.
type Request struct {
	TaskID       string `json:"job_id"` // The v1 wire key is retained; Task.ID is authoritative.
	SourceFileID string `json:"source_file_id"`
}

// Result's Ready flag remains false until the durable import state can prove
// that every output and target FileSet membership has been reconciled.
type Result struct {
	Ready   bool     `json:"ready"`
	FileIDs []string `json:"file_ids,omitempty"`
}

type Worker interface {
	Import(context.Context, Request) (Result, error)
}
