package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
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
	ErrCoordinatorConflict    = coordination.ErrConflict
	ErrCoordinatorLeaseLost   = coordination.ErrLeaseLost
	ErrCoordinatorConfigDiff  = coordination.ErrConfigDiff
	ErrCoordinatorJobMissing  = coordination.ErrTaskMissing
	ErrCoordinatorJobNotOwned = coordination.ErrTaskNotOwned
	ErrCoordinatorStale       = coordination.ErrStale
	ErrCoordinatorInvalid     = coordination.ErrInvalid
	ErrCoordinatorLimit       = coordination.ErrLimit
)

var (
	coordinatorJobType     = regexp.MustCompile(`^[a-z][a-z0-9._/-]{0,127}$`)
	coordinatorComponentID = regexp.MustCompile(`^[a-z][a-z0-9.-]{0,63}$`)
	coordinatorOperationID = regexp.MustCompile(`^[a-z][a-z0-9._/-]{0,127}$`)
	coordinatorErrorCode   = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,127}$`)
	coordinatorFingerprint = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

const maxCoordinatorPayloadBytes = 1 << 20
const maxCoordinatorChildrenPerTask = 256
const maxCoordinatorWorkflowRetention = 365 * 24 * time.Hour

type CoordinatorJob = coordination.CoordinatorJob
type CoordinatorJobCursor = coordination.CoordinatorJobCursor
type CoordinatorJobSummary = coordination.CoordinatorJobSummary
type CoordinatorJobListRequest = coordination.CoordinatorJobListRequest
type CoordinatorJobPage = coordination.CoordinatorJobPage
type CoordinatorTaskOutcome = coordination.CoordinatorTaskOutcome
type CoordinatorTaskChild = coordination.CoordinatorTaskChild

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
	inputMaterial := root.OperationID + "\x00" + fmt.Sprint(root.ContractVersion) + "\x00" + string(root.Payload)
	inputHash := sha256.Sum256([]byte(inputMaterial))
	if ctx == nil || pool == nil || request.ID == uuid.Nil || request.Owner == uuid.Nil || request.ConfigEpoch <= 0 ||
		!coordinatorComponentID.MatchString(request.OwnerEngineID) || !validCoordinatorIdempotencyKey(request.IdempotencyKey) ||
		len(request.InputHash) != sha256.Size || !equalBytes(request.InputHash, inputHash[:]) || root.ID == uuid.Nil || root.ComponentID != request.OwnerEngineID ||
		!coordinatorOperationID.MatchString(root.OperationID) || root.ContractVersion < 1 || root.ContractVersion > 1<<16 ||
		root.MaxAttempts < 1 || root.MaxAttempts > 100 || !validCoordinatorJSON(root.Payload) ||
		request.Retention <= 0 || request.Retention > maxCoordinatorWorkflowRetention ||
		(root.WorkflowID != uuid.Nil && root.WorkflowID != request.ID) || root.ParentTaskID != uuid.Nil || root.EmissionKey != "" || len(root.DependsOn) > 0 {
		return coordination.Workflow{}, false, ErrCoordinatorInvalid
	}
	keyHash := sha256.Sum256([]byte(request.OwnerEngineID + "\x00" + request.IdempotencyKey))
	idempotencyKey := hex.EncodeToString(keyHash[:])
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return coordination.Workflow{}, false, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := verifyCoordinatorInstance(ctx, tx, request.Owner, request.ConfigEpoch); err != nil {
		return coordination.Workflow{}, false, err
	}
	workflow, err := getWorkflowByIdempotencyKey(ctx, tx, request.OwnerEngineID, idempotencyKey)
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
	err = tx.QueryRow(ctx, `INSERT INTO coordination.workflows(workflow_id,root_task_id,owner_engine_id,idempotency_key,
		input_hash,config_epoch,state) VALUES($1,$2,$3,$4,$5,$6,'queued')
		ON CONFLICT(owner_engine_id,idempotency_key) DO NOTHING RETURNING workflow_id`, request.ID, root.ID,
		request.OwnerEngineID, idempotencyKey, request.InputHash, request.ConfigEpoch).Scan(&insertedID)
	if errors.Is(err, pgx.ErrNoRows) {
		workflow, err = getWorkflowByIdempotencyKey(ctx, tx, request.OwnerEngineID, idempotencyKey)
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
	_, err = tx.Exec(ctx, `INSERT INTO coordination.tasks(task_id,workflow_id,component_id,operation_id,contract_version,
		payload,state,max_attempts,available_at,config_epoch) VALUES($1,$2,$3,$4,$5,$6::jsonb,'queued',$7,
		clock_timestamp(),$8)`, root.ID, request.ID, root.ComponentID, root.OperationID, root.ContractVersion,
		[]byte(root.Payload), root.MaxAttempts, request.ConfigEpoch)
	if err != nil {
		return coordination.Workflow{}, false, err
	}
	result := coordination.Workflow{ID: request.ID, OwnerEngineID: request.OwnerEngineID, RootTaskID: root.ID, State: "queued"}
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
}, ownerEngineID, key string) (workflowRow, error) {
	return scanWorkflow(db.QueryRow(ctx, `SELECT workflow_id,owner_engine_id,state,root_task_id,result,input_hash,created_at,updated_at
		FROM coordination.workflows WHERE owner_engine_id=$1 AND idempotency_key=$2`, ownerEngineID, key))
}

func GetCoordinatorWorkflow(ctx context.Context, pool *pgxpool.Pool, ownerEngineID string, instanceID uuid.UUID, epoch int64, workflowID uuid.UUID) (coordination.Workflow, error) {
	if ctx == nil || pool == nil || !coordinatorComponentID.MatchString(ownerEngineID) || instanceID == uuid.Nil || epoch <= 0 || workflowID == uuid.Nil {
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
	workflow, err := scanWorkflow(tx.QueryRow(ctx, `SELECT workflow_id,owner_engine_id,state,root_task_id,result,input_hash,created_at,updated_at
		FROM coordination.workflows WHERE owner_engine_id=$1 AND workflow_id=$2`, ownerEngineID, workflowID))
	if errors.Is(err, pgx.ErrNoRows) {
		return coordination.Workflow{}, ErrCoordinatorJobMissing
	}
	if err != nil {
		return coordination.Workflow{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return coordination.Workflow{}, err
	}
	return workflow.Workflow, nil
}

func CancelCoordinatorWorkflow(ctx context.Context, pool *pgxpool.Pool, ownerEngineID string, instanceID uuid.UUID, epoch int64, workflowID uuid.UUID, retention time.Duration) error {
	if ctx == nil || pool == nil || !coordinatorComponentID.MatchString(ownerEngineID) || instanceID == uuid.Nil || epoch <= 0 || workflowID == uuid.Nil ||
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
	err = tx.QueryRow(ctx, `SELECT state FROM coordination.workflows WHERE owner_engine_id=$1 AND workflow_id=$2 FOR UPDATE`, ownerEngineID, workflowID).Scan(&state)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrCoordinatorJobMissing
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
	err := row.Scan(&result.Workflow.ID, &result.Workflow.OwnerEngineID, &result.Workflow.State,
		&result.Workflow.RootTaskID, &payload, &result.inputHash, &result.Workflow.CreatedAt, &result.Workflow.UpdatedAt)
	if err != nil {
		return workflowRow{}, err
	}
	result.Workflow.Result = append(json.RawMessage(nil), payload...)
	return result, nil
}

func ClaimCoordinatorJobs(ctx context.Context, pool *pgxpool.Pool, owner uuid.UUID, epoch int64, limit int, lease, workflowRetention time.Duration) ([]CoordinatorJob, error) {
	if ctx == nil || pool == nil || owner == uuid.Nil || epoch <= 0 || limit < 1 || limit > 256 || lease < time.Second || lease > time.Hour ||
		workflowRetention <= 0 || workflowRetention > maxCoordinatorWorkflowRetention {
		return nil, ErrCoordinatorInvalid
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := verifyCoordinatorInstance(ctx, tx, owner, epoch); err != nil {
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
		AND t.state='queued' AND t.available_at<=clock_timestamp() AND NOT EXISTS(
			SELECT 1 FROM coordination.dependencies d JOIN coordination.tasks prerequisite
			ON prerequisite.workflow_id=d.workflow_id AND prerequisite.task_id=d.depends_on
			WHERE d.workflow_id=t.workflow_id AND d.task_id=t.task_id AND prerequisite.state<>'succeeded'))`); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `WITH candidates AS (
		SELECT t.task_id FROM coordination.tasks t JOIN coordination.workflows w USING(workflow_id)
		WHERE t.state='queued' AND t.available_at<=clock_timestamp() AND t.attempts<t.max_attempts
			AND w.state='running' AND w.config_epoch=$3
		AND NOT EXISTS(SELECT 1 FROM coordination.dependencies d JOIN coordination.tasks prerequisite
			ON prerequisite.workflow_id=d.workflow_id AND prerequisite.task_id=d.depends_on
			WHERE d.workflow_id=t.workflow_id AND d.task_id=t.task_id AND prerequisite.state<>'succeeded')
		ORDER BY t.available_at,t.created_at,t.task_id LIMIT $4 FOR UPDATE OF t SKIP LOCKED
	), claimed AS (
		UPDATE coordination.tasks t SET state='running',attempts=t.attempts+1,lease_owner=$1,
		lease_until=clock_timestamp()+($2::bigint*interval '1 microsecond'),fencing_token=t.fencing_token+1,
		config_epoch=$3,updated_at=clock_timestamp() FROM candidates c WHERE t.task_id=c.task_id
		RETURNING t.*
	) SELECT c.task_id,c.workflow_id,c.component_id,c.operation_id,c.contract_version,COALESCE(c.emission_key,''),
		w.idempotency_key,c.payload,c.state,c.attempts,c.max_attempts,c.available_at,c.lease_owner,c.lease_until,c.fencing_token,c.config_epoch,
		c.result,COALESCE(c.error_code,''),c.created_at,c.updated_at FROM claimed c JOIN coordination.workflows w USING(workflow_id)`, owner, lease.Microseconds(), epoch, limit)
	if err != nil {
		return nil, err
	}
	jobs, err := scanCoordinatorJobs(rows)
	rows.Close()
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return jobs, nil
}

func ExtendCoordinatorJobLease(ctx context.Context, pool *pgxpool.Pool, jobID, owner uuid.UUID, epoch, token int64, lease time.Duration) error {
	if ctx == nil || pool == nil || jobID == uuid.Nil || owner == uuid.Nil || epoch <= 0 || token <= 0 || lease < time.Second || lease > time.Hour {
		return ErrCoordinatorInvalid
	}
	command, err := pool.Exec(ctx, `UPDATE coordination.tasks t SET lease_until=clock_timestamp()+($5::bigint*interval '1 microsecond'),
		updated_at=clock_timestamp() WHERE t.task_id=$1 AND t.lease_owner=$2 AND t.config_epoch=$3 AND t.fencing_token=$4
		AND t.state='running' AND t.lease_until>clock_timestamp() AND EXISTS(SELECT 1 FROM coordination.instances i
		JOIN coordination.runtime r ON r.singleton=true AND r.epoch=i.config_epoch
		JOIN coordination.workflows w ON w.workflow_id=t.workflow_id AND w.state='running'
		WHERE i.instance_id=$2 AND i.config_epoch=$3 AND i.lease_until>clock_timestamp())`, jobID, owner, epoch, token, lease.Microseconds())
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrCoordinatorJobNotOwned
	}
	return nil
}

// CompleteCoordinatorTask commits the parent result and its dynamically
// emitted children in one transaction. A stale lease cannot commit either.
func CompleteCoordinatorTask(ctx context.Context, pool *pgxpool.Pool, jobID, owner uuid.UUID, epoch, token int64,
	maxWorkflowTasks, maxChildren int, workflowRetention time.Duration, outcome CoordinatorTaskOutcome) error {
	if ctx == nil || pool == nil || jobID == uuid.Nil || owner == uuid.Nil || epoch <= 0 || token <= 0 ||
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
		WHERE t.task_id=$1 AND w.state IN ('queued','running') FOR UPDATE OF w`, jobID).Scan(&workflowID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrCoordinatorJobNotOwned
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
		jobID, owner, epoch, token, []byte(outcome.Result)).Scan(&workflowID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrCoordinatorJobNotOwned
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
	if err := insertCoordinatorChildren(ctx, tx, workflowID, jobID, epoch, outcome.Children); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE coordination.workflows w SET state='succeeded',result=root.result,
		expires_at=clock_timestamp()+($2::bigint*interval '1 microsecond'),updated_at=clock_timestamp()
		FROM coordination.tasks root WHERE w.workflow_id=$1 AND root.workflow_id=w.workflow_id AND root.task_id=w.root_task_id
		AND w.state IN ('queued','running') AND NOT EXISTS(SELECT 1 FROM coordination.tasks t
		WHERE t.workflow_id=w.workflow_id AND t.state IN ('queued','running','failed','cancelled','skipped'))`, workflowID, workflowRetention.Microseconds()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func insertCoordinatorChildren(ctx context.Context, tx pgx.Tx, workflowID, parentID uuid.UUID, epoch int64, children []CoordinatorTaskChild) error {
	if len(children) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, len(children))
	keys := make(map[string]struct{}, len(children))
	newTaskIDs := make(map[uuid.UUID]struct{}, len(children))
	for i, child := range children {
		if !coordinatorComponentID.MatchString(child.ComponentID) || !coordinatorOperationID.MatchString(child.OperationID) || child.ContractVersion < 1 || child.ContractVersion > 1<<16 ||
			!validCoordinatorIdempotencyKey(child.EmissionKey) || !validCoordinatorJSON(child.Payload) || child.MaxAttempts < 1 || child.MaxAttempts > 100 {
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
	}
	for i, child := range children {
		var availableAt any
		if !child.AvailableAt.IsZero() {
			availableAt = child.AvailableAt.UTC()
		}
		_, err := tx.Exec(ctx, `INSERT INTO coordination.tasks(task_id,workflow_id,component_id,operation_id,contract_version,
			parent_task_id,emission_key,payload,state,max_attempts,available_at,config_epoch)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8::jsonb,'queued',$9,COALESCE($10::timestamptz,clock_timestamp()),$11)`, ids[i], workflowID, child.ComponentID,
			child.OperationID, child.ContractVersion, parentID, child.EmissionKey, []byte(child.Payload), child.MaxAttempts, availableAt, epoch)
		if err != nil {
			return err
		}
	}
	for i, child := range children {
		dependencies := child.DependsOn
		if len(dependencies) == 0 {
			dependencies = []uuid.UUID{parentID}
		}
		seen := map[uuid.UUID]struct{}{}
		for _, dependency := range dependencies {
			if dependency == ids[i] {
				return ErrCoordinatorInvalid
			}
			if _, isNewTask := newTaskIDs[dependency]; isNewTask {
				return ErrCoordinatorInvalid
			}
			if _, duplicate := seen[dependency]; duplicate {
				return ErrCoordinatorInvalid
			}
			seen[dependency] = struct{}{}
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM coordination.tasks WHERE workflow_id=$1 AND task_id=$2)`, workflowID, dependency).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				return ErrCoordinatorInvalid
			}
			if _, err := tx.Exec(ctx, `INSERT INTO coordination.dependencies(workflow_id,task_id,depends_on) VALUES($1,$2,$3)`, workflowID, ids[i], dependency); err != nil {
				return err
			}
		}
	}
	return nil
}

func FailCoordinatorJob(ctx context.Context, pool *pgxpool.Pool, jobID, owner uuid.UUID, epoch, token int64,
	errorCode string, retryable bool, retryAfter, workflowRetention time.Duration) error {
	if ctx == nil || pool == nil || jobID == uuid.Nil || owner == uuid.Nil || epoch <= 0 || token <= 0 ||
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
		WHERE t.task_id=$1 AND w.state='running' FOR UPDATE OF w`, jobID).Scan(&workflowID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrCoordinatorJobNotOwned
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
		AND task.workflow_id=$8 RETURNING task.workflow_id,task.state='queued'`, jobID, owner, epoch, token, errorCode, retryable, retryAfter.Microseconds(), workflowID).Scan(&workflowID, &retry)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrCoordinatorJobNotOwned
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

func RetryCoordinatorJob(ctx context.Context, pool *pgxpool.Pool, owner, jobID uuid.UUID, epoch int64) error {
	if ctx == nil || pool == nil || owner == uuid.Nil || jobID == uuid.Nil || epoch <= 0 {
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
		WHERE task.task_id=$1 AND task.state='failed' AND workflow.state='failed' FOR UPDATE OF workflow`, jobID).Scan(&workflowID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrCoordinatorJobMissing
	}
	if err != nil {
		return err
	}
	err = tx.QueryRow(ctx, `UPDATE coordination.tasks AS task SET state='queued',attempts=0,available_at=clock_timestamp(),
		lease_owner=NULL,lease_until=NULL,error_code=NULL,result=NULL,updated_at=clock_timestamp()
		WHERE task.task_id=$1 AND task.workflow_id=$2 AND task.state='failed' RETURNING task.workflow_id`, jobID, workflowID).Scan(&workflowID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrCoordinatorJobMissing
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

func GetCoordinatorJob(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) (CoordinatorJob, error) {
	if ctx == nil || pool == nil || id == uuid.Nil {
		return CoordinatorJob{}, ErrCoordinatorInvalid
	}
	return getCoordinatorJobByID(ctx, pool, id)
}

func ListCoordinatorJobs(ctx context.Context, pool *pgxpool.Pool, request CoordinatorJobListRequest) (CoordinatorJobPage, error) {
	if ctx == nil || pool == nil || request.Owner == uuid.Nil || request.ConfigEpoch <= 0 || request.Limit < 1 || request.Limit > 200 ||
		(request.Type != "" && !coordinatorJobType.MatchString(request.Type)) || (request.State != "" && !validCoordinatorJobState(request.State)) {
		return CoordinatorJobPage{}, ErrCoordinatorInvalid
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return CoordinatorJobPage{}, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := verifyCoordinatorInstance(ctx, tx, request.Owner, request.ConfigEpoch); err != nil {
		return CoordinatorJobPage{}, err
	}
	typeOperation := request.Type
	var typeVersion any
	if request.Type != "" {
		cut := strings.LastIndex(request.Type, "/v")
		if cut <= 0 {
			return CoordinatorJobPage{}, ErrCoordinatorInvalid
		}
		version, parseErr := parsePositiveVersion(request.Type[cut+2:])
		if parseErr != nil {
			return CoordinatorJobPage{}, ErrCoordinatorInvalid
		}
		typeOperation, typeVersion = request.Type[:cut], version
	}
	var beforeAt any
	var beforeID any
	if request.Before != nil {
		if request.Before.ID == uuid.Nil || request.Before.CreatedAt.IsZero() {
			return CoordinatorJobPage{}, ErrCoordinatorInvalid
		}
		beforeAt, beforeID = request.Before.CreatedAt, request.Before.ID
	}
	rows, err := tx.Query(ctx, `SELECT task_id,component_id,operation_id,contract_version,state,attempts,max_attempts,available_at,
		lease_until,error_code,created_at,updated_at FROM coordination.tasks
		WHERE ($1='' OR operation_id=$1) AND ($2::integer IS NULL OR contract_version=$2)
		AND ($3='' OR state=$3) AND ($4::timestamptz IS NULL OR (created_at,task_id)<($4,$5::uuid))
		ORDER BY created_at DESC,task_id DESC LIMIT $6`, typeOperation, typeVersion, request.State, beforeAt, beforeID, request.Limit+1)
	if err != nil {
		return CoordinatorJobPage{}, err
	}
	page := CoordinatorJobPage{Items: make([]CoordinatorJobSummary, 0, request.Limit)}
	for rows.Next() {
		var item CoordinatorJobSummary
		var leaseUntil pgtype.Timestamptz
		var errorCode pgtype.Text
		if err := rows.Scan(&item.ID, &item.ComponentID, &item.OperationID, &item.ContractVersion, &item.State, &item.Attempts, &item.MaxAttempts,
			&item.AvailableAt, &leaseUntil, &errorCode, &item.CreatedAt, &item.UpdatedAt); err != nil {
			rows.Close()
			return CoordinatorJobPage{}, err
		}
		item.Type = item.OperationID + "/v" + fmt.Sprint(item.ContractVersion)
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
		return CoordinatorJobPage{}, err
	}
	rows.Close()
	if len(page.Items) > request.Limit {
		page.Items = page.Items[:request.Limit]
		last := page.Items[len(page.Items)-1]
		page.Next = &CoordinatorJobCursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	if err := tx.Commit(ctx); err != nil {
		return CoordinatorJobPage{}, err
	}
	return page, nil
}

func validCoordinatorJobState(state string) bool {
	switch state {
	case "queued", "running", "succeeded", "failed", "cancelled", "skipped":
		return true
	default:
		return false
	}
}

func parsePositiveVersion(value string) (int, error) {
	version, err := strconv.Atoi(value)
	if err != nil || version < 1 || version > 1<<16 {
		return 0, ErrCoordinatorInvalid
	}
	return version, nil
}

func getCoordinatorJobByID(ctx context.Context, db interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, id uuid.UUID) (CoordinatorJob, error) {
	return scanCoordinatorJob(db.QueryRow(ctx, `SELECT t.task_id,t.workflow_id,t.component_id,t.operation_id,t.contract_version,
		COALESCE(t.emission_key,''),w.idempotency_key,t.payload,t.state,
		t.attempts,t.max_attempts,t.available_at,t.lease_owner,t.lease_until,t.fencing_token,t.config_epoch,t.result,
		COALESCE(t.error_code,''),t.created_at,t.updated_at FROM coordination.tasks t
		JOIN coordination.workflows w USING(workflow_id) WHERE t.task_id=$1`, id))
}

type rowScanner interface{ Scan(...any) error }

func scanCoordinatorJob(row rowScanner) (CoordinatorJob, error) {
	var job CoordinatorJob
	var leaseOwner pgtype.UUID
	var leaseUntil pgtype.Timestamptz
	var errorCode pgtype.Text
	var payload, result []byte
	err := row.Scan(&job.ID, &job.WorkflowID, &job.ComponentID, &job.OperationID, &job.ContractVersion,
		&job.EmissionKey, &job.IdempotencyKey, &payload, &job.State, &job.Attempts, &job.MaxAttempts, &job.AvailableAt, &leaseOwner,
		&leaseUntil, &job.FencingToken, &job.ConfigEpoch, &result, &errorCode, &job.CreatedAt, &job.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return CoordinatorJob{}, ErrCoordinatorJobMissing
	}
	if err != nil {
		return CoordinatorJob{}, err
	}
	job.Type = job.OperationID + "/v" + fmt.Sprint(job.ContractVersion)
	job.Payload = append(json.RawMessage(nil), payload...)
	job.Result = append(json.RawMessage(nil), result...)
	if errorCode.Valid {
		job.ErrorCode = errorCode.String
	}
	if leaseOwner.Valid {
		job.LeaseOwner = uuid.UUID(leaseOwner.Bytes)
	}
	if leaseUntil.Valid {
		value := leaseUntil.Time
		job.LeaseUntil = &value
	}
	return job, nil
}

func scanCoordinatorJobs(rows pgx.Rows) ([]CoordinatorJob, error) {
	jobs := make([]CoordinatorJob, 0)
	for rows.Next() {
		job, err := scanCoordinatorJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
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

func normalizeChildDependencies(parent uuid.UUID, child CoordinatorTaskChild) ([]uuid.UUID, error) {
	if len(child.DependsOn) == 0 {
		return []uuid.UUID{parent}, nil
	}
	seen := make(map[uuid.UUID]struct{}, len(child.DependsOn))
	for _, id := range child.DependsOn {
		if id == uuid.Nil || id == parent {
			return nil, fmt.Errorf("%w: invalid dependency", ErrCoordinatorInvalid)
		}
		if _, exists := seen[id]; exists {
			return nil, ErrCoordinatorInvalid
		}
		seen[id] = struct{}{}
	}
	return child.DependsOn, nil
}
