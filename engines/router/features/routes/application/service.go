// Package application owns Router route authorization, validation, idempotency,
// and revision behavior.
package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"regexp"
	"strings"
	"time"

	core "github.com/Zubrium/cdn/core/presentation"
	"github.com/Zubrium/cdn/engines/router/features/routes/model"
	"github.com/Zubrium/cdn/engines/router/features/routes/repository"
	"github.com/google/uuid"
)

var (
	ErrUnauthorized  = errors.New("Router request is unauthorized")
	ErrForbidden     = errors.New("Router request is forbidden")
	ErrNotFound      = repository.ErrNotFound
	ErrConflict      = repository.ErrConflict
	ErrInvalid       = errors.New("invalid Router request")
	ErrUnavailable   = repository.ErrUnavailable
	validIdempotency = regexp.MustCompile(`^[A-Za-z0-9._:-]{16,128}$`)
)

type Identity interface {
	Authenticate(context.Context, string) (core.Session, error)
	AuthenticateSession(context.Context, string) (core.Session, core.Principal, error)
}

type GroupAuthorizer interface {
	RequireGroupLevel(context.Context, core.Session, []string, core.AccessLevel) error
}

type TargetValidator interface {
	ValidateRouteTarget(context.Context, model.Target, string) error
}

type Config struct {
	BranchID        string
	AccessGroup     string
	DefaultPageSize int
	MaxPageSize     int
}

type CreateRouteInput struct {
	BranchID       string
	Host           string
	Path           string
	Match          string
	Target         model.Target
	IdempotencyKey string
}

type ReplaceTargetInput struct {
	RouteID          string
	Target           model.Target
	ExpectedRevision int64
}

type DeleteRouteInput struct {
	RouteID          string
	ExpectedRevision int64
}

type Service struct {
	repository repository.Repository
	identity   Identity
	authorizer GroupAuthorizer
	validator  TargetValidator
	config     Config
	now        func() time.Time
}

type sessionKey struct{}

func New(repo repository.Repository, identity Identity, authorizer GroupAuthorizer, validator TargetValidator, config Config) (*Service, error) {
	if nilDependency(repo) || nilDependency(identity) || nilDependency(authorizer) || nilDependency(validator) ||
		config.BranchID == "" || len(config.BranchID) > 128 || strings.TrimSpace(config.BranchID) != config.BranchID ||
		!core.ValidAccessGroupKey(config.AccessGroup) || config.DefaultPageSize < 1 || config.MaxPageSize < config.DefaultPageSize || config.MaxPageSize > 100 {
		return nil, ErrInvalid
	}
	return &Service{repository: repo, identity: identity, authorizer: authorizer, validator: validator, config: config, now: time.Now}, nil
}

// Authenticate validates an ordinary credential and records only its opaque
// Core session in the request context. Cookie parsing stays in Presentation.
func (service *Service) Authenticate(ctx context.Context, credential string) (context.Context, error) {
	if service == nil || ctx == nil || credential == "" {
		return nil, ErrUnauthorized
	}
	session, err := service.identity.Authenticate(ctx, credential)
	if err != nil {
		return nil, mapCoreError(err)
	}
	return context.WithValue(ctx, sessionKey{}, session), nil
}

// AuthenticateSession resolves a Core session token; it does not inspect HTTP
// cookies or accept any browser transport data.
func (service *Service) AuthenticateSession(ctx context.Context, token string) (context.Context, error) {
	if service == nil || ctx == nil || token == "" {
		return nil, ErrUnauthorized
	}
	session, _, err := service.identity.AuthenticateSession(ctx, token)
	if err != nil {
		return nil, mapCoreError(err)
	}
	return context.WithValue(ctx, sessionKey{}, session), nil
}

func (service *Service) ListRoutes(ctx context.Context, branchID, host, cursor string, limit int) ([]model.Route, string, error) {
	if err := service.authorize(ctx, core.AccessLevelRead); err != nil {
		return nil, "", err
	}
	if branchID != service.config.BranchID || len(cursor) > 256 {
		return nil, "", ErrInvalid
	}
	if limit == 0 {
		limit = service.config.DefaultPageSize
	}
	if limit < 1 || limit > service.config.MaxPageSize {
		return nil, "", ErrInvalid
	}
	if host != "" {
		normalized, err := model.NormalizeHost(host)
		if err != nil {
			return nil, "", ErrInvalid
		}
		host = normalized
	}
	items, next, err := service.repository.List(ctx, branchID, host, cursor, limit)
	if err != nil {
		return nil, "", mapRepositoryError(err)
	}
	return items, next, nil
}

func (service *Service) GetRoute(ctx context.Context, branchID, id string) (model.Route, error) {
	if err := service.authorize(ctx, core.AccessLevelRead); err != nil {
		return model.Route{}, err
	}
	if branchID != service.config.BranchID || !validUUID(id) {
		return model.Route{}, ErrNotFound
	}
	route, err := service.repository.GetByID(ctx, branchID, id)
	if err != nil {
		return model.Route{}, mapRepositoryError(err)
	}
	return route, nil
}

func (service *Service) CreateRoute(ctx context.Context, input CreateRouteInput) (model.Route, error) {
	if err := service.authorize(ctx, core.AccessLevelWrite); err != nil {
		return model.Route{}, err
	}
	if input.BranchID != "" && input.BranchID != service.config.BranchID || !validIdempotency.MatchString(input.IdempotencyKey) {
		return model.Route{}, ErrInvalid
	}
	host, err := model.NormalizeHost(input.Host)
	if err != nil {
		return model.Route{}, ErrInvalid
	}
	path, err := model.NormalizePath(input.Path)
	if err != nil {
		return model.Route{}, ErrInvalid
	}
	if err := service.validator.ValidateRouteTarget(ctx, input.Target, input.Match); err != nil {
		return model.Route{}, mapTargetError(err)
	}
	route, err := model.NewRoute(uuid.NewString(), service.config.BranchID, host, path, input.Match, input.Target, service.now())
	if err != nil {
		return model.Route{}, ErrInvalid
	}
	fingerprint, err := fingerprint(route)
	if err != nil {
		return model.Route{}, ErrInvalid
	}
	created, err := service.repository.Create(ctx, route, input.IdempotencyKey, fingerprint)
	if err != nil {
		return model.Route{}, mapRepositoryError(err)
	}
	return created, nil
}

func (service *Service) ReplaceTarget(ctx context.Context, branchID string, input ReplaceTargetInput) (model.Route, error) {
	if err := service.authorize(ctx, core.AccessLevelWrite); err != nil {
		return model.Route{}, err
	}
	if branchID != service.config.BranchID || !validUUID(input.RouteID) || input.ExpectedRevision < 1 {
		return model.Route{}, ErrInvalid
	}
	current, err := service.repository.GetByID(ctx, branchID, input.RouteID)
	if err != nil {
		return model.Route{}, mapRepositoryError(err)
	}
	if current.Target.Kind == model.TargetCoreFile && current.Match != model.MatchExact {
		return model.Route{}, ErrInvalid
	}
	if input.Target.Kind == model.TargetCoreFile && current.Match != model.MatchExact {
		return model.Route{}, ErrInvalid
	}
	if err := service.validator.ValidateRouteTarget(ctx, input.Target, current.Match); err != nil {
		return model.Route{}, mapTargetError(err)
	}
	updated, err := service.repository.ReplaceTarget(ctx, branchID, input.RouteID, input.Target, input.ExpectedRevision, service.now())
	if err != nil {
		return model.Route{}, mapRepositoryError(err)
	}
	return updated, nil
}

func (service *Service) DeleteRoute(ctx context.Context, branchID string, input DeleteRouteInput) error {
	if err := service.authorize(ctx, core.AccessLevelWrite); err != nil {
		return err
	}
	if branchID != service.config.BranchID || !validUUID(input.RouteID) || input.ExpectedRevision < 1 {
		return ErrInvalid
	}
	return mapRepositoryError(service.repository.Delete(ctx, branchID, input.RouteID, input.ExpectedRevision))
}

// ResolveRoute returns the active mapping for an already selected static
// branch. Public resolution deliberately has no end-user credential input:
// delivery is authorized by the route's configured Core identity instead.
func (service *Service) ResolveRoute(ctx context.Context, host, escapedPath string) (model.Route, error) {
	if service == nil || ctx == nil {
		return model.Route{}, ErrUnavailable
	}
	host, err := model.NormalizeRequestHost(host)
	if err != nil {
		return model.Route{}, ErrNotFound
	}
	path, err := model.NormalizePath(escapedPath)
	if err != nil {
		return model.Route{}, ErrNotFound
	}
	route, err := service.repository.FindMatch(ctx, service.config.BranchID, host, path)
	if err != nil {
		return model.Route{}, mapRepositoryError(err)
	}
	return route, nil
}

func (service *Service) authorize(ctx context.Context, minimum core.AccessLevel) error {
	if service == nil || ctx == nil {
		return ErrUnavailable
	}
	session, ok := ctx.Value(sessionKey{}).(core.Session)
	if !ok {
		return ErrUnauthorized
	}
	if err := service.authorizer.RequireGroupLevel(ctx, session, []string{service.config.AccessGroup}, minimum); err != nil {
		return mapCoreError(err)
	}
	return nil
}

func fingerprint(route model.Route) (string, error) {
	value := struct {
		BranchID string       `json:"branch_id"`
		Host     string       `json:"host"`
		Path     string       `json:"path"`
		Match    string       `json:"match"`
		Target   model.Target `json:"target"`
	}{route.BranchID, route.Host, route.Path, route.Match, route.Target}
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func validUUID(value string) bool {
	parsed, err := uuid.Parse(value)
	return err == nil && parsed != uuid.Nil && parsed.String() == value
}

func mapCoreError(err error) error {
	switch {
	case errors.Is(err, core.ErrUnauthorized):
		return ErrUnauthorized
	case errors.Is(err, core.ErrForbidden):
		return ErrForbidden
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	default:
		return ErrUnavailable
	}
}

func mapRepositoryError(err error) error {
	switch {
	case errors.Is(err, repository.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, repository.ErrConflict):
		return ErrConflict
	case errors.Is(err, repository.ErrInvalid):
		return ErrInvalid
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	case err == nil:
		return nil
	default:
		return ErrUnavailable
	}
}

func mapTargetError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if errors.Is(err, ErrUnavailable) {
		return ErrUnavailable
	}
	if errors.Is(err, core.ErrUnavailable) || errors.Is(err, core.ErrUnauthorized) {
		return ErrUnavailable
	}
	if errors.Is(err, ErrForbidden) || errors.Is(err, core.ErrForbidden) {
		return ErrForbidden
	}
	if errors.Is(err, ErrNotFound) || errors.Is(err, core.ErrFileNotFound) {
		return ErrNotFound
	}
	return ErrInvalid
}

func nilDependency(value any) bool {
	if value == nil {
		return true
	}
	// Unlike a normal nil check, this also rejects a typed nil implementation.
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
