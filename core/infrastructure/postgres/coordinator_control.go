package postgres

import (
	"context"
	"errors"

	"github.com/Zubrium/cdn/core/domain/coordination"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

func ListCoordinatorWorkflows(ctx context.Context, pool *pgxpool.Pool, request coordination.WorkflowListRequest) (coordination.WorkflowPage, error) {
	if ctx == nil || pool == nil || request.Owner == uuid.Nil || request.ConfigEpoch <= 0 || request.Limit < 1 || request.Limit > 200 ||
		(request.State != "" && !validWorkflowState(request.State)) {
		return coordination.WorkflowPage{}, ErrCoordinatorInvalid
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return coordination.WorkflowPage{}, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := verifyCoordinatorInstance(ctx, tx, request.Owner, request.ConfigEpoch); err != nil {
		return coordination.WorkflowPage{}, err
	}
	var beforeAt, beforeID any
	if request.Before != nil {
		if request.Before.ID == uuid.Nil || request.Before.CreatedAt.IsZero() {
			return coordination.WorkflowPage{}, ErrCoordinatorInvalid
		}
		beforeAt, beforeID = request.Before.CreatedAt, request.Before.ID
	}
	rows, err := tx.Query(ctx, `SELECT workflow.workflow_id,workflow.owner_class_id,workflow.state,count(task.task_id),
		workflow.created_at,workflow.updated_at FROM coordination.workflows workflow
		LEFT JOIN coordination.tasks task USING(workflow_id)
		WHERE ($1='' OR workflow.state=$1) AND ($2::timestamptz IS NULL OR (workflow.created_at,workflow.workflow_id)<($2,$3::uuid))
		GROUP BY workflow.workflow_id ORDER BY workflow.created_at DESC,workflow.workflow_id DESC LIMIT $4`,
		request.State, beforeAt, beforeID, request.Limit+1)
	if err != nil {
		return coordination.WorkflowPage{}, err
	}
	page := coordination.WorkflowPage{Items: make([]coordination.WorkflowSummary, 0, request.Limit)}
	for rows.Next() {
		var item coordination.WorkflowSummary
		if err := rows.Scan(&item.ID, &item.OwnerClassID, &item.State, &item.TaskCount, &item.CreatedAt, &item.UpdatedAt); err != nil {
			rows.Close()
			return coordination.WorkflowPage{}, err
		}
		page.Items = append(page.Items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return coordination.WorkflowPage{}, err
	}
	rows.Close()
	if len(page.Items) > request.Limit {
		page.Items = page.Items[:request.Limit]
		last := page.Items[len(page.Items)-1]
		page.Next = &coordination.WorkflowCursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	if err := tx.Commit(ctx); err != nil {
		return coordination.WorkflowPage{}, err
	}
	return page, nil
}

func GetCoordinatorWorkflowDetail(ctx context.Context, pool *pgxpool.Pool, owner uuid.UUID, epoch int64, workflowID uuid.UUID,
	limit int, before *coordination.TaskCursor) (coordination.WorkflowDetail, error) {
	if ctx == nil || pool == nil || owner == uuid.Nil || epoch <= 0 || workflowID == uuid.Nil || limit < 1 || limit > 1000 ||
		(before != nil && (before.ID == uuid.Nil || before.CreatedAt.IsZero())) {
		return coordination.WorkflowDetail{}, ErrCoordinatorInvalid
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return coordination.WorkflowDetail{}, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := verifyCoordinatorInstance(ctx, tx, owner, epoch); err != nil {
		return coordination.WorkflowDetail{}, err
	}
	var workflow coordination.Workflow
	var result []byte
	err = tx.QueryRow(ctx, `SELECT workflow_id,owner_class_id,state,root_task_id,result,created_at,updated_at
		FROM coordination.workflows WHERE workflow_id=$1`, workflowID).Scan(&workflow.ID, &workflow.OwnerClassID, &workflow.State,
		&workflow.RootTaskID, &result, &workflow.CreatedAt, &workflow.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return coordination.WorkflowDetail{}, ErrTaskMissing
	}
	if err != nil {
		return coordination.WorkflowDetail{}, err
	}
	workflow.Result = append([]byte(nil), result...)
	var beforeAt, beforeID any
	if before != nil {
		beforeAt, beforeID = before.CreatedAt, before.ID
	}
	rows, err := tx.Query(ctx, `SELECT task.task_id,task.workflow_id,task.task_kind,task.task_class_id,task.operation_id,task.contract_version,
		task.parent_task_id,COALESCE(task.emission_key,''),
		ARRAY(SELECT dependency.depends_on FROM coordination.dependencies dependency
			WHERE dependency.workflow_id=task.workflow_id AND dependency.task_id=task.task_id ORDER BY dependency.depends_on),
		task.state,task.attempts,task.max_attempts,task.available_at,task.lease_until,task.error_code,task.created_at,task.updated_at
		FROM coordination.tasks task WHERE task.workflow_id=$1
		AND ($2::timestamptz IS NULL OR (task.created_at,task.task_id)<($2,$3::uuid))
		ORDER BY task.created_at DESC,task.task_id DESC LIMIT $4`, workflowID, beforeAt, beforeID, limit+1)
	if err != nil {
		return coordination.WorkflowDetail{}, err
	}
	page := coordination.TaskPage{Items: make([]coordination.TaskSummary, 0, limit)}
	for rows.Next() {
		var item coordination.TaskSummary
		var kind string
		var taskClass, operationID, errorCode pgtype.Text
		var contractVersion pgtype.Int4
		var parentTaskID pgtype.UUID
		var leaseUntil pgtype.Timestamptz
		if err := rows.Scan(&item.ID, &item.WorkflowID, &kind, &taskClass, &operationID, &contractVersion,
			&parentTaskID, &item.EmissionKey, &item.DependsOn, &item.State, &item.Attempts, &item.MaxAttempts,
			&item.AvailableAt, &leaseUntil, &errorCode, &item.CreatedAt, &item.UpdatedAt); err != nil {
			rows.Close()
			return coordination.WorkflowDetail{}, err
		}
		item.Kind = coordination.TaskKind(kind)
		if item.Kind == coordination.TaskKindOperation {
			if !taskClass.Valid || !operationID.Valid || !contractVersion.Valid {
				rows.Close()
				return coordination.WorkflowDetail{}, ErrCoordinatorInvalid
			}
			item.Operation = &coordination.OperationRef{ClassID: taskClass.String, ID: operationID.String,
				Version: int(contractVersion.Int32)}
		}
		if parentTaskID.Valid {
			item.ParentTaskID = uuid.UUID(parentTaskID.Bytes)
		}
		if leaseUntil.Valid {
			value := leaseUntil.Time
			item.LeaseUntil = &value
		}
		if errorCode.Valid {
			item.ErrorCode = errorCode.String
		}
		page.Items = append(page.Items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return coordination.WorkflowDetail{}, err
	}
	rows.Close()
	if len(page.Items) > limit {
		page.Items = page.Items[:limit]
		last := page.Items[len(page.Items)-1]
		page.Next = &coordination.TaskCursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	if err := tx.Commit(ctx); err != nil {
		return coordination.WorkflowDetail{}, err
	}
	return coordination.WorkflowDetail{Workflow: workflow, TaskPage: page}, nil
}

func validWorkflowState(state string) bool {
	switch state {
	case "queued", "running", "succeeded", "failed", "cancelled":
		return true
	default:
		return false
	}
}
