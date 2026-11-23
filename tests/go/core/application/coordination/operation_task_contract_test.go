package coordinator

import (
	"encoding/json"
	"testing"

	corecoord "github.com/Zubrium/cdn/core/domain/coordination"
)

func TestOperationTaskCarriesOpaqueClassAndOperationIdentity(t *testing.T) {
	task := corecoord.Task{
		Kind:      corecoord.TaskKindOperation,
		Operation: &corecoord.OperationRef{ClassID: "media-pool", ID: "media.derivative", Version: 2},
		Payload:   json.RawMessage(`{"job":"opaque"}`),
	}
	if task.Kind != corecoord.TaskKindOperation || task.Operation == nil ||
		task.Operation.ClassID != "media-pool" || task.Operation.ID != "media.derivative" ||
		task.Operation.Version != 2 {
		t.Fatalf("operation task lost its opaque dispatch identity: %+v", task)
	}
}
