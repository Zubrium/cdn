// Package models contains persistence types and database-specific column
// types. The clean init migration is the canonical SQL schema for new
// installations and is also consumed by the Atlas schema loader.
package models

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// UUID is a PostgreSQL uuid column.
type UUID uuid.UUID

// Value implements driver.Valuer.
func (u UUID) Value() (driver.Value, error) {
	return uuid.UUID(u).String(), nil
}

// Scan implements sql.Scanner.
func (u *UUID) Scan(src any) error {
	switch v := src.(type) {
	case string:
		id, err := uuid.Parse(v)
		if err != nil {
			return err
		}
		*u = UUID(id)
	case []byte:
		id, err := uuid.Parse(string(v))
		if err != nil {
			return err
		}
		*u = UUID(id)
	case nil:
		*u = UUID{}
	default:
		return fmt.Errorf("cannot scan %T into UUID", src)
	}
	return nil
}

// GormDataType tells GORM to use the uuid column type.
func (UUID) GormDataType() string { return "uuid" }

// String returns the canonical text form of the identifier.
func (u UUID) String() string { return uuid.UUID(u).String() }

// MarshalJSON renders the identifier as a JSON string.
func (u UUID) MarshalJSON() ([]byte, error) { return json.Marshal(u.String()) }

// UnmarshalJSON parses a JSON string into the identifier.
func (u *UUID) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	id, err := uuid.Parse(s)
	if err != nil {
		return err
	}
	*u = UUID(id)
	return nil
}

// JSONB is a PostgreSQL jsonb column that keeps the document verbatim.
type JSONB []byte

// Value implements driver.Valuer.
func (j JSONB) Value() (driver.Value, error) {
	if len(j) == 0 {
		return nil, nil
	}
	return string(j), nil
}

// Scan implements sql.Scanner.
func (j *JSONB) Scan(src any) error {
	switch v := src.(type) {
	case string:
		*j = JSONB(v)
	case []byte:
		*j = JSONB(append([]byte(nil), v...))
	case nil:
		*j = nil
	default:
		return fmt.Errorf("cannot scan %T into JSONB", src)
	}
	return nil
}

// GormDataType tells GORM to use the jsonb column type.
func (JSONB) GormDataType() string { return "jsonb" }

// MarshalJSON renders the stored document as raw JSON.
func (j JSONB) MarshalJSON() ([]byte, error) {
	if len(j) == 0 {
		return []byte("null"), nil
	}
	return j, nil
}

// UnmarshalJSON stores the raw document.
func (j *JSONB) UnmarshalJSON(data []byte) error {
	*j = append((*j)[:0], data...)
	return nil
}

// JSONMap is a jsonb column holding a flat object of strings.
type JSONMap map[string]string

// Value implements driver.Valuer.
func (m JSONMap) Value() (driver.Value, error) {
	if m == nil {
		return nil, nil
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	return string(raw), nil
}

// Scan implements sql.Scanner.
func (m *JSONMap) Scan(src any) error {
	switch v := src.(type) {
	case string:
		return json.Unmarshal([]byte(v), m)
	case []byte:
		return json.Unmarshal(v, m)
	case nil:
		*m = nil
		return nil
	default:
		return fmt.Errorf("cannot scan %T into JSONMap", src)
	}
}

// GormDataType tells GORM to use the jsonb column type.
func (JSONMap) GormDataType() string { return "jsonb" }

// TextArray is a PostgreSQL text[] column.
type TextArray []string

// Value implements driver.Valuer. The array is written in the PostgreSQL
// literal form, which the driver sends as an untyped parameter.
func (a TextArray) Value() (driver.Value, error) {
	if a == nil {
		return nil, nil
	}
	parts := make([]string, 0, len(a))
	for _, v := range a {
		parts = append(parts, `"`+strings.ReplaceAll(v, `"`, `\"`)+`"`)
	}
	return "{" + strings.Join(parts, ",") + "}", nil
}

// Scan implements sql.Scanner.
func (a *TextArray) Scan(src any) error {
	switch v := src.(type) {
	case string:
		*a = parseTextArray(v)
	case []byte:
		*a = parseTextArray(string(v))
	case nil:
		*a = nil
	default:
		return fmt.Errorf("cannot scan %T into TextArray", src)
	}
	return nil
}

// GormDataType tells GORM to use the text[] column type.
func (TextArray) GormDataType() string { return "text[]" }

// MarshalJSON renders the array as a JSON array.
func (a TextArray) MarshalJSON() ([]byte, error) { return json.Marshal([]string(a)) }

// UnmarshalJSON parses a JSON array.
func (a *TextArray) UnmarshalJSON(data []byte) error {
	var out []string
	if err := json.Unmarshal(data, &out); err != nil {
		return err
	}
	*a = out
	return nil
}

// parseTextArray reads the PostgreSQL array literal form.
func parseTextArray(s string) []string {
	if len(s) >= 2 && s[0] == '{' && s[len(s)-1] == '}' {
		s = s[1 : len(s)-1]
	}
	if s == "" {
		return []string{}
	}
	out := make([]string, 0, 4)
	var cur strings.Builder
	inQuotes := false
	escaped := false
	for _, r := range s {
		switch {
		case escaped:
			cur.WriteRune(r)
			escaped = false
		case r == '\\':
			escaped = true
		case r == '"':
			inQuotes = !inQuotes
		case r == ',' && !inQuotes:
			out = append(out, cur.String())
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	out = append(out, cur.String())
	return out
}
