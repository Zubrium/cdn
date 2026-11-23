package retirement

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Zubrium/cdn/pkg/contracts"
	"github.com/google/uuid"
)

const dataVersion = 1

type routeRecord struct {
	RouteID   string `json:"route_id"`
	State     string `json:"state"`
	Revision  int64  `json:"revision,omitempty"`
	ErrorCode string `json:"error_code,omitempty"`
}

type jobRecord struct {
	Kind           string        `json:"kind"`
	ID             string        `json:"id"`
	PageID         string        `json:"page_id"`
	BranchID       string        `json:"branch_id"`
	IdempotencyKey string        `json:"idempotency_key"`
	State          string        `json:"state"`
	Routes         []routeRecord `json:"routes"`
	CreatedAt      string        `json:"created_at"`
	UpdatedAt      string        `json:"updated_at"`
}

type receiptRecord struct {
	Kind   string `json:"kind"`
	PageID string `json:"page_id"`
	JobID  string `json:"job_id"`
}

type tombstoneRecord struct {
	Kind   string `json:"kind"`
	PageID string `json:"page_id"`
	JobID  string `json:"job_id"`
}

func jobKey(jobID string) contracts.RecordKey {
	return contracts.RecordKey{Namespace: namespace, ScopeType: scopeType, ScopeID: "", RecordKey: "job/" + jobID}
}

func tombstoneKey(pageID string) contracts.RecordKey {
	return contracts.RecordKey{Namespace: namespace, ScopeType: scopeType, ScopeID: "", RecordKey: "page/" + pageID}
}

func idempotencyKey(pageID, key string) contracts.RecordKey {
	digest := sha256.Sum256([]byte(pageID + "\x00" + key))
	return contracts.RecordKey{Namespace: namespace, ScopeType: scopeType, ScopeID: "", RecordKey: "idem/" + hex.EncodeToString(digest[:])}
}

func withBranch(key contracts.RecordKey, branchID string) contracts.RecordKey {
	key.ScopeID = branchID
	return key
}

func encodeRecord(payload any, index map[string]any) (*contracts.RecordValue, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("%w: encode retirement record: %v", ErrInvalid, err)
	}
	indexed, err := json.Marshal(index)
	if err != nil {
		return nil, fmt.Errorf("%w: encode retirement index: %v", ErrInvalid, err)
	}
	return &contracts.RecordValue{Payload: encoded, PayloadVersion: dataVersion, IndexData: indexed}, nil
}

func jobIndex(job jobRecord) map[string]any {
	return map[string]any{"record_type": recordTypeJob, "page_id": job.PageID, "state": job.State}
}

func jobCreationMutations(branchID string, request contracts.RetirePageRequest, job jobRecord) ([]contracts.RecordMutation, error) {
	jobValue, err := encodeRecord(job, jobIndex(job))
	if err != nil {
		return nil, err
	}
	receiptValue, err := encodeRecord(receiptRecord{Kind: recordTypeReceipt, PageID: request.PageID, JobID: job.ID},
		map[string]any{"record_type": recordTypeReceipt, "page_id": request.PageID})
	if err != nil {
		return nil, err
	}
	tombstoneValue, err := encodeRecord(tombstoneRecord{Kind: recordTypeTombstone, PageID: request.PageID, JobID: job.ID},
		map[string]any{"record_type": recordTypeTombstone, "page_id": request.PageID})
	if err != nil {
		return nil, err
	}
	return []contracts.RecordMutation{
		{Key: withBranch(jobKey(job.ID), branchID), ExpectedRevision: 0, Value: jobValue},
		{Key: withBranch(idempotencyKey(request.PageID, request.IdempotencyKey), branchID), ExpectedRevision: 0, Value: receiptValue},
		{Key: withBranch(tombstoneKey(request.PageID), branchID), ExpectedRevision: 0, Value: tombstoneValue},
	}, nil
}

func decodeJob(record contracts.Record, jobID, pageID string) (jobRecord, error) {
	var job jobRecord
	var index struct {
		RecordType string `json:"record_type"`
		PageID     string `json:"page_id"`
		State      string `json:"state"`
	}
	if record.PayloadVersion != dataVersion || record.Revision < 1 || json.Unmarshal(record.Payload, &job) != nil ||
		json.Unmarshal(record.IndexData, &index) != nil || index.RecordType != recordTypeJob ||
		(pageID != "" && (index.PageID != pageID || job.PageID != pageID)) ||
		index.State != job.State || job.Kind != recordTypeJob ||
		job.ID != jobID || !uuidPattern.MatchString(job.ID) ||
		!uuidPattern.MatchString(job.PageID) || !idempotentKey.MatchString(job.IdempotencyKey) ||
		len(job.Routes) == 0 || len(job.Routes) > maxRoutes ||
		(job.State != stateRunning && job.State != stateSucceeded && job.State != stateRetryable) {
		return jobRecord{}, fmt.Errorf("%w: invalid retirement job record", ErrUnavailable)
	}
	seen := make(map[string]struct{}, len(job.Routes))
	for _, route := range job.Routes {
		if !uuidPattern.MatchString(route.RouteID) ||
			(route.State != routeStatePending && route.State != routeStateRemoved && route.State != routeStateRetryable) {
			return jobRecord{}, fmt.Errorf("%w: invalid retirement route record", ErrUnavailable)
		}
		if _, duplicate := seen[route.RouteID]; duplicate {
			return jobRecord{}, fmt.Errorf("%w: duplicate retirement route", ErrUnavailable)
		}
		seen[route.RouteID] = struct{}{}
	}
	return job, nil
}

func jsonUnmarshal(payload []byte, target any) error { return json.Unmarshal(payload, target) }

func newUUID() string { return uuid.NewString() }

func timestamp() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func parseIfMatch(value string) (int64, error) {
	trimmed := strings.Trim(value, `"`)
	revision, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil || revision < 1 || `"`+strconv.FormatInt(revision, 10)+`"` != value {
		return 0, fmt.Errorf("%w: invalid If-Match", ErrInvalid)
	}
	return revision, nil
}

func mapStoreError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, contracts.ErrRecordNotFound):
		return ErrNotFound
	case errors.Is(err, contracts.ErrRecordConflict):
		return ErrConflict
	case errors.Is(err, contracts.ErrRecordInvalid):
		return ErrInvalid
	default:
		return ErrUnavailable
	}
}
