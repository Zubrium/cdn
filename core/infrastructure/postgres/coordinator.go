package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Zubrium/cdn/core/domain/coordination"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrCoordinatorConflict   = coordination.ErrConflict
	ErrCoordinatorLeaseLost  = coordination.ErrLeaseLost
	ErrCoordinatorConfigDiff = coordination.ErrConfigDiff
	ErrTaskMissing           = coordination.ErrTaskMissing
	ErrTaskNotOwned          = coordination.ErrTaskNotOwned
	ErrCoordinatorStale      = coordination.ErrStale
	ErrCoordinatorInvalid    = coordination.ErrInvalid
	ErrCoordinatorLimit      = coordination.ErrLimit
)

var (
	coordinatorTaskClassID = regexp.MustCompile(`^[a-z][a-z0-9.-]{0,63}$`)
	coordinatorOperationID = regexp.MustCompile(`^[a-z][a-z0-9._/-]{0,127}$`)
	coordinatorErrorCode   = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,127}$`)
	coordinatorFingerprint = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

const maxCoordinatorPayloadBytes = 1 << 20
const maxCoordinatorChildrenPerTask = 256
const maxCoordinatorWorkflowRetention = 365 * 24 * time.Hour

type Task = coordination.Task
type TaskCursor = coordination.TaskCursor
type TaskSummary = coordination.TaskSummary
type TaskPage = coordination.TaskPage
type TaskOutcome = coordination.TaskOutcome
type TaskChild = coordination.TaskChild

func validCoordinatorIdentity(instanceID uuid.UUID, fingerprint string, lease time.Duration) bool {
	return instanceID != uuid.Nil && coordinatorFingerprint.MatchString(fingerprint) && lease >= time.Second && lease <= time.Hour
}

// RegisterCoordinatorInstance locks the database-wide runtime row. A new
// fingerprint is accepted only after every old replica lease has expired.
func RegisterCoordinatorInstance(ctx context.Context, pool *pgxpool.Pool, instanceID uuid.UUID, fingerprint string, lease, retention time.Duration) (int64, error) {
	if ctx == nil || pool == nil || !validCoordinatorIdentity(instanceID, fingerprint, lease) || retention <= 0 || retention > maxCoordinatorWorkflowRetention {
		return 0, ErrCoordinatorInvalid
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(ctx, `INSERT INTO coordination.runtime(singleton,fingerprint,epoch)
		VALUES(true,$1,1) ON CONFLICT(singleton) DO NOTHING`, fingerprint); err != nil {
		return 0, err
	}
	var current string
	var epoch int64
	if err := tx.QueryRow(ctx, `SELECT fingerprint,epoch FROM coordination.runtime WHERE singleton=true FOR UPDATE`).Scan(&current, &epoch); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM coordination.instances WHERE lease_until<=clock_timestamp()`); err != nil {
		return 0, err
	}
	var active bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM coordination.instances WHERE lease_until>clock_timestamp())`).Scan(&active); err != nil {
		return 0, err
	}
	if current != fingerprint {
		if active {
			return 0, ErrCoordinatorConfigDiff
		}
		epoch++
		if _, err := tx.Exec(ctx, `UPDATE coordination.runtime SET fingerprint=$1,epoch=$2
			WHERE singleton=true`, fingerprint, epoch); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(ctx, `UPDATE coordination.workflows SET state='cancelled',
			expires_at=clock_timestamp()+($1::bigint*interval '1 microsecond'),updated_at=clock_timestamp()
			WHERE state IN ('queued','running')`, retention.Microseconds()); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(ctx, `UPDATE coordination.tasks SET state='cancelled',lease_owner=NULL,lease_until=NULL,
			config_epoch=$1,error_code='configuration_changed',updated_at=clock_timestamp()
			WHERE state IN ('queued','running')`, epoch); err != nil {
			return 0, err
		}
	}
	command, err := tx.Exec(ctx, `INSERT INTO coordination.instances(instance_id,config_epoch,lease_until)
		VALUES($1,$2,clock_timestamp()+($3::bigint*interval '1 microsecond')) ON CONFLICT(instance_id) DO NOTHING`,
		instanceID, epoch, lease.Microseconds())
	if err != nil {
		return 0, err
	}
	if command.RowsAffected() != 1 {
		return 0, ErrCoordinatorConflict
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return epoch, nil
}

func RenewCoordinatorInstance(ctx context.Context, pool *pgxpool.Pool, instanceID uuid.UUID, epoch int64, lease time.Duration) error {
	if ctx == nil || pool == nil || instanceID == uuid.Nil || epoch <= 0 || lease < time.Second || lease > time.Hour {
		return ErrCoordinatorInvalid
	}
	command, err := pool.Exec(ctx, `UPDATE coordination.instances SET lease_until=clock_timestamp()+($3::bigint*interval '1 microsecond')
		WHERE instance_id=$1 AND config_epoch=$2 AND lease_until>clock_timestamp()
		AND EXISTS(SELECT 1 FROM coordination.runtime WHERE singleton=true AND epoch=$2)`, instanceID, epoch, lease.Microseconds())
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrCoordinatorLeaseLost
	}
	return nil
}

func ReleaseCoordinatorInstance(ctx context.Context, pool *pgxpool.Pool, instanceID uuid.UUID, epoch int64) error {
	if ctx == nil || pool == nil || instanceID == uuid.Nil || epoch <= 0 {
		return ErrCoordinatorInvalid
	}
	_, err := pool.Exec(ctx, `DELETE FROM coordination.instances WHERE instance_id=$1 AND config_epoch=$2`, instanceID, epoch)
	return err
}

func StartCoordinatorWorkflow(ctx context.Context, pool *pgxpool.Pool, request coordination.WorkflowRequest) (coordination.Workflow, bool, error) {
	root := request.Root
	var inputMaterial string
	if root.Operation != nil {
		inputMaterial = root.Operation.ID + "\x00" + fmt.Sprint(root.Operation.Version) + "\x00" + string(root.Payload)
	}
	inputHash := sha256.Sum256([]byte(inputMaterial))
	if ctx == nil || pool == nil || request.ID == uuid.Nil || request.Owner == uuid.Nil || request.ConfigEpoch <= 0 ||
		!coordinatorTaskClassID.MatchString(request.OwnerClassID) || !validCoordinatorIdempotencyKey(request.IdempotencyKey) ||
		len(request.InputHash) != sha256.Size || !equalBytes(request.InputHash, inputHash[:]) || root.ID == uuid.Nil ||
		root.Kind != coordination.TaskKindOperation || root.Operation == nil || root.Operation.ClassID != request.OwnerClassID ||
		!coordinatorOperationID.MatchString(root.Operation.ID) || root.Operation.Version < 1 || root.Operation.Version > 1<<16 ||
		root.MaxAttempts < 1 || root.MaxAttempts > 100 || !validCoordinatorJSON(root.Payload) ||
		request.Retention <= 0 || request.Retention > maxCoordinatorWorkflowRetention ||
		(root.WorkflowID != uuid.Nil && root.WorkflowID != request.ID) || root.ParentTaskID != uuid.Nil || root.EmissionKey != "" ||
		len(root.DependsOn) > 0 || len(root.DependsOnEmissions) > 0 {
		return coordination.Workflow{}, false, ErrCoordinatorInvalid
	}
	keyHash := sha256.Sum256([]byte(request.OwnerClassID + "\x00" + request.IdempotencyKey))
	idempotencyKey := hex.EncodeToString(keyHash[:])
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return coordination.Workflow{}, false, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := verifyCoordinatorInstance(ctx, tx, request.Owner, request.ConfigEpoch); err != nil {
		return coordination.Workflow{}, false, err
	}
	workflow, err := getWorkflowByIdempotencyKey(ctx, tx, request.OwnerClassID, idempotencyKey)
	if err == nil {
		if !equalBytes(workflow.inputHash, request.InputHash) {
			return coordination.Workflow{}, false, ErrCoordinatorConflict
		}
		if err := tx.Commit(ctx); err != nil {
			return coordination.Workflow{}, false, err
		}
		return workflow.Workflow, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return coordination.Workflow{}, false, err
	}
	var insertedID uuid.UUID
	err = tx.QueryRow(ctx, `INSERT INTO coordination.workflows(workflow_id,root_task_id,owner_class_id,idempotency_key,
		input_hash,config_epoch,state) VALUES($1,$2,$3,$4,$5,$6,'queued')
		ON CONFLICT(owner_class_id,idempotency_key) DO NOTHING RETURNING workflow_id`, request.ID, root.ID,
		request.OwnerClassID, idempotencyKey, request.InputHash, request.ConfigEpoch).Scan(&insertedID)
	if errors.Is(err, pgx.ErrNoRows) {
		workflow, err = getWorkflowByIdempotencyKey(ctx, tx, request.OwnerClassID, idempotencyKey)
		if err != nil {
			return coordination.Workflow{}, false, err
		}
		if !equalBytes(workflow.inputHash, request.InputHash) {
			return coordination.Workflow{}, false, ErrCoordinatorConflict
		}
		if err := tx.Commit(ctx); err != nil {
			return coordination.Workflow{}, false, err
		}
		return workflow.Workflow, false, nil
	}
	if err != nil {
		return coordination.Workflow{}, false, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO coordination.tasks(task_id,workflow_id,task_kind,task_class_id,operation_id,contract_version,
		payload,state,max_attempts,available_at,config_epoch) VALUES($1,$2,$3,$4,$5,$6,$7::jsonb,'queued',$8,
		clock_timestamp(),$9)`, root.ID, request.ID, root.Kind, root.Operation.ClassID, root.Operation.ID, root.Operation.Version,
		[]byte(root.Payload), root.MaxAttempts, request.ConfigEpoch)
	if err != nil {
		return coordination.Workflow{}, false, err
	}
	result := coordination.Workflow{ID: request.ID, OwnerClassID: request.OwnerClassID, RootTaskID: root.ID, State: "queued"}
	if err := tx.QueryRow(ctx, `SELECT created_at,updated_at FROM coordination.workflows WHERE workflow_id=$1`, request.ID).
		Scan(&result.CreatedAt, &result.UpdatedAt); err != nil {
		return coordination.Workflow{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return coordination.Workflow{}, false, err
	}
	return result, true, nil
}

type workflowRow struct {
	Workflow  coordination.Workflow
	inputHash []byte
}

func getWorkflowByIdempotencyKey(ctx context.Context, db interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, ownerClassID, key string) (workflowRow, error) {
	return scanWorkflow(db.QueryRow(ctx, `SELECT workflow_id,owner_class_id,state,root_task_id,result,input_hash,created_at,updated_at
		FROM coordination.workflows WHERE owner_class_id=$1 AND idempotency_key=$2`, ownerClassID, key))
}

func GetCoordinatorWorkflow(ctx context.Context, pool *pgxpool.Pool, ownerClassID string, instanceID uuid.UUID, epoch int64, workflowID uuid.UUID) (coordination.Workflow, error) {
	if ctx == nil || pool == nil || !coordinatorTaskClassID.MatchString(ownerClassID) || instanceID == uuid.Nil || epoch <= 0 || workflowID == uuid.Nil {
		return coordination.Workflow{}, ErrCoordinatorInvalid
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return coordination.Workflow{}, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := verifyCoordinatorInstance(ctx, tx, instanceID, epoch); err != nil {
		return coordination.Workflow{}, err
	}
	workflow, err := scanWorkflow(tx.QueryRow(ctx, `SELECT workflow_id,owner_class_id,state,root_task_id,result,input_hash,created_at,updated_at
		FROM coordination.workflows WHERE owner_class_id=$1 AND workflow_id=$2`, ownerClassID, workflowID))
	if errors.Is(err, pgx.ErrNoRows) {
		return coordination.Workflow{}, ErrTaskMissing
	}
	if err != nil {
		return coordination.Workflow{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return coordination.Workflow{}, err
	}
	return workflow.Workflow, nil
}

func CancelCoordinatorWorkflow(ctx context.Context, pool *pgxpool.Pool, ownerClassID string, instanceID uuid.UUID, epoch int64, workflowID uuid.UUID, retention time.Duration) error {
	if ctx == nil || pool == nil || !coordinatorTaskClassID.MatchString(ownerClassID) || instanceID == uuid.Nil || epoch <= 0 || workflowID == uuid.Nil ||
		retention <= 0 || retention > maxCoordinatorWorkflowRetention {
		return ErrCoordinatorInvalid
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := verifyCoordinatorInstance(ctx, tx, instanceID, epoch); err != nil {
		return err
	}
	var state string
	err = tx.QueryRow(ctx, `SELECT state FROM coordination.workflows WHERE owner_class_id=$1 AND workflow_id=$2 FOR UPDATE`, ownerClassID, workflowID).Scan(&state)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrTaskMissing
	}
	if err != nil {
		return err
	}
	if state == "cancelled" {
		return tx.Commit(ctx)
	}
	if state != "queued" && state != "running" {
		return ErrCoordinatorConflict
	}
	if _, err := tx.Exec(ctx, `UPDATE coordination.workflows SET state='cancelled',
		expires_at=clock_timestamp()+($2::bigint*interval '1 microsecond'),updated_at=clock_timestamp() WHERE workflow_id=$1`, workflowID, retention.Microseconds()); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE coordination.tasks SET state='cancelled',lease_owner=NULL,lease_until=NULL,
		error_code='workflow_cancelled',updated_at=clock_timestamp() WHERE workflow_id=$1 AND state IN ('queued','running')`, workflowID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func PruneCoordinatorWorkflows(ctx context.Context, pool *pgxpool.Pool, limit int) (int64, error) {
	if ctx == nil || pool == nil || limit < 1 || limit > 10000 {
		return 0, ErrCoordinatorInvalid
	}
	command, err := pool.Exec(ctx, `WITH expired AS (
		SELECT workflow_id FROM coordination.workflows WHERE state IN ('succeeded','failed','cancelled')
		AND expires_at IS NOT NULL AND expires_at<=clock_timestamp()
		ORDER BY expires_at,workflow_id LIMIT $1 FOR UPDATE SKIP LOCKED
	) DELETE FROM coordination.workflows w USING expired e WHERE w.workflow_id=e.workflow_id`, limit)
	if err != nil {
		return 0, err
	}
	return command.RowsAffected(), nil
}

func scanWorkflow(row rowScanner) (workflowRow, error) {
	var result workflowRow
	var payload []byte
	err := row.Scan(&result.Workflow.ID, &result.Workflow.OwnerClassID, &result.Workflow.State,
		&result.Workflow.RootTaskID, &payload, &result.inputHash, &result.Workflow.CreatedAt, &result.Workflow.UpdatedAt)
	if err != nil {
		return workflowRow{}, err
	}
	result.Workflow.Result = append(json.RawMessage(nil), payload...)
	return result, nil
}

func ClaimTasks(ctx context.Context, pool *pgxpool.Pool, owner uuid.UUID, epoch int64, taskClasses []string, limit int, lease, workflowRetention time.Duration) ([]Task, error) {
	if ctx == nil || pool == nil || owner == uuid.Nil || epoch <= 0 || len(taskClasses) == 0 || len(taskClasses) > 256 || limit < 1 || limit > 256 || lease < time.Second || lease > time.Hour ||
		workflowRetention <= 0 || workflowRetention > maxCoordinatorWorkflowRetention {
		return nil, ErrCoordinatorInvalid
	}
	seenComponents := make(map[string]struct{}, len(taskClasses))
	for _, taskClass := range taskClasses {
		if taskClass == "" || len(taskClass) > 64 {
			return nil, ErrCoordinatorInvalid
		}
		if _, exists := seenComponents[taskClass]; exists {
			return nil, ErrCoordinatorInvalid
		}
		seenComponents[taskClass] = struct{}{}
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := verifyCoordinatorInstance(ctx, tx, owner, epoch); err != nil {
		return nil, err
	}
	if err := settleReadyJoinTasks(ctx, tx, workflowRetention); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE coordination.workflows w SET state='failed',expires_at=clock_timestamp()+($1::bigint*interval '1 microsecond'),updated_at=clock_timestamp()
		WHERE w.state IN ('queued','running') AND EXISTS(SELECT 1 FROM coordination.tasks t
		WHERE t.workflow_id=w.workflow_id AND t.state='running' AND t.lease_until<=clock_timestamp() AND t.attempts>=t.max_attempts)`, workflowRetention.Microseconds()); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE coordination.tasks SET state='failed',lease_owner=NULL,lease_until=NULL,
		error_code='attempts_exhausted',updated_at=clock_timestamp()
		WHERE state='running' AND lease_until<=clock_timestamp() AND attempts>=max_attempts
		AND workflow_id IN (SELECT workflow_id FROM coordination.workflows WHERE state='failed')`); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE coordination.tasks SET state='cancelled',lease_owner=NULL,lease_until=NULL,
		error_code='workflow_failed',updated_at=clock_timestamp() WHERE state IN ('queued','running') AND workflow_id IN
		(SELECT workflow_id FROM coordination.workflows WHERE state='failed')`); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE coordination.tasks SET state='queued',lease_owner=NULL,lease_until=NULL,
		available_at=clock_timestamp(),updated_at=clock_timestamp()
		WHERE state='running' AND lease_until<=clock_timestamp() AND attempts<max_attempts`); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE coordination.workflows w SET state='running',updated_at=clock_timestamp()
		WHERE w.state='queued' AND EXISTS(SELECT 1 FROM coordination.tasks t WHERE t.workflow_id=w.workflow_id
		AND t.task_kind='operation' AND t.state='queued' AND t.available_at<=clock_timestamp() AND NOT EXISTS(
			SELECT 1 FROM coordination.dependencies d JOIN coordination.tasks prerequisite
			ON prerequisite.workflow_id=d.workflow_id AND prerequisite.task_id=d.depends_on
			WHERE d.workflow_id=t.workflow_id AND d.task_id=t.task_id AND prerequisite.state<>'succeeded'))`); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `WITH candidates AS (
		SELECT t.task_id FROM coordination.tasks t JOIN coordination.workflows w USING(workflow_id)
		WHERE t.task_kind='operation' AND t.task_class_id=ANY($4::text[]) AND t.state='queued' AND t.available_at<=clock_timestamp() AND t.attempts<t.max_attempts
			AND w.state='running' AND w.config_epoch=$3
		AND NOT EXISTS(SELECT 1 FROM coordination.dependencies d JOIN coordination.tasks prerequisite
			ON prerequisite.workflow_id=d.workflow_id AND prerequisite.task_id=d.depends_on
			WHERE d.workflow_id=t.workflow_id AND d.task_id=t.task_id AND prerequisite.state<>'succeeded')
		ORDER BY t.available_at,t.created_at,t.task_id LIMIT $5 FOR UPDATE OF t SKIP LOCKED
	), claimed AS (
		UPDATE coordination.tasks t SET state='running',attempts=t.attempts+1,lease_owner=$1,
		lease_until=clock_timestamp()+($2::bigint*interval '1 microsecond'),fencing_token=t.fencing_token+1,
		config_epoch=$3,updated_at=clock_timestamp() FROM candidates c WHERE t.task_id=c.task_id
		RETURNING t.*
	) SELECT c.task_id,c.workflow_id,c.task_kind,c.task_class_id,c.operation_id,c.contract_version,COALESCE(c.emission_key,''),
		w.idempotency_key,c.payload,c.state,c.attempts,c.max_attempts,c.available_at,c.lease_owner,c.lease_until,c.fencing_token,c.config_epoch,
		c.result,COALESCE(c.error_code,''),c.created_at,c.updated_at FROM claimed c JOIN coordination.workflows w USING(workflow_id)`, owner, lease.Microseconds(), epoch, taskClasses, limit)
	if err != nil {
		return nil, err
	}
	tasks, err := scanTasks(rows)
	rows.Close()
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return tasks, nil
}

func ExtendTaskLease(ctx context.Context, pool *pgxpool.Pool, taskID, owner uuid.UUID, epoch, token int64, lease time.Duration) error {
	if ctx == nil || pool == nil || taskID == uuid.Nil || owner == uuid.Nil || epoch <= 0 || token <= 0 || lease < time.Second || lease > time.Hour {
		return ErrCoordinatorInvalid
	}
	command, err := pool.Exec(ctx, `UPDATE coordination.tasks t SET lease_until=clock_timestamp()+($5::bigint*interval '1 microsecond'),
		updated_at=clock_timestamp() WHERE t.task_id=$1 AND t.lease_owner=$2 AND t.config_epoch=$3 AND t.fencing_token=$4
		AND t.state='running' AND t.lease_until>clock_timestamp() AND EXISTS(SELECT 1 FROM coordination.instances i
		JOIN coordination.runtime r ON r.singleton=true AND r.epoch=i.config_epoch
		JOIN coordination.workflows w ON w.workflow_id=t.workflow_id AND w.state='running'
		WHERE i.instance_id=$2 AND i.config_epoch=$3 AND i.lease_until>clock_timestamp())`, taskID, owner, epoch, token, lease.Microseconds())
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrTaskNotOwned
	}
	return nil
}

// CompleteCoordinatorTask commits the parent result and its dynamically
// emitted children in one transaction. A stale lease cannot commit either.
func CompleteCoordinatorTask(ctx context.Context, pool *pgxpool.Pool, taskID, owner uuid.UUID, epoch, token int64,
	maxWorkflowTasks, maxChildren int, workflowRetention time.Duration, outcome TaskOutcome) error {
	if ctx == nil || pool == nil || taskID == uuid.Nil || owner == uuid.Nil || epoch <= 0 || token <= 0 ||
		maxWorkflowTasks < 1 || maxWorkflowTasks > 100000 || maxChildren < 0 || maxChildren > maxCoordinatorChildrenPerTask ||
		workflowRetention <= 0 || workflowRetention > maxCoordinatorWorkflowRetention || !validCoordinatorJSON(outcome.Result) || len(outcome.Children) > maxChildren {
		return ErrCoordinatorInvalid
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := verifyCoordinatorInstance(ctx, tx, owner, epoch); err != nil {
		return err
	}
	var workflowID uuid.UUID
	err = tx.QueryRow(ctx, `SELECT t.workflow_id FROM coordination.tasks t JOIN coordination.workflows w USING(workflow_id)
		WHERE t.task_id=$1 AND w.state IN ('queued','running') FOR UPDATE OF w`, taskID).Scan(&workflowID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrTaskNotOwned
	}
	if err != nil {
		return err
	}
	err = tx.QueryRow(ctx, `UPDATE coordination.tasks t SET state='succeeded',result=$5::jsonb,lease_owner=NULL,
		lease_until=NULL,error_code=NULL,updated_at=clock_timestamp() WHERE t.task_id=$1 AND t.lease_owner=$2
		AND t.config_epoch=$3 AND t.fencing_token=$4 AND t.state='running' AND t.lease_until>clock_timestamp()
		AND EXISTS(SELECT 1 FROM coordination.instances i JOIN coordination.runtime r ON r.singleton=true AND r.epoch=i.config_epoch
		JOIN coordination.workflows w ON w.workflow_id=t.workflow_id AND w.state IN ('queued','running')
		WHERE i.instance_id=$2 AND i.config_epoch=$3 AND i.lease_until>clock_timestamp()) RETURNING t.workflow_id`,
		taskID, owner, epoch, token, []byte(outcome.Result)).Scan(&workflowID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrTaskNotOwned
	}
	if err != nil {
		return err
	}
	var taskCount int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM coordination.tasks WHERE workflow_id=$1`, workflowID).Scan(&taskCount); err != nil {
		return err
	}
	if taskCount+len(outcome.Children) > maxWorkflowTasks {
		return ErrCoordinatorLimit
	}
	if err := insertCoordinatorChildren(ctx, tx, workflowID, taskID, epoch, outcome.Children); err != nil {
		return err
	}
	if err := settleReadyJoinTasks(ctx, tx, workflowRetention); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func insertCoordinatorChildren(ctx context.Context, tx pgx.Tx, workflowID, parentID uuid.UUID, epoch int64, children []TaskChild) error {
	if len(children) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, len(children))
	keys := make(map[string]struct{}, len(children))
	newTaskIDs := make(map[uuid.UUID]struct{}, len(children))
	idsByEmission := make(map[string]uuid.UUID, len(children))
	for i, child := range children {
		if !validCoordinatorIdempotencyKey(child.EmissionKey) || !validCoordinatorJSON(child.Payload) {
			return ErrCoordinatorInvalid
		}
		switch child.Kind {
		case coordination.TaskKindOperation:
			if child.Operation == nil || !coordinatorTaskClassID.MatchString(child.Operation.ClassID) || !coordinatorOperationID.MatchString(child.Operation.ID) ||
				child.Operation.Version < 1 || child.Operation.Version > 1<<16 || child.MaxAttempts < 1 || child.MaxAttempts > 100 {
				return ErrCoordinatorInvalid
			}
		case coordination.TaskKindJoin:
			if child.Operation != nil || child.MaxAttempts != 0 || len(child.DependsOn)+len(child.DependsOnEmissions) == 0 {
				return ErrCoordinatorInvalid
			}
		default:
			return ErrCoordinatorInvalid
		}
		if _, duplicate := keys[child.EmissionKey]; duplicate {
			return ErrCoordinatorInvalid
		}
		keys[child.EmissionKey] = struct{}{}
		id, err := uuid.NewRandom()
		if err != nil {
			return err
		}
		ids[i] = id
		newTaskIDs[id] = struct{}{}
		idsByEmission[child.EmissionKey] = id
	}
	for i, child := range children {
		var availableAt any
		if child.Kind == coordination.TaskKindOperation && !child.AvailableAt.IsZero() {
			availableAt = child.AvailableAt.UTC()
		}
		var taskClass, operationID any
		var version, maxAttempts int
		if child.Kind == coordination.TaskKindOperation {
			taskClass, operationID, version, maxAttempts = child.Operation.ClassID, child.Operation.ID, child.Operation.Version, child.MaxAttempts
		} else {
			maxAttempts = 1
		}
		_, err := tx.Exec(ctx, `INSERT INTO coordination.tasks(task_id,workflow_id,task_kind,task_class_id,operation_id,contract_version,
			parent_task_id,emission_key,payload,state,max_attempts,available_at,config_epoch)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9::jsonb,'queued',$10,COALESCE($11::timestamptz,clock_timestamp()),$12)`, ids[i], workflowID,
			child.Kind, taskClass, operationID, nullableVersion(version), parentID, child.EmissionKey, []byte(child.Payload), maxAttempts, availableAt, epoch)
		if err != nil {
			return err
		}
	}
	dependenciesByTask := make([][]uuid.UUID, len(children))
	for i, child := range children {
		dependencies := append([]uuid.UUID(nil), child.DependsOn...)
		for _, emissionKey := range child.DependsOnEmissions {
			dependencyID, exists := idsByEmission[emissionKey]
			if !exists {
				return ErrCoordinatorInvalid
			}
			dependencies = append(dependencies, dependencyID)
		}
		if len(dependencies) == 0 && child.Kind == coordination.TaskKindOperation {
			dependencies = []uuid.UUID{parentID}
		}
		if len(dependencies) == 0 || len(dependencies) > 256 {
			return ErrCoordinatorInvalid
		}
		seen := map[uuid.UUID]struct{}{}
		for _, dependency := range dependencies {
			if dependency == ids[i] {
				return ErrCoordinatorInvalid
			}
			if _, duplicate := seen[dependency]; duplicate {
				return ErrCoordinatorInvalid
			}
			seen[dependency] = struct{}{}
			if _, isNewTask := newTaskIDs[dependency]; !isNewTask {
				var exists bool
				if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM coordination.tasks WHERE workflow_id=$1 AND task_id=$2)`, workflowID, dependency).Scan(&exists); err != nil {
					return err
				}
				if !exists {
					return ErrCoordinatorInvalid
				}
			}
			dependenciesByTask[i] = append(dependenciesByTask[i], dependency)
		}
	}
	if hasTaskDependencyCycle(ids, dependenciesByTask) {
		return ErrCoordinatorInvalid
	}
	for i, dependencies := range dependenciesByTask {
		for _, dependency := range dependencies {
			if _, err := tx.Exec(ctx, `INSERT INTO coordination.dependencies(workflow_id,task_id,depends_on) VALUES($1,$2,$3)`, workflowID, ids[i], dependency); err != nil {
				return err
			}
		}
	}
	return nil
}

func nullableVersion(version int) any {
	if version == 0 {
		return nil
	}
	return version
}

func hasTaskDependencyCycle(ids []uuid.UUID, dependencies [][]uuid.UUID) bool {
	indices := make(map[uuid.UUID]int, len(ids))
	for index, id := range ids {
		indices[id] = index
	}
	state := make([]uint8, len(ids))
	var visit func(int) bool
	visit = func(index int) bool {
		if state[index] == 1 {
			return true
		}
		if state[index] == 2 {
			return false
		}
		state[index] = 1
		for _, dependency := range dependencies[index] {
			if next, exists := indices[dependency]; exists && visit(next) {
				return true
			}
		}
		state[index] = 2
		return false
	}
	for index := range ids {
		if visit(index) {
			return true
		}
	}
	return false
}

func settleReadyJoinTasks(ctx context.Context, tx pgx.Tx, workflowRetention time.Duration) error {
	for {
		command, err := tx.Exec(ctx, `UPDATE coordination.tasks task SET state='succeeded',result='{}'::jsonb,
			lease_owner=NULL,lease_until=NULL,error_code=NULL,updated_at=clock_timestamp()
			WHERE task.task_kind='join' AND task.state='queued'
			AND EXISTS(SELECT 1 FROM coordination.dependencies d WHERE d.workflow_id=task.workflow_id AND d.task_id=task.task_id)
			AND NOT EXISTS(SELECT 1 FROM coordination.dependencies d JOIN coordination.tasks prerequisite
				ON prerequisite.workflow_id=d.workflow_id AND prerequisite.task_id=d.depends_on
				WHERE d.workflow_id=task.workflow_id AND d.task_id=task.task_id AND prerequisite.state<>'succeeded')
			AND EXISTS(SELECT 1 FROM coordination.workflows workflow WHERE workflow.workflow_id=task.workflow_id
				AND workflow.state IN ('queued','running'))`)
		if err != nil {
			return err
		}
		if command.RowsAffected() == 0 {
			break
		}
	}
	_, err := tx.Exec(ctx, `UPDATE coordination.workflows workflow SET state='succeeded',result=root.result,
		expires_at=clock_timestamp()+($1::bigint*interval '1 microsecond'),updated_at=clock_timestamp()
		FROM coordination.tasks root WHERE root.workflow_id=workflow.workflow_id AND root.task_id=workflow.root_task_id
		AND root.state='succeeded' AND workflow.state IN ('queued','running')
		AND NOT EXISTS(SELECT 1 FROM coordination.tasks task WHERE task.workflow_id=workflow.workflow_id AND task.state<>'succeeded')`,
		workflowRetention.Microseconds())
	return err
}

func FailTask(ctx context.Context, pool *pgxpool.Pool, taskID, owner uuid.UUID, epoch, token int64,
	errorCode string, retryable bool, retryAfter, workflowRetention time.Duration) error {
	if ctx == nil || pool == nil || taskID == uuid.Nil || owner == uuid.Nil || epoch <= 0 || token <= 0 ||
		!coordinatorErrorCode.MatchString(errorCode) || retryAfter < 0 || workflowRetention <= 0 || workflowRetention > maxCoordinatorWorkflowRetention {
		return ErrCoordinatorInvalid
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := verifyCoordinatorInstance(ctx, tx, owner, epoch); err != nil {
		return err
	}
	var workflowID uuid.UUID
	err = tx.QueryRow(ctx, `SELECT w.workflow_id FROM coordination.workflows w
		JOIN coordination.tasks t USING(workflow_id)
		WHERE t.task_id=$1 AND w.state='running' FOR UPDATE OF w`, taskID).Scan(&workflowID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrTaskNotOwned
	}
	if err != nil {
		return err
	}
	var retry bool
	err = tx.QueryRow(ctx, `UPDATE coordination.tasks AS task SET state=CASE WHEN $6 AND task.attempts<task.max_attempts THEN 'queued' ELSE 'failed' END,
		available_at=CASE WHEN $6 AND task.attempts<task.max_attempts THEN clock_timestamp()+($7::bigint*interval '1 microsecond') ELSE task.available_at END,
		lease_owner=NULL,lease_until=NULL,error_code=$5,updated_at=clock_timestamp()
		WHERE task.task_id=$1 AND task.lease_owner=$2 AND task.config_epoch=$3 AND task.fencing_token=$4 AND task.state='running'
		AND task.lease_until>clock_timestamp() AND EXISTS(SELECT 1 FROM coordination.instances i JOIN coordination.runtime r
		ON r.singleton=true AND r.epoch=i.config_epoch JOIN coordination.workflows w ON w.workflow_id=task.workflow_id
		AND w.state='running' WHERE i.instance_id=$2 AND i.config_epoch=$3 AND i.lease_until>clock_timestamp())
		AND task.workflow_id=$8 RETURNING task.workflow_id,task.state='queued'`, taskID, owner, epoch, token, errorCode, retryable, retryAfter.Microseconds(), workflowID).Scan(&workflowID, &retry)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrTaskNotOwned
	}
	if err != nil {
		return err
	}
	if !retry {
		if _, err := tx.Exec(ctx, `UPDATE coordination.workflows SET state='failed',
			expires_at=clock_timestamp()+($2::bigint*interval '1 microsecond'),updated_at=clock_timestamp()
			WHERE workflow_id=$1 AND state IN ('queued','running')`, workflowID, workflowRetention.Microseconds()); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE coordination.tasks SET state='cancelled',lease_owner=NULL,lease_until=NULL,
			error_code='workflow_failed',updated_at=clock_timestamp() WHERE workflow_id=$1 AND state IN ('queued','running')`, workflowID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func RetryTask(ctx context.Context, pool *pgxpool.Pool, owner, targetWorkflowID, taskID uuid.UUID, epoch int64) error {
	if ctx == nil || pool == nil || owner == uuid.Nil || targetWorkflowID == uuid.Nil || taskID == uuid.Nil || epoch <= 0 {
		return ErrCoordinatorInvalid
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := verifyCoordinatorInstance(ctx, tx, owner, epoch); err != nil {
		return err
	}
	var workflowID uuid.UUID
	err = tx.QueryRow(ctx, `SELECT workflow.workflow_id FROM coordination.workflows AS workflow
		JOIN coordination.tasks AS task ON task.workflow_id=workflow.workflow_id
		WHERE workflow.workflow_id=$1 AND task.task_id=$2 AND task.task_kind='operation' AND task.state='failed'
		AND workflow.state='failed' FOR UPDATE OF workflow`, targetWorkflowID, taskID).Scan(&workflowID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrTaskMissing
	}
	if err != nil {
		return err
	}
	err = tx.QueryRow(ctx, `UPDATE coordination.tasks AS task SET state='queued',attempts=0,available_at=clock_timestamp(),
		lease_owner=NULL,lease_until=NULL,error_code=NULL,result=NULL,config_epoch=$3,updated_at=clock_timestamp()
		WHERE task.task_id=$1 AND task.workflow_id=$2 AND task.task_kind='operation' AND task.state='failed' RETURNING task.workflow_id`, taskID, workflowID, epoch).Scan(&workflowID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrTaskMissing
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE coordination.tasks SET state='queued',attempts=0,available_at=clock_timestamp(),
		lease_owner=NULL,lease_until=NULL,error_code=NULL,result=NULL,config_epoch=$2,updated_at=clock_timestamp()
		WHERE workflow_id=$1 AND state='cancelled' AND error_code='workflow_failed'`, workflowID, epoch); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE coordination.workflows SET state='queued',result=NULL,expires_at=NULL,config_epoch=$2,updated_at=clock_timestamp()
		WHERE workflow_id=$1`, workflowID, epoch); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type rowScanner interface{ Scan(...any) error }

func scanTask(row rowScanner) (Task, error) {
	var task Task
	var kind string
	var taskClass, operationID pgtype.Text
	var contractVersion pgtype.Int4
	var leaseOwner pgtype.UUID
	var leaseUntil pgtype.Timestamptz
	var errorCode pgtype.Text
	var payload, result []byte
	err := row.Scan(&task.ID, &task.WorkflowID, &kind, &taskClass, &operationID, &contractVersion,
		&task.EmissionKey, &task.IdempotencyKey, &payload, &task.State, &task.Attempts, &task.MaxAttempts, &task.AvailableAt, &leaseOwner,
		&leaseUntil, &task.FencingToken, &task.ConfigEpoch, &result, &errorCode, &task.CreatedAt, &task.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Task{}, ErrTaskMissing
	}
	if err != nil {
		return Task{}, err
	}
	task.Kind = coordination.TaskKind(kind)
	if task.Kind == coordination.TaskKindOperation {
		if !taskClass.Valid || !operationID.Valid || !contractVersion.Valid {
			return Task{}, ErrCoordinatorInvalid
		}
		task.Operation = &coordination.OperationRef{ClassID: taskClass.String, ID: operationID.String,
			Version: int(contractVersion.Int32)}
	}
	task.Payload = append(json.RawMessage(nil), payload...)
	task.Result = append(json.RawMessage(nil), result...)
	if errorCode.Valid {
		task.ErrorCode = errorCode.String
	}
	if leaseOwner.Valid {
		task.LeaseOwner = uuid.UUID(leaseOwner.Bytes)
	}
	if leaseUntil.Valid {
		value := leaseUntil.Time
		task.LeaseUntil = &value
	}
	return task, nil
}

func scanTasks(rows pgx.Rows) ([]Task, error) {
	tasks := make([]Task, 0)
	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	return tasks, rows.Err()
}

func verifyCoordinatorInstance(ctx context.Context, tx pgx.Tx, instanceID uuid.UUID, epoch int64) error {
	if instanceID == uuid.Nil || epoch <= 0 {
		return ErrCoordinatorInvalid
	}
	var valid bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM coordination.instances i JOIN coordination.runtime r
		ON r.singleton=true AND r.epoch=i.config_epoch WHERE i.instance_id=$1 AND i.config_epoch=$2 AND i.lease_until>clock_timestamp())`, instanceID, epoch).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return ErrCoordinatorLeaseLost
	}
	return nil
}

func validCoordinatorJSON(payload json.RawMessage) bool {
	if len(payload) == 0 || len(payload) > maxCoordinatorPayloadBytes || !utf8.Valid(payload) || !json.Valid(payload) {
		return false
	}
	var object map[string]json.RawMessage
	return json.Unmarshal(payload, &object) == nil && object != nil
}

func validCoordinatorIdempotencyKey(value string) bool {
	if value == "" || len(value) > 256 || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return false
	}
	for _, char := range value {
		if unicode.IsControl(char) {
			return false
		}
	}
	return true
}

func equalBytes(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
