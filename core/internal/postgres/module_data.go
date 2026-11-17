package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrModuleDataNotFound  = errors.New("module data not found")
	ErrModuleDataConflict  = errors.New("module data revision conflict")
	ErrModuleDataInvalid   = errors.New("invalid module data operation")
	ErrModuleDataLimit     = errors.New("module data limit exceeded")
	moduleIDPattern        = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
	moduleNamespacePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}(/[a-z][a-z0-9_-]{0,63})*$`)
	moduleScopePattern     = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
)

type ModuleDataKey struct {
	Namespace string
	ScopeType string
	ScopeID   string
	RecordKey string
}

type ModuleDataValue struct {
	Payload        json.RawMessage
	PayloadVersion int
	IndexData      json.RawMessage
	TTL            time.Duration
}

type ModuleDataRecord struct {
	Payload        json.RawMessage
	PayloadVersion int
	IndexData      json.RawMessage
	Revision       int64
	ExpiresAt      *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type ModuleDataMutation struct {
	Key              ModuleDataKey
	ExpectedRevision int64
	Value            *ModuleDataValue
}

// Each selector is an AND term containing one or more OR alternatives. Values
// are prevalidated, serialized JSON object fragments; no field is interpolated
// into SQL.
type ModuleDataSelector struct {
	Alternatives []json.RawMessage
}

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

// ValidateModuleDataJSONBObject validates a bounded JSON object using the
// same normalization and PostgreSQL numeric limits as persisted payloads.
func ValidateModuleDataJSONBObject(encoded []byte, maxBytes int) (int, error) {
	if maxBytes <= 0 {
		return 0, ErrModuleDataInvalid
	}
	if len(encoded) > maxBytes {
		return 0, ErrModuleDataLimit
	}
	if !utf8.Valid(encoded) || !json.Valid(encoded) || !validJSONBUnicode(encoded) {
		return 0, ErrModuleDataInvalid
	}
	normalizedBytes, ok := estimateJSONBTextBytes(encoded, maxBytes)
	if !ok {
		return 0, ErrModuleDataLimit
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return 0, ErrModuleDataInvalid
	}
	if _, ok := value.(map[string]any); !ok {
		return 0, ErrModuleDataInvalid
	}
	return normalizedBytes, nil
}

// estimateJSONBTextBytes bounds the text representation returned by PostgreSQL
// JSONB. JSONB adds separator spaces and expands numeric exponents to decimal
// notation; rejecting a document whose normalized form exceeds its configured
// limit keeps List responses bounded even for adversarial numeric literals.
func estimateJSONBTextBytes(encoded []byte, maxBytes int) (int, bool) {
	if maxBytes <= 0 || len(encoded) > maxBytes || !utf8.Valid(encoded) || !json.Valid(encoded) || !validJSONBUnicode(encoded) {
		return 0, false
	}
	total := int64(len(encoded))
	inString, escaped := false, false
	for i := 0; i < len(encoded); {
		char := encoded[i]
		if inString {
			if escaped {
				escaped = false
			} else if char == '\\' {
				escaped = true
			} else if char == '"' {
				inString = false
			}
			i++
			continue
		}
		if char == '"' {
			inString = true
			i++
			continue
		}
		if char == ',' || char == ':' {
			total++
			i++
			if total > int64(maxBytes) {
				return 0, false
			}
			continue
		}
		if char == '-' || char >= '0' && char <= '9' {
			end := i + 1
			for end < len(encoded) && isJSONNumberByte(encoded[end]) {
				end++
			}
			numberBytes, ok := jsonbNumberTextBytes(encoded[i:end])
			if !ok {
				return 0, false
			}
			if numberBytes > end-i {
				total += int64(numberBytes - (end - i))
				if total > int64(maxBytes) {
					return 0, false
				}
			}
			i = end
			continue
		}
		i++
	}
	return int(total), total <= int64(maxBytes)
}

func validJSONBUnicode(encoded []byte) bool {
	inString := false
	for i := 0; i < len(encoded); i++ {
		if !inString {
			if encoded[i] == '"' {
				inString = true
			}
			continue
		}
		switch encoded[i] {
		case '"':
			inString = false
		case '\\':
			if i+1 >= len(encoded) {
				return false
			}
			if encoded[i+1] != 'u' {
				i++
				continue
			}
			code, next, ok := jsonUnicodeEscape(encoded, i)
			if !ok || code == 0 || code >= 0xDC00 && code <= 0xDFFF {
				return false
			}
			if code >= 0xD800 && code <= 0xDBFF {
				low, afterLow, valid := jsonUnicodeEscape(encoded, next)
				if !valid || low < 0xDC00 || low > 0xDFFF {
					return false
				}
				i = afterLow - 1
			} else {
				i = next - 1
			}
		}
	}
	return !inString
}

func jsonUnicodeEscape(encoded []byte, start int) (uint16, int, bool) {
	if start < 0 || start+6 > len(encoded) || encoded[start] != '\\' || encoded[start+1] != 'u' {
		return 0, 0, false
	}
	var code uint16
	for _, digit := range encoded[start+2 : start+6] {
		code <<= 4
		switch {
		case digit >= '0' && digit <= '9':
			code |= uint16(digit - '0')
		case digit >= 'a' && digit <= 'f':
			code |= uint16(digit-'a') + 10
		case digit >= 'A' && digit <= 'F':
			code |= uint16(digit-'A') + 10
		default:
			return 0, 0, false
		}
	}
	return code, start + 6, true
}

func isJSONNumberByte(value byte) bool {
	return value == '-' || value == '+' || value == '.' || value == 'e' || value == 'E' || value >= '0' && value <= '9'
}

func jsonbNumberTextBytes(value []byte) (int, bool) {
	if len(value) == 0 {
		return 0, false
	}
	sign := int64(0)
	if value[0] == '-' {
		sign = 1
		value = value[1:]
	}
	exponent := int64(0)
	if index := bytes.IndexAny(value, "eE"); index >= 0 {
		exponentText := value[index+1:]
		value = value[:index]
		if len(exponentText) == 0 {
			return 0, false
		}
		exponentSign := int64(1)
		if exponentText[0] == '+' || exponentText[0] == '-' {
			if exponentText[0] == '-' {
				exponentSign = -1
			}
			exponentText = exponentText[1:]
			if len(exponentText) == 0 {
				return 0, false
			}
		}
		firstDigit := 0
		for firstDigit < len(exponentText) && exponentText[firstDigit] == '0' {
			firstDigit++
		}
		exponentText = exponentText[firstDigit:]
		if len(exponentText) > 9 {
			return 0, false
		}
		for _, digit := range exponentText {
			exponent = exponent*10 + int64(digit-'0')
		}
		exponent *= exponentSign
	}
	integerDigits, fractionDigits := value, []byte(nil)
	if dot := bytes.IndexByte(value, '.'); dot >= 0 {
		integerDigits, fractionDigits = value[:dot], value[dot+1:]
	}
	leadingZeros, hasCoefficient := 0, false
	for i := 0; i < len(integerDigits)+len(fractionDigits); i++ {
		digit := byte(0)
		if i < len(integerDigits) {
			digit = integerDigits[i]
		} else {
			digit = fractionDigits[i-len(integerDigits)]
		}
		if !hasCoefficient && digit == '0' {
			leadingZeros++
			continue
		}
		hasCoefficient = true
	}
	coefficientBytes := len(integerDigits) + len(fractionDigits) - leadingZeros
	if !hasCoefficient {
		scale := int64(len(fractionDigits)) - exponent
		if scale <= 0 {
			return int(sign + 1), true
		}
		if scale > 16383 {
			return 0, false
		}
		return int(sign + 2 + scale), true
	}
	// Leading fractional zeros move the decimal point left relative to the
	// significant coefficient. For example, 0.001e2 normalizes to 0.1; using
	// only the raw integer-part width would incorrectly estimate it as 100.0.
	point := int64(len(integerDigits)) + exponent - int64(leadingZeros)
	scale := int64(len(fractionDigits)) - exponent
	if scale < 0 {
		scale = 0
	}
	if point > 131072 || scale > 16383 {
		return 0, false
	}
	length := sign
	if point > 0 {
		length += point
		if scale > 0 {
			length += 1 + scale
		}
	} else {
		length += 2 - point + int64(coefficientBytes)
	}
	if length > int64(^uint(0)>>1) {
		return 0, false
	}
	return int(length), true
}

func GetModuleData(ctx context.Context, pool *pgxpool.Pool, moduleID string, key ModuleDataKey) (ModuleDataRecord, error) {
	if pool == nil || !validModuleDataKey(moduleID, key) {
		return ModuleDataRecord{}, errors.New("invalid module data key")
	}
	var record ModuleDataRecord
	err := pool.QueryRow(ctx, `SELECT payload,payload_version,index_data,revision,expires_at,created_at,updated_at
		FROM core.module_data WHERE module_id=$1 AND namespace=$2 AND scope_type=$3 AND scope_id=$4 AND record_key=$5
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
		FROM core.module_data WHERE module_id=$1 AND namespace=$2 AND scope_type=$3 AND scope_id=$4 AND record_key=$5
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
		err = db.QueryRow(ctx, `INSERT INTO core.module_data(module_id,namespace,scope_type,scope_id,record_key,payload,payload_version,index_data,revision,expires_at)
			VALUES($1,$2,$3,$4,$5,$6::jsonb,$7,$8::jsonb,nextval('core.module_data_revision_seq'),
				CASE WHEN $9::bigint=0 THEN NULL ELSE now()+make_interval(secs => $9::double precision/1000000000.0) END)
			ON CONFLICT(module_id,namespace,scope_type,scope_id,record_key) DO UPDATE SET
			payload=EXCLUDED.payload,payload_version=EXCLUDED.payload_version,index_data=EXCLUDED.index_data,
			revision=nextval('core.module_data_revision_seq'),expires_at=EXCLUDED.expires_at,updated_at=now(),created_at=now()
			WHERE core.module_data.expires_at IS NOT NULL AND core.module_data.expires_at<=now()
			RETURNING payload,payload_version,index_data,revision,expires_at,created_at,updated_at`, moduleID, key.Namespace, key.ScopeType,
			key.ScopeID, key.RecordKey, []byte(value.Payload), value.PayloadVersion, []byte(value.IndexData), int64(value.TTL)).
			Scan(&written.Payload, &written.PayloadVersion, &written.IndexData, &written.Revision, &written.ExpiresAt, &written.CreatedAt, &written.UpdatedAt)
	} else {
		err = db.QueryRow(ctx, `UPDATE core.module_data SET payload=$6::jsonb,payload_version=$7,index_data=$8::jsonb,
			revision=nextval('core.module_data_revision_seq'),expires_at=CASE WHEN $9::bigint=0 THEN NULL
				ELSE now()+make_interval(secs => $9::double precision/1000000000.0) END,updated_at=now()
			WHERE module_id=$1 AND namespace=$2 AND scope_type=$3 AND scope_id=$4 AND record_key=$5 AND revision=$10
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
	tag, err := pool.Exec(ctx, `DELETE FROM core.module_data WHERE module_id=$1 AND namespace=$2 AND scope_type=$3 AND scope_id=$4 AND record_key=$5 AND revision=$6`,
		moduleID, key.Namespace, key.ScopeType, key.ScopeID, key.RecordKey, expectedRevision)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM core.module_data WHERE module_id=$1 AND namespace=$2 AND scope_type=$3 AND scope_id=$4 AND record_key=$5)`,
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
			tag, err := tx.Exec(ctx, `DELETE FROM core.module_data WHERE module_id=$1 AND namespace=$2 AND scope_type=$3 AND scope_id=$4 AND record_key=$5 AND revision=$6`,
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
	query := `SELECT record_key,payload,payload_version,index_data,revision,expires_at,created_at,updated_at FROM core.module_data
		WHERE module_id=$1 AND namespace=$2 AND scope_type=$3 AND scope_id=$4 AND record_key>$5`
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
