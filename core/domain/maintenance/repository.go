package maintenance

import (
	"context"
	"time"
)

// Repository exposes generic Core-owned reconciliation work. Product-specific
// workflows and referenced-data policies do not belong in this boundary.
type Repository interface {
	RecoverPending(context.Context, time.Time, int) error
	PruneWorkflows(context.Context, int) (int64, error)
	PruneSessions(context.Context, time.Time, int) (int64, error)
}
