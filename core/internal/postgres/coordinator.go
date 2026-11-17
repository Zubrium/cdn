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

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrCoordinatorConflict        = errors.New("coordinator state conflict")
	ErrCoordinatorLeaseLost       = errors.New("coordinator lease lost")
	ErrCoordinatorConfigDiff      = errors.New("coordinator configuration differs from active instances")
	ErrCoordinatorJobMissing      = errors.New("coordinator job not found")
	ErrCoordinatorJobNotOwned     = errors.New("coordinator job lease is not owned")
	ErrCoordinatorStale           = errors.New("publication generation is stale")
	ErrRouteTargetMissing         = errors.New("route target not found")
	ErrCoordinatorInvalid         = errors.New("invalid coordinator operation")
	ErrRouteTargetFileUnavailable = errors.New("route target file is unavailable")
)

var (
	coordinatorSystemPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)
	coordinatorJobType       = regexp.MustCompile(`^[a-z][a-z0-9._/-]{0,127}$`)
	coordinatorErrorCode     = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,127}$`)
	coordinatorFingerprint   = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

const maxCoordinatorPayloadBytes = 1 << 20
const coordinatorPublicationJobType = "site-publication/v1"

type CoordinatorJob struct {
	ID             uuid.UUID
	SystemID       string
	Type           string
	IdempotencyKey string
	Payload        json.RawMessage
	State          string
	Attempts       int
	MaxAttempts    int
	AvailableAt    time.Time
	LeaseOwner     uuid.UUID
	LeaseUntil     *time.Time
	FencingToken   int64
	ConfigEpoch    int64
	Result         json.RawMessage
	ErrorCode      string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type CoordinatorJobRequest struct {
	ID             uuid.UUID
	SystemID       string
	Owner          uuid.UUID
	ConfigEpoch    int64
	Type           string
	IdempotencyKey string
	Payload        json.RawMessage
	MaxAttempts    int
}

type CoordinatorPublicationRequest struct {
	SystemID       string
	RouteID        uuid.UUID
	Owner          uuid.UUID
	ConfigEpoch    int64
	Fingerprint    string
	JobID          uuid.UUID
	JobType        string
	IdempotencyKey string
	Payload        json.RawMessage
	MaxAttempts    int
}

type CoordinatorRouteTarget struct {
	SystemID              string
	RouteID               uuid.UUID
	FileID                uuid.UUID
	Revision              int64
	PublicationGeneration int64
	UpdatedAt             time.Time
}

type CoordinatorJobCursor struct {
	CreatedAt time.Time
	ID        uuid.UUID
}

type CoordinatorJobSummary struct {
	ID          uuid.UUID
	SystemID    string
	Type        string
	State       string
	Attempts    int
	MaxAttempts int
	AvailableAt time.Time
	LeaseUntil  *time.Time
	ErrorCode   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type CoordinatorJobListRequest struct {
	SystemID    string
	Owner       uuid.UUID
	ConfigEpoch int64
	Type        string
	State       string
	Limit       int
	Before      *CoordinatorJobCursor
}

type CoordinatorJobPage struct {
	Items []CoordinatorJobSummary
	Next  *CoordinatorJobCursor
}

func validCoordinatorIdentity(systemID string, instanceID uuid.UUID, fingerprint string, lease time.Duration) bool {
	return coordinatorSystemPattern.MatchString(systemID) && instanceID != uuid.Nil &&
		coordinatorFingerprint.MatchString(fingerprint) && lease >= time.Second && lease <= time.Hour
}

// RegisterCoordinatorInstance serializes configuration changes with replica
// registration. Database time controls leases; a different fingerprint is
// accepted only after every prior lease expired or was explicitly released.
func RegisterCoordinatorInstance(ctx context.Context, pool *pgxpool.Pool, systemID string, instanceID uuid.UUID, fingerprint string, lease time.Duration) (int64, error) {
	if ctx == nil || pool == nil || !validCoordinatorIdentity(systemID, instanceID, fingerprint, lease) {
		return 0, ErrCoordinatorInvalid
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `INSERT INTO core.coordinator_config(system_id,fingerprint,epoch)
		VALUES($1,$2,1) ON CONFLICT(system_id) DO NOTHING`, systemID, fingerprint); err != nil {
		return 0, err
	}
	var current string
	var epoch int64
	if err := tx.QueryRow(ctx, `SELECT fingerprint,epoch FROM core.coordinator_config WHERE system_id=$1 FOR UPDATE`, systemID).Scan(&current, &epoch); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM core.coordinator_instances WHERE system_id=$1 AND lease_until<=clock_timestamp()`, systemID); err != nil {
		return 0, err
	}
	var active bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM core.coordinator_instances
		WHERE system_id=$1 AND lease_until>clock_timestamp())`, systemID).Scan(&active); err != nil {
		return 0, err
	}
	if current != fingerprint {
		if active {
			return 0, ErrCoordinatorConfigDiff
		}
		epoch++
		if _, err := tx.Exec(ctx, `UPDATE core.coordinator_config SET fingerprint=$2,epoch=$3,updated_at=clock_timestamp()
			WHERE system_id=$1`, systemID, fingerprint, epoch); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(ctx, `UPDATE core.coordinator_jobs SET state='cancelled',lease_owner=NULL,lease_until=NULL,
			error_code='configuration_changed',updated_at=clock_timestamp()
			WHERE system_id=$1 AND state IN ('queued','running')`, systemID); err != nil {
			return 0, err
		}
	}
	command, err := tx.Exec(ctx, `INSERT INTO core.coordinator_instances(system_id,instance_id,config_epoch,lease_until)
		VALUES($1,$2,$3,clock_timestamp()+($4::bigint * interval '1 microsecond')) ON CONFLICT(system_id,instance_id) DO NOTHING`, systemID, instanceID, epoch, lease.Microseconds())
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

func RenewCoordinatorInstance(ctx context.Context, pool *pgxpool.Pool, systemID string, instanceID uuid.UUID, epoch int64, lease time.Duration) error {
	if ctx == nil || pool == nil || !coordinatorSystemPattern.MatchString(systemID) || instanceID == uuid.Nil || epoch <= 0 || lease < time.Second || lease > time.Hour {
		return ErrCoordinatorInvalid
	}
	command, err := pool.Exec(ctx, `UPDATE core.coordinator_instances SET lease_until=clock_timestamp()+($4::bigint * interval '1 microsecond'),
		heartbeat_at=clock_timestamp() WHERE system_id=$1 AND instance_id=$2 AND config_epoch=$3
		AND lease_until>clock_timestamp() AND EXISTS(SELECT 1 FROM core.coordinator_config c
		WHERE c.system_id=$1 AND c.epoch=$3)`, systemID, instanceID, epoch, lease.Microseconds())
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrCoordinatorLeaseLost
	}
	return nil
}

func verifyCoordinatorInstance(ctx context.Context, tx pgx.Tx, systemID string, instanceID uuid.UUID, epoch int64) error {
	// Hold the configuration row until this transaction commits. This makes an
	// accepted operation linearize before a config-epoch change, rather than
	// allowing an old instance to enqueue or publish after a concurrent restart.
	var currentEpoch int64
	if err := tx.QueryRow(ctx, `SELECT epoch FROM core.coordinator_config WHERE system_id=$1 FOR SHARE`, systemID).Scan(&currentEpoch); err != nil {
		return err
	}
	if currentEpoch != epoch {
		return ErrCoordinatorLeaseLost
	}
	var registered int
	err := tx.QueryRow(ctx, `SELECT 1 FROM core.coordinator_instances i
		WHERE i.system_id=$1 AND i.instance_id=$2 AND i.config_epoch=$3 AND i.lease_until>clock_timestamp()
		FOR SHARE`, systemID, instanceID, epoch).Scan(&registered)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrCoordinatorLeaseLost
	}
	if err != nil {
		return err
	}
	return nil
}

func ReleaseCoordinatorInstance(ctx context.Context, pool *pgxpool.Pool, systemID string, instanceID uuid.UUID, epoch int64) error {
	if ctx == nil || pool == nil || !coordinatorSystemPattern.MatchString(systemID) || instanceID == uuid.Nil || epoch <= 0 {
		return ErrCoordinatorInvalid
	}
	_, err := pool.Exec(ctx, `DELETE FROM core.coordinator_instances WHERE system_id=$1 AND instance_id=$2 AND config_epoch=$3`, systemID, instanceID, epoch)
	return err
}

func validCoordinatorJSON(value json.RawMessage) bool {
	if len(value) == 0 || len(value) > maxCoordinatorPayloadBytes || !utf8.Valid(value) || !json.Valid(value) {
		return false
	}
	var object map[string]json.RawMessage
	return json.Unmarshal(value, &object) == nil && object != nil
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

func EnqueueCoordinatorJob(ctx context.Context, pool *pgxpool.Pool, request CoordinatorJobRequest) (CoordinatorJob, bool, error) {
	if ctx == nil || pool == nil || request.ID == uuid.Nil || !coordinatorSystemPattern.MatchString(request.SystemID) || request.Owner == uuid.Nil || request.ConfigEpoch <= 0 ||
		!coordinatorJobType.MatchString(request.Type) || request.Type == coordinatorPublicationJobType || !validCoordinatorIdempotencyKey(request.IdempotencyKey) ||
		!validCoordinatorJSON(request.Payload) || request.MaxAttempts < 1 || request.MaxAttempts > 100 {
		return CoordinatorJob{}, false, ErrCoordinatorInvalid
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return CoordinatorJob{}, false, err
	}
	defer tx.Rollback(ctx)
	if err := verifyCoordinatorInstance(ctx, tx, request.SystemID, request.Owner, request.ConfigEpoch); err != nil {
		return CoordinatorJob{}, false, err
	}
	command, err := tx.Exec(ctx, `INSERT INTO core.coordinator_jobs(job_id,system_id,job_type,idempotency_key,payload,state,max_attempts)
		VALUES($1,$2,$3,$4,$5::jsonb,'queued',$6) ON CONFLICT(system_id,job_type,idempotency_key) DO NOTHING`,
		request.ID, request.SystemID, request.Type, request.IdempotencyKey, []byte(request.Payload), request.MaxAttempts)
	if err != nil {
		return CoordinatorJob{}, false, err
	}
	created := command.RowsAffected() == 1
	job, err := scanCoordinatorJob(tx.QueryRow(ctx, `SELECT job_id,system_id,job_type,idempotency_key,payload,state,
		attempts,max_attempts,available_at,lease_owner,lease_until,fencing_token,claim_epoch,result,error_code,created_at,updated_at
		FROM core.coordinator_jobs WHERE system_id=$1 AND job_type=$2 AND idempotency_key=$3`, request.SystemID, request.Type, request.IdempotencyKey))
	if err != nil {
		return CoordinatorJob{}, false, err
	}
	if !created {
		var matches bool
		if err := tx.QueryRow(ctx, `SELECT payload=$4::jsonb AND max_attempts=$5 FROM core.coordinator_jobs
			WHERE system_id=$1 AND job_type=$2 AND idempotency_key=$3`, request.SystemID, request.Type,
			request.IdempotencyKey, []byte(request.Payload), request.MaxAttempts).Scan(&matches); err != nil {
			return CoordinatorJob{}, false, err
		}
		if !matches {
			return CoordinatorJob{}, false, ErrCoordinatorConflict
		}
	}
	if err := verifyCoordinatorInstance(ctx, tx, request.SystemID, request.Owner, request.ConfigEpoch); err != nil {
		return CoordinatorJob{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return CoordinatorJob{}, false, err
	}
	return job, created, nil
}

func ClaimCoordinatorJobs(ctx context.Context, pool *pgxpool.Pool, systemID string, owner uuid.UUID, epoch int64, limit int, lease time.Duration) ([]CoordinatorJob, error) {
	if ctx == nil || pool == nil || !coordinatorSystemPattern.MatchString(systemID) || owner == uuid.Nil || epoch <= 0 || limit < 1 || limit > 1000 || lease < time.Second || lease > time.Hour {
		return nil, ErrCoordinatorInvalid
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err := verifyCoordinatorInstance(ctx, tx, systemID, owner, epoch); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE core.coordinator_jobs SET state='failed',lease_owner=NULL,lease_until=NULL,
		error_code=COALESCE(error_code,'lease_expired'),updated_at=clock_timestamp()
		WHERE system_id=$1 AND state='running' AND lease_until<=clock_timestamp() AND attempts>=max_attempts`, systemID); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `WITH candidates AS (
		SELECT job_id FROM core.coordinator_jobs WHERE system_id=$1 AND attempts<max_attempts AND
		((state='queued' AND available_at<=clock_timestamp()) OR (state='running' AND lease_until<=clock_timestamp()))
		ORDER BY available_at,created_at,job_id FOR UPDATE SKIP LOCKED LIMIT $2
	) UPDATE core.coordinator_jobs AS jobs SET state='running',attempts=jobs.attempts+1,
		lease_owner=$3,lease_until=clock_timestamp()+($4::bigint * interval '1 microsecond'),fencing_token=jobs.fencing_token+1,claim_epoch=$5,
		updated_at=clock_timestamp(),error_code=NULL FROM candidates WHERE jobs.job_id=candidates.job_id
	RETURNING jobs.job_id,jobs.system_id,jobs.job_type,jobs.idempotency_key,jobs.payload,jobs.state,
		jobs.attempts,jobs.max_attempts,jobs.available_at,jobs.lease_owner,jobs.lease_until,
		jobs.fencing_token,jobs.claim_epoch,jobs.result,jobs.error_code,jobs.created_at,jobs.updated_at`, systemID, limit, owner, lease.Microseconds(), epoch)
	if err != nil {
		return nil, err
	}
	jobs, err := scanCoordinatorJobs(rows)
	if err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	if err := verifyCoordinatorInstance(ctx, tx, systemID, owner, epoch); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return jobs, nil
}

func ExtendCoordinatorJobLease(ctx context.Context, pool *pgxpool.Pool, systemID string, jobID, owner uuid.UUID, epoch, token int64, lease time.Duration) error {
	if ctx == nil || pool == nil || !coordinatorSystemPattern.MatchString(systemID) || jobID == uuid.Nil || owner == uuid.Nil || epoch <= 0 || token <= 0 || lease < time.Second || lease > time.Hour {
		return ErrCoordinatorInvalid
	}
	command, err := pool.Exec(ctx, `UPDATE core.coordinator_jobs SET lease_until=clock_timestamp()+($6::bigint * interval '1 microsecond'),updated_at=clock_timestamp()
		WHERE system_id=$1 AND job_id=$2 AND lease_owner=$3 AND claim_epoch=$4 AND fencing_token=$5
		AND state='running' AND lease_until>clock_timestamp() AND EXISTS(SELECT 1 FROM core.coordinator_instances i
		JOIN core.coordinator_config c ON c.system_id=i.system_id AND c.epoch=i.config_epoch
		WHERE i.system_id=$1 AND i.instance_id=$3 AND i.config_epoch=$4 AND i.lease_until>clock_timestamp())`,
		systemID, jobID, owner, epoch, token, lease.Microseconds())
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrCoordinatorJobNotOwned
	}
	return nil
}

func CompleteCoordinatorJob(ctx context.Context, pool *pgxpool.Pool, systemID string, jobID, owner uuid.UUID, epoch, token int64, result json.RawMessage) error {
	if ctx == nil || pool == nil || !coordinatorSystemPattern.MatchString(systemID) || jobID == uuid.Nil || owner == uuid.Nil || epoch <= 0 || token <= 0 || !validCoordinatorJSON(result) {
		return ErrCoordinatorInvalid
	}
	command, err := pool.Exec(ctx, `UPDATE core.coordinator_jobs SET state='succeeded',result=$6::jsonb,
		lease_owner=NULL,lease_until=NULL,error_code=NULL,updated_at=clock_timestamp()
		WHERE system_id=$1 AND job_id=$2 AND lease_owner=$3 AND fencing_token=$4 AND claim_epoch=$5
		AND state='running' AND lease_until>clock_timestamp() AND EXISTS(SELECT 1 FROM core.coordinator_instances i
		JOIN core.coordinator_config c ON c.system_id=i.system_id AND c.epoch=i.config_epoch
		WHERE i.system_id=$1 AND i.instance_id=$3 AND i.config_epoch=$5 AND i.lease_until>clock_timestamp())`, systemID, jobID, owner, token, epoch, []byte(result))
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrCoordinatorJobNotOwned
	}
	return nil
}

func FailCoordinatorJob(ctx context.Context, pool *pgxpool.Pool, systemID string, jobID, owner uuid.UUID, epoch, token int64, errorCode string, retryable bool, retryAfter time.Duration) error {
	if ctx == nil || pool == nil || !coordinatorSystemPattern.MatchString(systemID) || jobID == uuid.Nil || owner == uuid.Nil || epoch <= 0 || token <= 0 ||
		!coordinatorErrorCode.MatchString(errorCode) || retryAfter < 0 || retryAfter > 24*time.Hour {
		return ErrCoordinatorInvalid
	}
	command, err := pool.Exec(ctx, `UPDATE core.coordinator_jobs SET
		state=CASE WHEN attempts>=max_attempts OR NOT $7 THEN 'failed' ELSE 'queued' END,
		available_at=clock_timestamp()+($8::bigint * interval '1 microsecond'),lease_owner=NULL,lease_until=NULL,error_code=$6,
		updated_at=clock_timestamp()
		WHERE system_id=$1 AND job_id=$2 AND lease_owner=$3 AND fencing_token=$4 AND claim_epoch=$5
		AND state='running' AND lease_until>clock_timestamp() AND EXISTS(SELECT 1 FROM core.coordinator_instances i
		JOIN core.coordinator_config c ON c.system_id=i.system_id AND c.epoch=i.config_epoch
		WHERE i.system_id=$1 AND i.instance_id=$3 AND i.config_epoch=$5 AND i.lease_until>clock_timestamp())`, systemID, jobID, owner, token, epoch, errorCode, retryable, retryAfter.Microseconds())
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrCoordinatorJobNotOwned
	}
	return nil
}

func RetryCoordinatorJob(ctx context.Context, pool *pgxpool.Pool, systemID string, owner, jobID uuid.UUID, epoch int64) error {
	if ctx == nil || pool == nil || !coordinatorSystemPattern.MatchString(systemID) || owner == uuid.Nil || jobID == uuid.Nil || epoch <= 0 {
		return ErrCoordinatorInvalid
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := verifyCoordinatorInstance(ctx, tx, systemID, owner, epoch); err != nil {
		return err
	}
	var jobType string
	var payload []byte
	err = tx.QueryRow(ctx, `SELECT job_type,payload FROM core.coordinator_jobs
		WHERE system_id=$1 AND job_id=$2 AND state='failed' FOR UPDATE`, systemID, jobID).Scan(&jobType, &payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrCoordinatorJobMissing
	}
	if err != nil {
		return err
	}
	if jobType == coordinatorPublicationJobType {
		var publication struct {
			RouteID    string `json:"route_id"`
			Generation int64  `json:"generation"`
		}
		if err := json.Unmarshal(payload, &publication); err != nil {
			return ErrCoordinatorInvalid
		}
		routeID, err := uuid.Parse(publication.RouteID)
		if err != nil || routeID == uuid.Nil || publication.Generation <= 0 {
			return ErrCoordinatorInvalid
		}
		var currentGeneration int64
		err = tx.QueryRow(ctx, `SELECT generation FROM core.coordinator_publications
			WHERE system_id=$1 AND route_id=$2 FOR SHARE`, systemID, routeID).Scan(&currentGeneration)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && currentGeneration != publication.Generation) {
			return ErrCoordinatorStale
		}
		if err != nil {
			return err
		}
	}
	command, err := tx.Exec(ctx, `UPDATE core.coordinator_jobs SET state='queued',attempts=0,available_at=clock_timestamp(),
		error_code=NULL,result=NULL,updated_at=clock_timestamp() WHERE system_id=$1 AND job_id=$2 AND state='failed'`, systemID, jobID)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrCoordinatorJobMissing
	}
	if err := verifyCoordinatorInstance(ctx, tx, systemID, owner, epoch); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func GetCoordinatorJob(ctx context.Context, pool *pgxpool.Pool, systemID string, jobID uuid.UUID) (CoordinatorJob, error) {
	if ctx == nil || pool == nil || !coordinatorSystemPattern.MatchString(systemID) || jobID == uuid.Nil {
		return CoordinatorJob{}, ErrCoordinatorInvalid
	}
	return getCoordinatorJobByID(ctx, pool, systemID, jobID)
}

func ListCoordinatorJobs(ctx context.Context, pool *pgxpool.Pool, request CoordinatorJobListRequest) (CoordinatorJobPage, error) {
	if ctx == nil || pool == nil || !coordinatorSystemPattern.MatchString(request.SystemID) || request.Owner == uuid.Nil || request.ConfigEpoch <= 0 || request.Limit < 1 || request.Limit > 200 ||
		(request.Type != "" && !coordinatorJobType.MatchString(request.Type)) || (request.State != "" && !validCoordinatorJobState(request.State)) ||
		(request.Before != nil && (request.Before.ID == uuid.Nil || request.Before.CreatedAt.IsZero())) {
		return CoordinatorJobPage{}, ErrCoordinatorInvalid
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return CoordinatorJobPage{}, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := verifyCoordinatorInstance(ctx, tx, request.SystemID, request.Owner, request.ConfigEpoch); err != nil {
		return CoordinatorJobPage{}, err
	}
	var beforeCreatedAt *time.Time
	beforeID := uuid.Nil
	if request.Before != nil {
		beforeCreatedAt = &request.Before.CreatedAt
		beforeID = request.Before.ID
	}
	rows, err := tx.Query(ctx, `SELECT job_id,system_id,job_type,state,attempts,max_attempts,available_at,lease_until,error_code,created_at,updated_at
		FROM core.coordinator_jobs WHERE system_id=$1 AND ($2='' OR job_type=$2) AND ($3='' OR state=$3)
		AND ($4::timestamptz IS NULL OR (created_at,job_id)<($4::timestamptz,$5::uuid))
		ORDER BY created_at DESC,job_id DESC LIMIT $6`, request.SystemID, request.Type, request.State, beforeCreatedAt, beforeID, request.Limit+1)
	if err != nil {
		return CoordinatorJobPage{}, err
	}
	defer rows.Close()
	page := CoordinatorJobPage{Items: make([]CoordinatorJobSummary, 0, request.Limit)}
	for rows.Next() {
		var item CoordinatorJobSummary
		var leaseUntil pgtype.Timestamptz
		var errorCode pgtype.Text
		if err := rows.Scan(&item.ID, &item.SystemID, &item.Type, &item.State, &item.Attempts, &item.MaxAttempts,
			&item.AvailableAt, &leaseUntil, &errorCode, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return CoordinatorJobPage{}, err
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
		return CoordinatorJobPage{}, err
	}
	rows.Close()
	if len(page.Items) > request.Limit {
		page.Items = page.Items[:request.Limit]
		last := page.Items[len(page.Items)-1]
		page.Next = &CoordinatorJobCursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	if err := ctx.Err(); err != nil {
		return CoordinatorJobPage{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return CoordinatorJobPage{}, err
	}
	return page, nil
}

func validCoordinatorJobState(state string) bool {
	switch state {
	case "queued", "running", "succeeded", "failed", "cancelled":
		return true
	default:
		return false
	}
}

func getCoordinatorJobByID(ctx context.Context, pool *pgxpool.Pool, systemID string, id uuid.UUID) (CoordinatorJob, error) {
	return scanCoordinatorJob(pool.QueryRow(ctx, `SELECT job_id,system_id,job_type,idempotency_key,payload,state,
		attempts,max_attempts,available_at,lease_owner,lease_until,fencing_token,claim_epoch,result,error_code,created_at,updated_at
		FROM core.coordinator_jobs WHERE system_id=$1 AND job_id=$2`, systemID, id))
}

type rowScanner interface{ Scan(...any) error }

func scanCoordinatorJob(row rowScanner) (CoordinatorJob, error) {
	var job CoordinatorJob
	var payload, result []byte
	var leaseOwner pgtype.UUID
	var leaseUntil pgtype.Timestamptz
	var errorCode pgtype.Text
	err := row.Scan(&job.ID, &job.SystemID, &job.Type, &job.IdempotencyKey, &payload, &job.State,
		&job.Attempts, &job.MaxAttempts, &job.AvailableAt, &leaseOwner, &leaseUntil, &job.FencingToken, &job.ConfigEpoch,
		&result, &errorCode, &job.CreatedAt, &job.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return CoordinatorJob{}, ErrCoordinatorJobMissing
	}
	if err != nil {
		return CoordinatorJob{}, err
	}
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
	return jobs, nil
}

func setDesiredPublicationTx(ctx context.Context, tx pgx.Tx, systemID string, routeID uuid.UUID, fingerprint string) (int64, error) {
	var generation int64
	err := tx.QueryRow(ctx, `INSERT INTO core.coordinator_publications(system_id,route_id,input_fingerprint,generation)
		VALUES($1,$2,$3,1) ON CONFLICT(system_id,route_id) DO UPDATE SET input_fingerprint=EXCLUDED.input_fingerprint,
		generation=core.coordinator_publications.generation+1,updated_at=clock_timestamp()
		WHERE core.coordinator_publications.input_fingerprint<>EXCLUDED.input_fingerprint
		RETURNING generation`, systemID, routeID, fingerprint).Scan(&generation)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `SELECT generation FROM core.coordinator_publications WHERE system_id=$1 AND route_id=$2 FOR UPDATE`, systemID, routeID).Scan(&generation)
	}
	return generation, err
}

func ScheduleCoordinatorPublication(ctx context.Context, pool *pgxpool.Pool, request CoordinatorPublicationRequest) (int64, CoordinatorJob, bool, error) {
	if ctx == nil || pool == nil || !coordinatorSystemPattern.MatchString(request.SystemID) || request.RouteID == uuid.Nil || request.Owner == uuid.Nil ||
		request.ConfigEpoch <= 0 || !coordinatorFingerprint.MatchString(request.Fingerprint) || request.JobID == uuid.Nil ||
		request.JobType != coordinatorPublicationJobType || !validCoordinatorIdempotencyKey(request.IdempotencyKey) ||
		!validCoordinatorJSON(request.Payload) || request.MaxAttempts < 1 || request.MaxAttempts > 100 {
		return 0, CoordinatorJob{}, false, ErrCoordinatorInvalid
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(request.Payload, &fields); err != nil || fields == nil {
		return 0, CoordinatorJob{}, false, ErrCoordinatorInvalid
	}
	var payloadRouteID string
	if err := json.Unmarshal(fields["route_id"], &payloadRouteID); err != nil || payloadRouteID != request.RouteID.String() {
		return 0, CoordinatorJob{}, false, ErrCoordinatorInvalid
	}
	if _, alreadyHasGeneration := fields["generation"]; alreadyHasGeneration {
		return 0, CoordinatorJob{}, false, ErrCoordinatorInvalid
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, CoordinatorJob{}, false, err
	}
	defer tx.Rollback(ctx)
	if err := verifyCoordinatorInstance(ctx, tx, request.SystemID, request.Owner, request.ConfigEpoch); err != nil {
		return 0, CoordinatorJob{}, false, err
	}
	generation, err := setDesiredPublicationTx(ctx, tx, request.SystemID, request.RouteID, request.Fingerprint)
	if err != nil {
		return 0, CoordinatorJob{}, false, err
	}
	generationJSON, _ := json.Marshal(generation)
	fields["generation"] = generationJSON
	payload, err := json.Marshal(fields)
	if err != nil || !validCoordinatorJSON(payload) {
		return 0, CoordinatorJob{}, false, ErrCoordinatorInvalid
	}
	// Superseding a desired input makes queued and running work for an older
	// generation useless. Clearing its lease also causes an active handler's
	// next heartbeat/activation to fail closed instead of spending a full retry
	// window on work that can no longer become current.
	if _, err := tx.Exec(ctx, `UPDATE core.coordinator_jobs SET state='cancelled',lease_owner=NULL,lease_until=NULL,
		error_code='publication_superseded',updated_at=clock_timestamp()
		WHERE system_id=$1 AND job_type=$2 AND state IN ('queued','running')
		AND payload->>'route_id'=$3 AND payload->>'generation' IS DISTINCT FROM $4::text`,
		request.SystemID, request.JobType, request.RouteID.String(), strconv.FormatInt(generation, 10)); err != nil {
		return 0, CoordinatorJob{}, false, err
	}
	publicationKey := coordinatorPublicationJobKey(request, generation)
	command, err := tx.Exec(ctx, `INSERT INTO core.coordinator_jobs(job_id,system_id,job_type,idempotency_key,payload,state,max_attempts)
		VALUES($1,$2,$3,$4,$5::jsonb,'queued',$6) ON CONFLICT(system_id,job_type,idempotency_key) DO NOTHING`,
		request.JobID, request.SystemID, request.JobType, publicationKey, payload, request.MaxAttempts)
	if err != nil {
		return 0, CoordinatorJob{}, false, err
	}
	created := command.RowsAffected() == 1
	job, err := scanCoordinatorJob(tx.QueryRow(ctx, `SELECT job_id,system_id,job_type,idempotency_key,payload,state,
		attempts,max_attempts,available_at,lease_owner,lease_until,fencing_token,claim_epoch,result,error_code,created_at,updated_at
		FROM core.coordinator_jobs WHERE system_id=$1 AND job_type=$2 AND idempotency_key=$3`,
		request.SystemID, request.JobType, publicationKey))
	if err != nil {
		return 0, CoordinatorJob{}, false, err
	}
	if !created {
		var matches bool
		if err := tx.QueryRow(ctx, `SELECT payload=$4::jsonb AND max_attempts=$5 FROM core.coordinator_jobs
			WHERE system_id=$1 AND job_type=$2 AND idempotency_key=$3`, request.SystemID, request.JobType,
			publicationKey, payload, request.MaxAttempts).Scan(&matches); err != nil {
			return 0, CoordinatorJob{}, false, err
		}
		if !matches {
			return 0, CoordinatorJob{}, false, ErrCoordinatorConflict
		}
	}
	if err := verifyCoordinatorInstance(ctx, tx, request.SystemID, request.Owner, request.ConfigEpoch); err != nil {
		return 0, CoordinatorJob{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, CoordinatorJob{}, false, err
	}
	return generation, job, created, nil
}

func CreateCoordinatorRouteTarget(ctx context.Context, pool *pgxpool.Pool, systemID string, routeID, fileID uuid.UUID) (CoordinatorRouteTarget, error) {
	if ctx == nil || pool == nil || !coordinatorSystemPattern.MatchString(systemID) || routeID == uuid.Nil || fileID == uuid.Nil {
		return CoordinatorRouteTarget{}, ErrCoordinatorInvalid
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return CoordinatorRouteTarget{}, err
	}
	defer tx.Rollback(ctx)
	if err := verifyRouteTargetFile(ctx, tx, fileID); err != nil {
		return CoordinatorRouteTarget{}, err
	}
	var target CoordinatorRouteTarget
	err = tx.QueryRow(ctx, `INSERT INTO core.coordinator_route_targets(system_id,route_id,file_id,revision)
		VALUES($1,$2,$3,1) RETURNING system_id,route_id,file_id,revision,publication_generation,updated_at`,
		systemID, routeID, fileID).Scan(&target.SystemID, &target.RouteID, &target.FileID, &target.Revision, &target.PublicationGeneration, &target.UpdatedAt)
	if err != nil {
		return CoordinatorRouteTarget{}, err
	}
	if err := notifyRouteTarget(ctx, tx, systemID, routeID); err != nil {
		return CoordinatorRouteTarget{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return CoordinatorRouteTarget{}, err
	}
	return target, nil
}

func GetCoordinatorRouteTarget(ctx context.Context, pool *pgxpool.Pool, systemID string, routeID uuid.UUID) (CoordinatorRouteTarget, error) {
	if ctx == nil || pool == nil || !coordinatorSystemPattern.MatchString(systemID) || routeID == uuid.Nil {
		return CoordinatorRouteTarget{}, ErrCoordinatorInvalid
	}
	return getCoordinatorRouteTarget(ctx, pool, systemID, routeID)
}

func ReplaceCoordinatorRouteTarget(ctx context.Context, pool *pgxpool.Pool, systemID string, routeID uuid.UUID, expectedRevision int64, fileID uuid.UUID) (CoordinatorRouteTarget, error) {
	if ctx == nil || pool == nil || !coordinatorSystemPattern.MatchString(systemID) || routeID == uuid.Nil || expectedRevision <= 0 || fileID == uuid.Nil {
		return CoordinatorRouteTarget{}, ErrCoordinatorInvalid
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return CoordinatorRouteTarget{}, err
	}
	defer tx.Rollback(ctx)
	manualFingerprint := manualRouteTargetFingerprint(systemID, routeID, expectedRevision+1, fileID)
	var generation int64
	err = tx.QueryRow(ctx, `INSERT INTO core.coordinator_publications(system_id,route_id,input_fingerprint,generation)
		VALUES($1,$2,$3,1) ON CONFLICT(system_id,route_id) DO UPDATE SET input_fingerprint=EXCLUDED.input_fingerprint,
		generation=core.coordinator_publications.generation+1,updated_at=clock_timestamp()
		RETURNING generation`, systemID, routeID, manualFingerprint).Scan(&generation)
	if err != nil {
		return CoordinatorRouteTarget{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE core.coordinator_jobs SET state='cancelled',lease_owner=NULL,lease_until=NULL,
		error_code='publication_superseded',updated_at=clock_timestamp()
		WHERE system_id=$1 AND job_type=$2 AND state IN ('queued','running')
		AND payload->>'route_id'=$3 AND payload->>'generation' IS DISTINCT FROM $4::text`,
		systemID, coordinatorPublicationJobType, routeID.String(), strconv.FormatInt(generation, 10)); err != nil {
		return CoordinatorRouteTarget{}, err
	}
	if err := verifyRouteTargetFile(ctx, tx, fileID); err != nil {
		return CoordinatorRouteTarget{}, err
	}
	var target CoordinatorRouteTarget
	err = tx.QueryRow(ctx, `UPDATE core.coordinator_route_targets SET file_id=$4,revision=revision+1,
		publication_generation=0,updated_at=clock_timestamp() WHERE system_id=$1 AND route_id=$2 AND revision=$3
		RETURNING system_id,route_id,file_id,revision,publication_generation,updated_at`, systemID, routeID, expectedRevision, fileID).
		Scan(&target.SystemID, &target.RouteID, &target.FileID, &target.Revision, &target.PublicationGeneration, &target.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return CoordinatorRouteTarget{}, ErrCoordinatorConflict
	}
	if err != nil {
		return CoordinatorRouteTarget{}, err
	}
	if err := notifyRouteTarget(ctx, tx, systemID, routeID); err != nil {
		return CoordinatorRouteTarget{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return CoordinatorRouteTarget{}, err
	}
	return target, nil
}

func coordinatorPublicationJobKey(request CoordinatorPublicationRequest, generation int64) string {
	material := request.SystemID + "\x00" + request.JobType + "\x00" + request.IdempotencyKey + "\x00" + strconv.FormatInt(generation, 10)
	digest := sha256.Sum256([]byte(material))
	return hex.EncodeToString(digest[:])
}

func manualRouteTargetFingerprint(systemID string, routeID uuid.UUID, revision int64, fileID uuid.UUID) string {
	material := "manual-route-target/v1\x00" + systemID + "\x00" + routeID.String() + "\x00" + strconv.FormatInt(revision, 10) + "\x00" + fileID.String()
	digest := sha256.Sum256([]byte(material))
	return hex.EncodeToString(digest[:])
}

// ActivateCoordinatorPublication compares the desired input generation and
// updates the active route target in the same database transaction. Repeating
// a committed request after losing its response is idempotent.
func ActivateCoordinatorPublication(ctx context.Context, pool *pgxpool.Pool, systemID string, routeID, jobID, owner uuid.UUID, epoch, fencingToken, generation int64, fileID uuid.UUID) (CoordinatorRouteTarget, error) {
	if ctx == nil || pool == nil || !coordinatorSystemPattern.MatchString(systemID) || routeID == uuid.Nil || jobID == uuid.Nil || owner == uuid.Nil || epoch <= 0 || fencingToken <= 0 || generation <= 0 || fileID == uuid.Nil {
		return CoordinatorRouteTarget{}, ErrCoordinatorInvalid
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return CoordinatorRouteTarget{}, err
	}
	defer tx.Rollback(ctx)
	if err := verifyCoordinatorInstance(ctx, tx, systemID, owner, epoch); err != nil {
		return CoordinatorRouteTarget{}, err
	}
	var desired int64
	err = tx.QueryRow(ctx, `SELECT generation FROM core.coordinator_publications WHERE system_id=$1 AND route_id=$2 FOR UPDATE`, systemID, routeID).Scan(&desired)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && desired != generation) {
		return CoordinatorRouteTarget{}, ErrCoordinatorStale
	}
	if err != nil {
		return CoordinatorRouteTarget{}, err
	}
	if err := verifyCoordinatorJobLease(ctx, tx, systemID, jobID, owner, epoch, fencingToken); err != nil {
		return CoordinatorRouteTarget{}, err
	}
	if err := verifyRouteTargetFile(ctx, tx, fileID); err != nil {
		return CoordinatorRouteTarget{}, err
	}
	target, err := getCoordinatorRouteTargetTx(ctx, tx, systemID, routeID)
	if errors.Is(err, ErrRouteTargetMissing) {
		return CoordinatorRouteTarget{}, ErrRouteTargetMissing
	}
	if err != nil {
		return CoordinatorRouteTarget{}, err
	}
	if target.PublicationGeneration > generation {
		return CoordinatorRouteTarget{}, ErrCoordinatorStale
	}
	if target.PublicationGeneration == generation && target.FileID == fileID {
		if err := verifyCoordinatorJobLease(ctx, tx, systemID, jobID, owner, epoch, fencingToken); err != nil {
			return CoordinatorRouteTarget{}, err
		}
		if err := verifyCoordinatorInstance(ctx, tx, systemID, owner, epoch); err != nil {
			return CoordinatorRouteTarget{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return CoordinatorRouteTarget{}, err
		}
		return target, nil
	}
	err = tx.QueryRow(ctx, `UPDATE core.coordinator_route_targets AS target SET file_id=$3,revision=revision+1,
		publication_generation=$4,updated_at=clock_timestamp() WHERE target.system_id=$1 AND target.route_id=$2
		AND EXISTS(SELECT 1 FROM core.coordinator_jobs AS job WHERE job.system_id=$1 AND job.job_id=$5
			AND job.state='running' AND job.lease_owner=$6 AND job.claim_epoch=$7 AND job.fencing_token=$8
			AND job.lease_until>clock_timestamp() AND EXISTS(SELECT 1 FROM core.coordinator_instances AS instance
				JOIN core.coordinator_config AS config ON config.system_id=instance.system_id AND config.epoch=instance.config_epoch
				WHERE instance.system_id=$1 AND instance.instance_id=$6 AND instance.config_epoch=$7
				AND instance.lease_until>clock_timestamp()))
		RETURNING target.system_id,target.route_id,target.file_id,target.revision,target.publication_generation,target.updated_at`,
		systemID, routeID, fileID, generation, jobID, owner, epoch, fencingToken).
		Scan(&target.SystemID, &target.RouteID, &target.FileID, &target.Revision, &target.PublicationGeneration, &target.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return CoordinatorRouteTarget{}, ErrCoordinatorJobNotOwned
	}
	if err != nil {
		return CoordinatorRouteTarget{}, err
	}
	if err := notifyRouteTarget(ctx, tx, systemID, routeID); err != nil {
		return CoordinatorRouteTarget{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return CoordinatorRouteTarget{}, err
	}
	return target, nil
}

func verifyRouteTargetFile(ctx context.Context, tx pgx.Tx, fileID uuid.UUID) error {
	var ready bool
	err := tx.QueryRow(ctx, `SELECT state='ready' FROM core.core_objects WHERE id=$1 FOR SHARE`, fileID).Scan(&ready)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrRouteTargetFileUnavailable
	}
	if err != nil {
		return err
	}
	if !ready {
		return ErrRouteTargetFileUnavailable
	}
	return nil
}

func verifyCoordinatorJobLease(ctx context.Context, tx pgx.Tx, systemID string, jobID, owner uuid.UUID, epoch, fencingToken int64) error {
	var currentToken int64
	var leaseUntil time.Time
	err := tx.QueryRow(ctx, `SELECT fencing_token,lease_until FROM core.coordinator_jobs
		WHERE system_id=$1 AND job_id=$2 AND state='running' AND lease_owner=$3 AND claim_epoch=$4
		FOR UPDATE`, systemID, jobID, owner, epoch).Scan(&currentToken, &leaseUntil)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrCoordinatorJobNotOwned
	}
	if err != nil {
		return err
	}
	var active bool
	if err := tx.QueryRow(ctx, `SELECT $1::bigint=$2::bigint AND $3::timestamptz>clock_timestamp()`, currentToken, fencingToken, leaseUntil).Scan(&active); err != nil {
		return err
	}
	if !active {
		return ErrCoordinatorJobNotOwned
	}
	return nil
}

func getCoordinatorRouteTarget(ctx context.Context, pool *pgxpool.Pool, systemID string, routeID uuid.UUID) (CoordinatorRouteTarget, error) {
	return scanCoordinatorRouteTarget(pool.QueryRow(ctx, `SELECT system_id,route_id,file_id,revision,publication_generation,updated_at
		FROM core.coordinator_route_targets WHERE system_id=$1 AND route_id=$2`, systemID, routeID))
}

func getCoordinatorRouteTargetTx(ctx context.Context, tx pgx.Tx, systemID string, routeID uuid.UUID) (CoordinatorRouteTarget, error) {
	return scanCoordinatorRouteTarget(tx.QueryRow(ctx, `SELECT system_id,route_id,file_id,revision,publication_generation,updated_at
		FROM core.coordinator_route_targets WHERE system_id=$1 AND route_id=$2 FOR UPDATE`, systemID, routeID))
}

func scanCoordinatorRouteTarget(row rowScanner) (CoordinatorRouteTarget, error) {
	var target CoordinatorRouteTarget
	err := row.Scan(&target.SystemID, &target.RouteID, &target.FileID, &target.Revision, &target.PublicationGeneration, &target.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return CoordinatorRouteTarget{}, ErrRouteTargetMissing
	}
	return target, err
}

func notifyRouteTarget(ctx context.Context, tx pgx.Tx, systemID string, routeID uuid.UUID) error {
	payload := systemID + ":" + routeID.String()
	if len(payload) > 7900 || strings.ContainsRune(payload, '\x00') {
		return ErrCoordinatorInvalid
	}
	if _, err := tx.Exec(ctx, `SELECT pg_notify('core_route_target_invalidation',$1)`, payload); err != nil {
		return fmt.Errorf("notify Router target update: %w", err)
	}
	return nil
}
