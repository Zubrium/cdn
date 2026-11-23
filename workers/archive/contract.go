// Package archive implements bounded ZIP inspection and the fail-closed
// boundary for importing archives into Core.
package archive

import (
	"context"
	recorddata "github.com/Zubrium/cdn/pkg/contracts"
)

const (
	DataModuleID       = "worker.archive"
	DataNamespace      = "imports"
	DataScopeType      = "task"
	DataPayloadVersion = 2
	OperationID        = "feature-import"
)

type Descriptor struct{}

func (Descriptor) ComponentDescriptor() recorddata.ComponentDescriptor { return ComponentDescriptor() }
func (Descriptor) DataDescriptor() recorddata.RecordDescriptor         { return DataDescriptor() }

var (
	_ recorddata.ComponentDescriptorProvider = Descriptor{}
)

func ComponentDescriptor() recorddata.ComponentDescriptor {
	return recorddata.ComponentDescriptor{ID: DataModuleID, Kind: "worker", Version: 1,
		Operations: []recorddata.ComponentOperation{{ID: OperationID, ContractVersion: 1}}}
}

func DataDescriptor() recorddata.RecordDescriptor {
	return recorddata.RecordDescriptor{OwnerID: DataModuleID, Namespaces: []recorddata.RecordNamespace{{
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
