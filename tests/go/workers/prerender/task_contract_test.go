package prerender

import (
	"context"

	"github.com/Zubrium/cdn/pkg/contracts"
)

var (
	_ func(context.Context, contracts.OperationTask) (contracts.OperationOutcome, error) = FailClosedWorker{}.WorkerHandler
	_ func(context.Context, contracts.OperationTask) (contracts.OperationOutcome, error) = (&DockerWorker{}).WorkerHandler
)
