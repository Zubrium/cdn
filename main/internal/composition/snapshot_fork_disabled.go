//go:build !full || devfixtures || router

package composition

import (
	"context"
	"errors"

	platform "github.com/Zubrium/cdn/core/presentation"
)

type snapshotForkWorkflowStarter struct{ core *coreComponent }

func (s snapshotForkWorkflowStarter) Start(context.Context, platform.WorkflowStartRequest) (platform.WorkflowReceipt, error) {
	return platform.WorkflowReceipt{}, errors.New("snapshot fork workflow is not available in this profile")
}
