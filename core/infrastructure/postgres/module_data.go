package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	appengine "github.com/Zubrium/cdn/core/application/engine_data"
	"github.com/Zubrium/cdn/core/domain/engine_data"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrModuleDataNotFound  = engine_data.ErrNotFound
	ErrModuleDataConflict  = engine_data.ErrConflict
	ErrModuleDataInvalid   = engine_data.ErrInvalid
	ErrModuleDataLimit     = engine_data.ErrLimit
	moduleIDPattern        = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
	moduleNamespacePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}(/[a-z][a-z0-9_-]{0,63})*$`)
	moduleScopePattern     = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
)

type ModuleDataKey = engine_data.Key
type ModuleDataValue = engine_data.Value
type ModuleDataRecord = engine_data.Record
type ModuleDataMutation = engine_data.Mutation
type ModuleDataSelector = engine_data.ContainmentSelector

func validModuleDataKey(moduleID string, key ModuleDataKey) bool {
	return moduleIDPattern.MatchString(moduleID) && validModuleNamespace(key.Namespace) &&
		moduleScopePattern.MatchString(key.ScopeType) && validDataText(key.ScopeID, 256) &&
		validDataText(key.RecordKey, 256)
}

func validModuleNamespace(value string) bool {
	return len(value) <= 255 && moduleNamespacePattern.MatchString(value)
}

func validDataText(value string, maxBytes int) bool {
	return len(value) > 0 && len(value) <= maxBytes && utf8.ValidString(value) && !strings.ContainsRune(value, '\x00')
}

func validateModuleDataValue(value ModuleDataValue, maxPayloadBytes, maxIndexBytes int) error {
	_, err := ModuleDataValueSize(value, maxPayloadBytes, maxIndexBytes)
	return err
}

// ModuleDataValueSize validates a record and returns its normalized JSONB byte
// size. Batch callers use the same estimate as page limits so compact numeric
// exponents cannot bypass aggregate request budgets.
func ModuleDataValueSize(value ModuleDataValue, maxPayloadBytes, maxIndexBytes int) (int, error) {
	if maxPayloadBytes <= 0 || maxIndexBytes <= 0 || value.PayloadVersion <= 0 ||
		value.PayloadVersion > 1<<31-1 || len(value.Payload) == 0 || value.TTL < 0 {
		return 0, ErrModuleDataInvalid
	}
	payloadBytes, err := ValidateModuleDataJSONBObject(value.Payload, maxPayloadBytes)
	if err != nil {
		return 0, err
	}
	indexData := value.IndexData
	if len(indexData) == 0 {
		indexData = json.RawMessage(`{}`)
	}
	indexBytes, err := ValidateModuleDataJSONBObject(indexData, maxIndexBytes)
	if err != nil {
		return 0, err
	}
	return payloadBytes + indexBytes, nil
}

// ValidateModuleDataJSONBObject preserves the infrastructure API while the
// deterministic JSONB contract lives in the engine-data domain package.
func ValidateModuleDataJSONBObject(encoded []byte, maxBytes int) (int, error) {
	return appengine.ValidateJSONBObject(encoded, maxBytes)
}

func GetModuleData(ctx context.Context, pool *pgxpool.Pool, moduleID string, key ModuleDataKey) (ModuleDataRecord, error) {
	if pool == nil || !validModuleDataKey(moduleID, key) {
		return ModuleDataRecord{}, errors.New("invalid module data key")
	}
	var record ModuleDataRecord
	err := pool.QueryRow(ctx, `SELECT payload,payload_version,index_data,revision,expires_at,created_at,updated_at
		FROM engine_data.records WHERE engine_id=$1 AND namespace=$2 AND scope_type=$3 AND scope_id=$4 AND record_key=$5
		AND (expires_at IS NULL OR expires_at>now())`, moduleID, key.Namespace, key.ScopeType, key.ScopeID, key.RecordKey).
		Scan(&record.Payload, &record.PayloadVersion, &record.IndexData, &record.Revision, &record.ExpiresAt, &record.CreatedAt, &record.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ModuleDataRecord{}, ErrModuleDataNotFound
	}
	return record, err
}

// GetExpiredModuleData is reserved for engine cleanup and recovery workflows.
// Normal reads must use GetModuleData so expired records remain invisible.
func GetExpiredModuleData(ctx context.Context, pool *pgxpool.Pool, moduleID string, key ModuleDataKey) (ModuleDataRecord, error) {
	if pool == nil || !validModuleDataKey(moduleID, key) {
		return ModuleDataRecord{}, errors.New("invalid module data key")
	}
	var record ModuleDataRecord
	err := pool.QueryRow(ctx, `SELECT payload,payload_version,index_data,revision,expires_at,created_at,updated_at
		FROM engine_data.records WHERE engine_id=$1 AND namespace=$2 AND scope_type=$3 AND scope_id=$4 AND record_key=$5
		AND expires_at IS NOT NULL AND expires_at<=now()`, moduleID, key.Namespace, key.ScopeType, key.ScopeID, key.RecordKey).
		Scan(&record.Payload, &record.PayloadVersion, &record.IndexData, &record.Revision, &record.ExpiresAt, &record.CreatedAt, &record.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ModuleDataRecord{}, ErrModuleDataNotFound
	}
	return record, err
}

func PutModuleData(ctx context.Context, pool *pgxpool.Pool, moduleID string, key ModuleDataKey, value ModuleDataValue, expectedRevision int64, maxPayloadBytes, maxIndexBytes int) (ModuleDataRecord, error) {
	if pool == nil || !validModuleDataKey(moduleID, key) || expectedRevision < 0 {
		return ModuleDataRecord{}, errors.New("invalid module data write")
	}
	if err := validateModuleDataValue(value, maxPayloadBytes, maxIndexBytes); err != nil {
		return ModuleDataRecord{}, err
	}
	if len(value.IndexData) == 0 {
		value.IndexData = json.RawMessage(`{}`)
	}
	return putModuleData(ctx, pool, moduleID, key, value, expectedRevision)
}

type moduleDataDB interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func putModuleData(ctx context.Context, db moduleDataDB, moduleID string, key ModuleDataKey, value ModuleDataValue, expectedRevision int64) (ModuleDataRecord, error) {
	var written ModuleDataRecord
	var err error
	if expectedRevision == 0 {
		err = db.QueryRow(ctx, `INSERT INTO engine_data.records(engine_id,namespace,scope_type,scope_id,record_key,payload,payload_version,index_data,revision,expires_at)
			VALUES($1,$2,$3,$4,$5,$6::jsonb,$7,$8::jsonb,nextval('engine_data.revision_seq'),
				CASE WHEN $9::bigint=0 THEN NULL ELSE now()+make_interval(secs => $9::double precision/1000000000.0) END)
			ON CONFLICT(engine_id,namespace,scope_type,scope_id,record_key) DO UPDATE SET
			payload=EXCLUDED.payload,payload_version=EXCLUDED.payload_version,index_data=EXCLUDED.index_data,
			revision=nextval('engine_data.revision_seq'),expires_at=EXCLUDED.expires_at,updated_at=now(),created_at=now()
			WHERE engine_data.records.expires_at IS NOT NULL AND engine_data.records.expires_at<=now()
			RETURNING payload,payload_version,index_data,revision,expires_at,created_at,updated_at`, moduleID, key.Namespace, key.ScopeType,
			key.ScopeID, key.RecordKey, []byte(value.Payload), value.PayloadVersion, []byte(value.IndexData), int64(value.TTL)).
			Scan(&written.Payload, &written.PayloadVersion, &written.IndexData, &written.Revision, &written.ExpiresAt, &written.CreatedAt, &written.UpdatedAt)
	} else {
		err = db.QueryRow(ctx, `UPDATE engine_data.records SET payload=$6::jsonb,payload_version=$7,index_data=$8::jsonb,
			revision=nextval('engine_data.revision_seq'),expires_at=CASE WHEN $9::bigint=0 THEN NULL
				ELSE now()+make_interval(secs => $9::double precision/1000000000.0) END,updated_at=now()
			WHERE engine_id=$1 AND namespace=$2 AND scope_type=$3 AND scope_id=$4 AND record_key=$5 AND revision=$10
				AND (expires_at IS NULL OR expires_at>now())
			RETURNING payload,payload_version,index_data,revision,expires_at,created_at,updated_at`, moduleID, key.Namespace, key.ScopeType,
			key.ScopeID, key.RecordKey, []byte(value.Payload), value.PayloadVersion, []byte(value.IndexData), int64(value.TTL), expectedRevision).
			Scan(&written.Payload, &written.PayloadVersion, &written.IndexData, &written.Revision, &written.ExpiresAt, &written.CreatedAt, &written.UpdatedAt)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ModuleDataRecord{}, ErrModuleDataConflict
	}
	return written, err
}

func DeleteModuleData(ctx context.Context, pool *pgxpool.Pool, moduleID string, key ModuleDataKey, expectedRevision int64) error {
	if pool == nil || !validModuleDataKey(moduleID, key) || expectedRevision <= 0 {
		return errors.New("invalid module data delete")
	}
	tag, err := pool.Exec(ctx, `DELETE FROM engine_data.records WHERE engine_id=$1 AND namespace=$2 AND scope_type=$3 AND scope_id=$4 AND record_key=$5 AND revision=$6`,
		moduleID, key.Namespace, key.ScopeType, key.ScopeID, key.RecordKey, expectedRevision)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM engine_data.records WHERE engine_id=$1 AND namespace=$2 AND scope_type=$3 AND scope_id=$4 AND record_key=$5)`,
		moduleID, key.Namespace, key.ScopeType, key.ScopeID, key.RecordKey).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrModuleDataNotFound
	}
	return ErrModuleDataConflict
}

func BatchModuleData(ctx context.Context, pool *pgxpool.Pool, moduleID string, mutations []ModuleDataMutation, maxBatch, maxBatchBytes, maxPayloadBytes, maxIndexBytes int) error {
	if pool == nil || !moduleIDPattern.MatchString(moduleID) || len(mutations) == 0 || maxBatch <= 0 || maxBatchBytes <= 0 || maxPayloadBytes <= 0 || maxIndexBytes <= 0 {
		return errors.New("invalid module data batch")
	}
	if len(mutations) > maxBatch {
		return ErrModuleDataLimit
	}
	first := mutations[0].Key
	seen := make(map[string]struct{}, len(mutations))
	for _, mutation := range mutations {
		if !validModuleDataKey(moduleID, mutation.Key) || mutation.ExpectedRevision < 0 ||
			mutation.Key.Namespace != first.Namespace || mutation.Key.ScopeType != first.ScopeType || mutation.Key.ScopeID != first.ScopeID {
			return errors.New("module data batch must use one valid scope")
		}
		if _, exists := seen[mutation.Key.RecordKey]; exists {
			return errors.New("module data batch contains duplicate keys")
		}
		seen[mutation.Key.RecordKey] = struct{}{}
		if mutation.Value == nil {
			if mutation.ExpectedRevision <= 0 {
				return errors.New("module data delete requires an expected revision")
			}
			continue
		}
	}
	if err := validateModuleDataBatchSize(mutations, maxBatch, maxBatchBytes, maxPayloadBytes, maxIndexBytes); err != nil {
		return err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	for _, mutation := range mutations {
		if mutation.Value == nil {
			tag, err := tx.Exec(ctx, `DELETE FROM engine_data.records WHERE engine_id=$1 AND namespace=$2 AND scope_type=$3 AND scope_id=$4 AND record_key=$5 AND revision=$6`,
				moduleID, mutation.Key.Namespace, mutation.Key.ScopeType, mutation.Key.ScopeID, mutation.Key.RecordKey, mutation.ExpectedRevision)
			if err != nil {
				return err
			}
			if tag.RowsAffected() != 1 {
				return ErrModuleDataConflict
			}
			continue
		}
		value := *mutation.Value
		if len(value.IndexData) == 0 {
			value.IndexData = json.RawMessage(`{}`)
		}
		if _, err := putModuleData(ctx, tx, moduleID, mutation.Key, value, mutation.ExpectedRevision); err != nil {
			return fmt.Errorf("module data batch CAS: %w", err)
		}
	}
	return tx.Commit(ctx)
}

func validateModuleDataBatchSize(mutations []ModuleDataMutation, maxBatch, maxBatchBytes, maxPayloadBytes, maxIndexBytes int) error {
	if maxBatch <= 0 || maxBatchBytes <= 0 || maxPayloadBytes <= 0 || maxIndexBytes <= 0 {
		return ErrModuleDataInvalid
	}
	if len(mutations) > maxBatch {
		return ErrModuleDataLimit
	}
	var total int64
	for _, mutation := range mutations {
		if mutation.Value == nil {
			continue
		}
		valueBytes, err := ModuleDataValueSize(*mutation.Value, maxPayloadBytes, maxIndexBytes)
		if err != nil {
			return err
		}
		if int64(valueBytes) > int64(maxBatchBytes)-total {
			return ErrModuleDataLimit
		}
		total += int64(valueBytes)
	}
	return nil
}

func ListModuleData(ctx context.Context, pool *pgxpool.Pool, moduleID, namespace, scopeType, scopeID, after string, selectors []ModuleDataSelector, limit int) ([]ModuleDataKey, []ModuleDataRecord, string, error) {
	return listModuleData(ctx, pool, moduleID, namespace, scopeType, scopeID, after, selectors, limit, false)
}

func ListExpiredModuleData(ctx context.Context, pool *pgxpool.Pool, moduleID, namespace, scopeType, scopeID, after string, limit int) ([]ModuleDataKey, []ModuleDataRecord, string, error) {
	return listModuleData(ctx, pool, moduleID, namespace, scopeType, scopeID, after, nil, limit, true)
}

func listModuleData(ctx context.Context, pool *pgxpool.Pool, moduleID, namespace, scopeType, scopeID, after string, selectors []ModuleDataSelector, limit int, expiredOnly bool) ([]ModuleDataKey, []ModuleDataRecord, string, error) {
	key := ModuleDataKey{Namespace: namespace, ScopeType: scopeType, ScopeID: scopeID, RecordKey: "_"}
	if pool == nil || !validModuleDataKey(moduleID, key) || (after != "" && !validDataText(after, 256)) || limit < 1 || limit > 1000 {
		return nil, nil, "", errors.New("invalid module data list")
	}
	query := `SELECT record_key,payload,payload_version,index_data,revision,expires_at,created_at,updated_at FROM engine_data.records
		WHERE engine_id=$1 AND namespace=$2 AND scope_type=$3 AND scope_id=$4 AND record_key>$5`
	args := []any{moduleID, namespace, scopeType, scopeID, after}
	if expiredOnly {
		query += ` AND expires_at IS NOT NULL AND expires_at<=now()`
	} else {
		query += ` AND (expires_at IS NULL OR expires_at>now())`
		for _, selector := range selectors {
			if len(selector.Alternatives) == 0 {
				return nil, nil, "", errors.New("empty module data selector")
			}
			query += ` AND (`
			for i, alternative := range selector.Alternatives {
				if !json.Valid(alternative) {
					return nil, nil, "", errors.New("invalid module data selector")
				}
				if i > 0 {
					query += ` OR `
				}
				args = append(args, string(alternative))
				query += fmt.Sprintf(`index_data @> $%d::jsonb`, len(args))
			}
			query += `)`
		}
	}
	args = append(args, limit+1)
	query += fmt.Sprintf(` ORDER BY record_key ASC LIMIT $%d`, len(args))
	rows, err := pool.Query(ctx, query, args...)
	if err != nil {
		return nil, nil, "", err
	}
	defer rows.Close()
	keys := make([]ModuleDataKey, 0, limit+1)
	records := make([]ModuleDataRecord, 0, limit+1)
	for rows.Next() {
		var itemKey ModuleDataKey
		itemKey.Namespace, itemKey.ScopeType, itemKey.ScopeID = namespace, scopeType, scopeID
		var record ModuleDataRecord
		if err := rows.Scan(&itemKey.RecordKey, &record.Payload, &record.PayloadVersion, &record.IndexData, &record.Revision,
			&record.ExpiresAt, &record.CreatedAt, &record.UpdatedAt); err != nil {
			return nil, nil, "", err
		}
		keys, records = append(keys, itemKey), append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, "", err
	}
	if len(keys) > limit {
		keys, records = keys[:limit], records[:limit]
		return keys, records, keys[len(keys)-1].RecordKey, nil
	}
	return keys, records, "", nil
}
