package archive

import (
	"context"

	"github.com/Zubrium/cdn/pkg/contracts"
)

var _ func(context.Context, contracts.OperationTask) (contracts.OperationOutcome, error) = (&Handler{}).WorkerHandler
