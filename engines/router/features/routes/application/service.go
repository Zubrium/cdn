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

	"github.com/Zubrium/cdn/engines/router/features/routes/model"
	"github.com/Zubrium/cdn/engines/router/features/routes/repository"
	"github.com/Zubrium/cdn/pkg/contracts"
	"github.com/google/uuid"
)

var (
	ErrUnauthorized  = contracts.ErrPublicUnauthorized
	ErrForbidden     = contracts.ErrPublicForbidden
	ErrNotFound      = contracts.ErrPublicNotFound
	ErrConflict      = contracts.ErrPublicConflict
	ErrInvalid       = contracts.ErrPublicInvalid
	ErrUnavailable   = contracts.ErrPublicUnavailable
	validIdempotency = regexp.MustCompile(`^[A-Za-z0-9._:-]{16,128}$`)
)

type Identity interface {
	Authenticate(context.Context, string) (contracts.Principal, error)
	AuthenticateSession(context.Context, string) (contracts.Principal, error)
}

type GroupAuthorizer interface {
	RequireGroupLevel(context.Context, contracts.AuthorizationSubject, []string, contracts.AccessLevel) error
}

type TargetValidator interface {
	ValidateRouteTarget(context.Context, model.Target, string) error
}

// RouteFingerprintSource is the authoritative, server-owned identity check
// used before a cached public route match may be reused. It is never populated
// from visitor input.
type RouteFingerprintSource interface {
	ResolveRouteFingerprint(context.Context, string, string) (string, error)
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

// ReplaceLocaleTargetInput activates exactly one configured (route, locale)
// slot. ExpectedRevision is that locale slot's independent CAS revision.
type ReplaceLocaleTargetInput struct {
	RouteID                string
	Locale                 string
	ArtifactManifestFileID string
	ExpectedRevision       int64
	IdempotencyKey         string
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
		!contracts.ValidAccessGroupKey(config.AccessGroup) || config.DefaultPageSize < 1 || config.MaxPageSize < config.DefaultPageSize || config.MaxPageSize > 100 {
		return nil, ErrInvalid
	}
	return &Service{repository: repo, identity: identity, authorizer: authorizer, validator: validator, config: config, now: time.Now}, nil
}

// Authenticate validates a configured credential and retains it only as an
// internal DataBus authorization subject. Cookie parsing stays in Presentation.
func (service *Service) Authenticate(ctx context.Context, credential string) (context.Context, error) {
	if service == nil || ctx == nil || credential == "" {
		return nil, ErrUnauthorized
	}
	_, err := service.identity.Authenticate(ctx, credential)
	if err != nil {
		return nil, mapCoreError(err)
	}
	return context.WithValue(ctx, sessionKey{}, contracts.AuthorizationSubject{Credential: credential}), nil
}

// AuthenticateSession validates a Core session token through DataBus; it does
// not inspect HTTP cookies or accept browser transport data.
func (service *Service) AuthenticateSession(ctx context.Context, token string) (context.Context, error) {
	if service == nil || ctx == nil || token == "" {
		return nil, ErrUnauthorized
	}
	_, err := service.identity.AuthenticateSession(ctx, token)
	if err != nil {
		return nil, mapCoreError(err)
	}
	return context.WithValue(ctx, sessionKey{}, contracts.AuthorizationSubject{SessionToken: token}), nil
}

func (service *Service) ListRoutes(ctx context.Context, branchID, host, cursor string, limit int) ([]model.Route, string, error) {
	if err := service.authorize(ctx, contracts.AccessLevelRead); err != nil {
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
	if err := service.authorize(ctx, contracts.AccessLevelRead); err != nil {
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
	if err := service.authorize(ctx, contracts.AccessLevelWrite); err != nil {
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
	if err := service.authorize(ctx, contracts.AccessLevelWrite); err != nil {
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

// ReplaceLocaleTarget performs an independent, retry-safe CAS for one route
// locale. Sibling locale revisions and unverified artifacts are untouched.
func (service *Service) ReplaceLocaleTarget(ctx context.Context, branchID string, input ReplaceLocaleTargetInput) (model.Route, error) {
	if err := service.authorize(ctx, contracts.AccessLevelWrite); err != nil {
		return model.Route{}, err
	}
	locale, err := model.CanonicalLocale(input.Locale)
	if err != nil || locale != input.Locale || branchID != service.config.BranchID || !validUUID(input.RouteID) ||
		!validUUID(input.ArtifactManifestFileID) || input.ExpectedRevision < 1 || !validIdempotency.MatchString(input.IdempotencyKey) {
		return model.Route{}, ErrInvalid
	}
	current, err := service.repository.GetByID(ctx, branchID, input.RouteID)
	if err != nil {
		return model.Route{}, mapRepositoryError(err)
	}
	_, found := current.Target.LocaleTarget(locale)
	if !found || current.Target.Kind != model.TargetStaticSiteArtifact {
		return model.Route{}, ErrNotFound
	}
	updatedTarget := current.Target
	updatedTarget.LocaleTargets = append([]model.LocaleArtifactTarget(nil), current.Target.LocaleTargets...)
	for index := range updatedTarget.LocaleTargets {
		if updatedTarget.LocaleTargets[index].Locale == locale {
			updatedTarget.LocaleTargets[index].ArtifactManifestFileID = input.ArtifactManifestFileID
			break
		}
	}
	if err := service.validator.ValidateRouteTarget(ctx, updatedTarget, current.Match); err != nil {
		return model.Route{}, mapTargetError(err)
	}
	route, err := service.repository.ReplaceLocaleTarget(ctx, branchID, input.RouteID, repository.LocaleTargetUpdate{
		Locale: locale, ArtifactManifestFileID: input.ArtifactManifestFileID,
		ExpectedRevision: input.ExpectedRevision, IdempotencyKey: input.IdempotencyKey,
	}, service.now())
	if err != nil {
		return model.Route{}, mapRepositoryError(err)
	}
	for _, item := range route.Target.LocaleTargets {
		if item.Locale == locale {
			return route, nil
		}
	}
	return model.Route{}, ErrConflict
}

func (service *Service) DeleteRoute(ctx context.Context, branchID string, input DeleteRouteInput) error {
	if err := service.authorize(ctx, contracts.AccessLevelWrite); err != nil {
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

// RouteFingerprint binds the route revision, its locale assignment revisions
// and the target identity into one value. A changed publication or assignment
// changes the fingerprint, so a cached match cannot outlive its authority.
func RouteFingerprint(route model.Route) string {
	identity := struct {
		ID        string       `json:"id"`
		BranchID  string       `json:"branch_id"`
		Revision  int64        `json:"revision"`
		UpdatedAt time.Time    `json:"updated_at"`
		Target    model.Target `json:"target"`
	}{route.ID, route.BranchID, route.Revision, route.UpdatedAt, route.Target}
	data, err := json.Marshal(identity)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

// ResolveRouteFingerprint reads the authoritative record for one configured
// branch. Delivery has no end-user credential: this is a server-owned check
// and never accepts an identity supplied by a visitor.
func (service *Service) ResolveRouteFingerprint(ctx context.Context, branchID, routeID string) (string, error) {
	if service == nil || ctx == nil || branchID != service.config.BranchID || !validUUID(routeID) {
		return "", ErrInvalid
	}
	route, err := service.repository.GetByID(ctx, branchID, routeID)
	if err != nil {
		return "", mapRepositoryError(err)
	}
	if route.State != model.StateActive || route.ValidateForStorage() != nil {
		return "", ErrNotFound
	}
	fingerprint := RouteFingerprint(route)
	if fingerprint == "" {
		return "", ErrUnavailable
	}
	return fingerprint, nil
}

func (service *Service) authorize(ctx context.Context, minimum contracts.AccessLevel) error {
	if service == nil || ctx == nil {
		return ErrUnavailable
	}
	subject, ok := ctx.Value(sessionKey{}).(contracts.AuthorizationSubject)
	if !ok {
		return ErrUnauthorized
	}
	if err := service.authorizer.RequireGroupLevel(ctx, subject, []string{service.config.AccessGroup}, minimum); err != nil {
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
	case errors.Is(err, contracts.ErrAccessUnauthorized):
		return ErrUnauthorized
	case errors.Is(err, contracts.ErrAccessForbidden):
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
	if errors.Is(err, contracts.ErrAccessUnavailable) || errors.Is(err, contracts.ErrAccessUnauthorized) {
		return ErrUnavailable
	}
	if errors.Is(err, ErrForbidden) || errors.Is(err, contracts.ErrAccessForbidden) || errors.Is(err, contracts.ErrFileForbidden) {
		return ErrForbidden
	}
	if errors.Is(err, ErrNotFound) || errors.Is(err, contracts.ErrFileNotFound) {
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
