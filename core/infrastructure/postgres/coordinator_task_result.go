package postgres

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/Zubrium/cdn/core/domain/coordination"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// GetCoordinatorTaskResult is the owner-scoped read of one durable task result.
// The workflow table has no per-instance owner column: instance ownership is
// represented by an active lease at the client's config epoch, which is
// checked both by the instance guard and by the read itself.
func GetCoordinatorTaskResult(ctx context.Context, pool *pgxpool.Pool, ownerClassID string, owner uuid.UUID, epoch int64, workflowID, taskID uuid.UUID) (coordination.TaskResult, error) {
	if ctx == nil || pool == nil || !coordinatorTaskClassID.MatchString(ownerClassID) || owner == uuid.Nil || epoch <= 0 ||
		workflowID == uuid.Nil || taskID == uuid.Nil {
		return coordination.TaskResult{}, ErrCoordinatorInvalid
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return coordination.TaskResult{}, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := verifyCoordinatorInstance(ctx, tx, owner, epoch); err != nil {
		return coordination.TaskResult{}, err
	}
	var result coordination.TaskResult
	var taskClass, operationID, errorCode pgtype.Text
	var contractVersion pgtype.Int4
	var payload []byte
	err = tx.QueryRow(ctx, `SELECT t.task_id,t.workflow_id,t.task_class_id,t.operation_id,t.contract_version,
		t.state,t.result,t.error_code FROM coordination.tasks AS t
		JOIN coordination.workflows AS w ON w.workflow_id=t.workflow_id
		WHERE w.owner_class_id=$1 AND w.config_epoch=$2
		AND EXISTS(SELECT 1 FROM coordination.instances AS i
			WHERE i.instance_id=$3 AND i.config_epoch=$2 AND i.lease_until>clock_timestamp())
		AND t.workflow_id=$4 AND t.task_id=$5`, ownerClassID, epoch, owner, workflowID, taskID).
		Scan(&result.TaskID, &result.WorkflowID, &taskClass, &operationID, &contractVersion, &result.State, &payload, &errorCode)
	if errors.Is(err, pgx.ErrNoRows) {
		return coordination.TaskResult{}, ErrTaskMissing
	}
	if err != nil {
		return coordination.TaskResult{}, err
	}
	if result.TaskID != taskID || result.WorkflowID != workflowID {
		return coordination.TaskResult{}, ErrCoordinatorInvalid
	}
	if len(payload) > maxCoordinatorPayloadBytes {
		return coordination.TaskResult{}, ErrCoordinatorLimit
	}
	if taskClass.Valid != operationID.Valid || taskClass.Valid != contractVersion.Valid {
		return coordination.TaskResult{}, ErrCoordinatorInvalid
	}
	if taskClass.Valid {
		result.Operation = &coordination.OperationRef{ClassID: taskClass.String,
			ID: operationID.String, Version: int(contractVersion.Int32)}
	}
	result.Result = append(json.RawMessage(nil), payload...)
	if errorCode.Valid {
		result.ErrorCode = errorCode.String
	}
	if err := tx.Commit(ctx); err != nil {
		return coordination.TaskResult{}, err
	}
	return result, nil
}
