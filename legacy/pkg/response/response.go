// Package response provides consistent JSON/error responses and request
// metadata helpers.
package response

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

type requestIDKey struct{}

func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, id)
}

func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

type ErrorDetail struct {
	Path    string `json:"path,omitempty"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
}

type APIError struct {
	Code      string        `json:"code"`
	Message   string        `json:"message"`
	Details   []ErrorDetail `json:"details,omitempty"`
	RequestID string        `json:"request_id,omitempty"`
}

type errorEnvelope struct {
	Error APIError `json:"error"`
}

// JSON writes a JSON response.
func JSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	if value == nil {
		return
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(value)
}

// Error writes the shared API error envelope.
func Error(w http.ResponseWriter, r *http.Request, status int, code, message string, details ...ErrorDetail) {
	JSON(w, status, errorEnvelope{Error: APIError{
		Code:      code,
		Message:   message,
		Details:   details,
		RequestID: RequestID(r.Context()),
	}})
}

func NoContent(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNoContent)
}

var ErrEmptyBody = errors.New("request body is empty")

type DecodeError struct {
	Status  int
	Code    string
	Message string
}

func (e *DecodeError) Error() string { return e.Message }

// DecodeJSON decodes exactly one JSON object. It enforces a byte limit and
// rejects unknown fields so typos in administrative requests fail loudly.
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any, limit int64) error {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return &DecodeError{Status: http.StatusRequestEntityTooLarge, Code: "request_too_large", Message: "Тело запроса превышает допустимый размер"}
		}
		if errors.Is(err, io.EOF) {
			return &DecodeError{Status: http.StatusBadRequest, Code: "empty_body", Message: "Тело запроса обязательно"}
		}
		return &DecodeError{Status: http.StatusBadRequest, Code: "invalid_json", Message: "Некорректный JSON: " + err.Error()}
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return &DecodeError{Status: http.StatusBadRequest, Code: "invalid_json", Message: "Ожидается ровно один JSON-объект"}
	}
	return nil
}

// WriteDecodeError converts a DecodeError into the shared envelope.
func WriteDecodeError(w http.ResponseWriter, r *http.Request, err error) bool {
	var de *DecodeError
	if errors.As(err, &de) {
		Error(w, r, de.Status, de.Code, de.Message)
		return true
	}
	return false
}

type Cursor struct {
	CreatedAt time.Time `json:"t"`
	ID        string    `json:"i"`
	SortOrder *int64    `json:"o,omitempty"`
}

func EncodeCursor(createdAt time.Time, id uuid.UUID) string {
	raw, _ := json.Marshal(Cursor{CreatedAt: createdAt.UTC(), ID: id.String()})
	return base64.RawURLEncoding.EncodeToString(raw)
}

func EncodeOrderedCursor(sortOrder int64, createdAt time.Time, id uuid.UUID) string {
	raw, _ := json.Marshal(Cursor{CreatedAt: createdAt.UTC(), ID: id.String(), SortOrder: &sortOrder})
	return base64.RawURLEncoding.EncodeToString(raw)
}

func DecodeCursor(raw string) (Cursor, bool) {
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return Cursor{}, false
	}
	var c Cursor
	if err := json.Unmarshal(decoded, &c); err != nil {
		return Cursor{}, false
	}
	if c.CreatedAt.IsZero() || c.ID == "" {
		return Cursor{}, false
	}
	if _, err := uuid.Parse(c.ID); err != nil {
		return Cursor{}, false
	}
	return c, true
}

// ParseLimit returns a bounded page size.
func ParseLimit(raw string, def, max int) (int, error) {
	if strings.TrimSpace(raw) == "" {
		return def, nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 1 || n > max {
		return 0, fmt.Errorf("limit must be between 1 and %d", max)
	}
	return n, nil
}

func ParseBearer(r *http.Request) (string, bool) {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if header == "" {
		return "", false
	}
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", false
	}
	secret := strings.TrimSpace(parts[1])
	if secret == "" {
		return "", false
	}
	return secret, true
}
