// Package publiccontentruntime implements the server-configured public
// content-runtime Router operation. It contains no HTTP handlers: the Router
// HTTP adapter owns request normalization and response serialization.
package publiccontentruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"

	"github.com/Zubrium/cdn/pkg/contracts"
	httpjson "github.com/Zubrium/cdn/pkg/json"
	"github.com/google/uuid"
)

const (
	OperationID     = "public.content-runtime"
	ContractVersion = 1
)

const (
	maxConfigBytes = 64 << 10
	maxPathBytes   = 8192
)

// Reader is the narrow content capability required by this operation. The
// composition root owns the concrete adapter and may resolve the configured
// page snapshot internally; no visitor input reaches the Reader beyond the
// server-side route configuration.
type Reader interface {
	ReadPublishedContent(context.Context, string, string) (contracts.PublishedContent, error)
}

// Config is strictly server-side route configuration. Unknown JSON fields are
// rejected by the shared strict decoder.
type Config struct {
	Enabled        bool   `json:"enabled"`
	PageSnapshotID string `json:"page_snapshot_id"`
	Locale         string `json:"locale"`
}

func (config Config) Validate() error {
	if !config.Enabled {
		return contracts.ErrInvalidTarget
	}
	id, err := uuid.Parse(config.PageSnapshotID)
	if err != nil || id == uuid.Nil || id.String() != config.PageSnapshotID {
		return contracts.ErrInvalidTarget
	}
	if !validLocale(config.Locale) {
		return contracts.ErrInvalidTarget
	}
	return nil
}

func validLocale(locale string) bool {
	if locale == "" || len(locale) > 35 {
		return false
	}
	for i := 0; i < len(locale); i++ {
		char := locale[i]
		switch {
		case char >= 'a' && char <= 'z', char >= 'A' && char <= 'Z', char >= '0' && char <= '9', char == '-', char == '_':
		default:
			return false
		}
	}
	return true
}

func decodeConfig(raw json.RawMessage) (Config, error) {
	if len(raw) == 0 || len(raw) > maxConfigBytes {
		return Config{}, contracts.ErrInvalidTarget
	}
	var config Config
	if err := httpjson.DecodeBytes(raw, &config); err != nil || config.Validate() != nil {
		return Config{}, contracts.ErrInvalidTarget
	}
	return config, nil
}

// Operation is stateless apart from its immutable Reader dependency.
type Operation struct {
	reader Reader
}

// New fails closed when no Reader is supplied.
func New(reader Reader) (*Operation, error) {
	if isNilReader(reader) {
		return nil, contracts.ErrPublicUnavailable
	}
	return &Operation{reader: reader}, nil
}

func isNilReader(reader Reader) bool {
	if reader == nil {
		return true
	}
	value := reflect.ValueOf(reader)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func (operation *Operation) Descriptor() contracts.PublicOperationDescriptor {
	if operation == nil {
		return contracts.PublicOperationDescriptor{}
	}
	return contracts.PublicOperationDescriptor{
		ID:                  OperationID,
		ContractVersion:     ContractVersion,
		Methods:             []string{http.MethodGet, http.MethodHead},
		MaxRequestBytes:     1,
		AllowedContentTypes: []string{"application/json"},
	}
}

func (operation *Operation) ValidateRouteConfig(raw json.RawMessage) error {
	if operation == nil {
		return contracts.ErrPublicInvalidOperation
	}
	_, err := decodeConfig(raw)
	return err
}

// Execute evaluates the normalized If-None-Match precondition supplied by
// Router's HTTP adapter. Visitor input never overrides the server-side config.
func (operation *Operation) Execute(ctx context.Context, raw json.RawMessage, request contracts.PublicOperationRequest) (contracts.DeliveryResponse, error) {
	return operation.execute(ctx, raw, request, request.IfNoneMatch)
}

func (operation *Operation) execute(ctx context.Context, raw json.RawMessage, request contracts.PublicOperationRequest, ifNoneMatch string) (contracts.DeliveryResponse, error) {
	if operation == nil || operation.reader == nil || ctx == nil {
		return contracts.DeliveryResponse{}, contracts.ErrPublicUnavailable
	}
	switch request.Method {
	case http.MethodGet, http.MethodHead:
	default:
		return contracts.DeliveryResponse{}, contracts.ErrPublicInvalid
	}
	if request.Query != "" || request.ContentType != "" || request.IdempotencyKey != "" ||
		len(request.EscapedPath) > maxPathBytes || requireEmptyBody(request) != nil {
		return contracts.DeliveryResponse{}, contracts.ErrPublicInvalid
	}
	config, err := decodeConfig(raw)
	if err != nil {
		return contracts.DeliveryResponse{}, err
	}

	content, err := operation.reader.ReadPublishedContent(ctx, config.PageSnapshotID, config.Locale)
	if err != nil {
		return contracts.DeliveryResponse{}, contracts.ErrPublicUnavailable
	}
	body, err := json.Marshal(content)
	if err != nil {
		return contracts.DeliveryResponse{}, contracts.ErrPublicUnavailable
	}
	etag := `"` + strconv.FormatInt(content.ContentRevision, 10) + `"`
	if matchesIfNoneMatch(ifNoneMatch, etag) {
		return contracts.DeliveryResponse{StatusCode: http.StatusNotModified, ETag: etag, CacheControl: "no-store"}, nil
	}
	if request.Method == http.MethodHead {
		return contracts.DeliveryResponse{StatusCode: http.StatusOK, ContentType: "application/json", ContentLength: int64(len(body)),
			ETag: etag, CacheControl: "no-store"}, nil
	}
	return contracts.DeliveryResponse{StatusCode: http.StatusOK, ContentType: "application/json", ContentLength: int64(len(body)),
		ETag: etag, CacheControl: "no-store", Body: io.NopCloser(bytes.NewReader(body))}, nil
}

func requireEmptyBody(request contracts.PublicOperationRequest) error {
	if request.Body == nil || request.ContentLength != 0 {
		return contracts.ErrPublicInvalid
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, 2))
	if err != nil || len(body) != 0 {
		return contracts.ErrPublicInvalid
	}
	return nil
}

func matchesIfNoneMatch(value, etag string) bool {
	if value == "" {
		return false
	}
	for _, candidate := range strings.Split(value, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || strings.TrimPrefix(candidate, "W/") == etag {
			return true
		}
	}
	return false
}

var _ contracts.PublicOperation = (*Operation)(nil)
