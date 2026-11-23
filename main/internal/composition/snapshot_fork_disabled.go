//go:build !full || devfixtures || router

package composition

import (
	"context"
	"errors"

	"github.com/Zubrium/cdn/pkg/contracts"
)

type snapshotForkWorkflowStarter struct{ core *coreComponent }

func (s snapshotForkWorkflowStarter) Start(context.Context, contracts.WorkflowStartInput) (contracts.WorkflowStartReceipt, error) {
	return contracts.WorkflowStartReceipt{}, errors.New("snapshot fork workflow is not available in this profile")
}
