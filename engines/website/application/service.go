// Package application contains Website use cases backed by the scoped Core
// engine-data capability. It does not access SQL, S3, or another engine.
package application

import (
	"context"
	"errors"
	"net"
	"net/url"
	"reflect"
	"regexp"
	"strings"

	core "github.com/Zubrium/cdn/core/presentation"
	websiteapi "github.com/Zubrium/cdn/engines/website/presentation"
)

var (
	errConfigInvalid = errors.New("invalid Website configuration")
	errDependency    = errors.New("Website dependency unavailable")
)

var identifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
var snapshotIDPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
var featureVersionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.+_-]{0,127}$`)

type RecordStore interface {
	Get(context.Context, core.ModuleDataKey) (core.ModuleDataRecord, error)
	List(context.Context, string, string, string, string, []core.ModuleDataSelector, int) ([]core.ModuleDataKey, []core.ModuleDataRecord, string, error)
	Batch(context.Context, []core.ModuleDataMutation) error
	MaxPageSize() int
}

type Config struct {
	AccessGroup string
	Sites       []string
	Branches    []BranchConfig
}

// BranchConfig is immutable startup topology. Only its snapshot assignment is
// stored as Website engine data and changed through AssignBranch.
type BranchConfig struct {
	ID                string
	Name              string
	SiteID            string
	ListenAddress     string
	CORSOrigins       []string
	Indexable         bool
	Enabled           bool
	InitialSnapshotID *string
}

type Service struct {
	config      Config
	sites       map[string]struct{}
	branches    map[string]BranchConfig
	branchOrder []string
	store       RecordStore
	authority   core.GroupAuthorizationCapability
	sessions    core.SessionCapability
}

type actor struct {
	session core.Session
}

type actorContextKey struct{}

func NewService(config Config, store RecordStore, authority core.GroupAuthorizationCapability, sessions core.SessionCapability) (*Service, error) {
	if !validInterface(store) || !validInterface(authority) || !validInterface(sessions) ||
		!core.ValidAccessGroupKey(config.AccessGroup) || store.MaxPageSize() < 1 || len(config.Sites) == 0 || len(config.Sites) > 256 || len(config.Branches) > 1024 {
		return nil, errConfigInvalid
	}
	siteSet := make(map[string]struct{}, len(config.Sites))
	for _, id := range config.Sites {
		if !identifierPattern.MatchString(id) {
			return nil, errConfigInvalid
		}
		if _, duplicate := siteSet[id]; duplicate {
			return nil, errConfigInvalid
		}
		siteSet[id] = struct{}{}
	}
	branchByID := make(map[string]BranchConfig, len(config.Branches))
	branchIDs := make([]string, 0, len(config.Branches))
	for _, branch := range config.Branches {
		if !identifierPattern.MatchString(branch.ID) || strings.TrimSpace(branch.Name) != branch.Name || len([]rune(branch.Name)) < 1 || len([]rune(branch.Name)) > 100 ||
			!validListenAddress(branch.ListenAddress) || len(branch.CORSOrigins) > 32 {
			return nil, errConfigInvalid
		}
		if _, exists := siteSet[branch.SiteID]; !exists {
			return nil, errConfigInvalid
		}
		if _, duplicate := branchByID[branch.ID]; duplicate {
			return nil, errConfigInvalid
		}
		if branch.InitialSnapshotID != nil && !snapshotIDPattern.MatchString(*branch.InitialSnapshotID) {
			return nil, errConfigInvalid
		}
		seenOrigins := make(map[string]struct{}, len(branch.CORSOrigins))
		for _, origin := range branch.CORSOrigins {
			if !validOrigin(origin) {
				return nil, errConfigInvalid
			}
			if _, duplicate := seenOrigins[origin]; duplicate {
				return nil, errConfigInvalid
			}
			seenOrigins[origin] = struct{}{}
		}
		branch.CORSOrigins = append([]string(nil), branch.CORSOrigins...)
		if branch.InitialSnapshotID != nil {
			initial := *branch.InitialSnapshotID
			branch.InitialSnapshotID = &initial
		}
		branchByID[branch.ID] = branch
		branchIDs = append(branchIDs, branch.ID)
	}
	// Site and branch ordering is stable across replicas, so paginated API
	// results do not depend on Go map iteration order.
	sites := append([]string(nil), config.Sites...)
	for i := 0; i < len(sites); i++ {
		for j := i + 1; j < len(sites); j++ {
			if sites[j] < sites[i] {
				sites[i], sites[j] = sites[j], sites[i]
			}
		}
	}
	for i := 0; i < len(branchIDs); i++ {
		for j := i + 1; j < len(branchIDs); j++ {
			if branchIDs[j] < branchIDs[i] {
				branchIDs[i], branchIDs[j] = branchIDs[j], branchIDs[i]
			}
		}
	}
	config.Sites = sites
	config.Branches = append([]BranchConfig(nil), config.Branches...)
	return &Service{config: config, sites: siteSet, branches: branchByID, branchOrder: branchIDs,
		store: store, authority: authority, sessions: sessions}, nil
}

func (s *Service) Authenticate(ctx context.Context, credential string) (context.Context, error) {
	if s == nil || ctx == nil || strings.TrimSpace(credential) == "" {
		return nil, websiteapi.ErrUnauthorized
	}
	session, _, err := s.authority.Authenticate(ctx, credential)
	if err != nil {
		return nil, mapCoreError(err)
	}
	return context.WithValue(ctx, actorContextKey{}, actor{session: session}), nil
}

func (s *Service) AuthenticateSession(ctx context.Context, token string) (context.Context, error) {
	if s == nil || ctx == nil || strings.TrimSpace(token) == "" {
		return nil, websiteapi.ErrUnauthorized
	}
	session, _, err := s.sessions.AuthenticateSession(ctx, token)
	if err != nil {
		return nil, mapCoreError(err)
	}
	return context.WithValue(ctx, actorContextKey{}, actor{session: session}), nil
}

func (s *Service) requireLevel(ctx context.Context, minimum core.AccessLevel) error {
	if s == nil || ctx == nil {
		return websiteapi.ErrUnauthorized
	}
	current, ok := ctx.Value(actorContextKey{}).(actor)
	if !ok {
		return websiteapi.ErrUnauthorized
	}
	if err := s.authority.RequireGroupLevel(ctx, current.session, []string{s.config.AccessGroup}, minimum); err != nil {
		return mapCoreError(err)
	}
	return nil
}

func mapCoreError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, core.ErrUnauthorized):
		return websiteapi.ErrUnauthorized
	case errors.Is(err, core.ErrForbidden):
		return websiteapi.ErrForbidden
	case errors.Is(err, core.ErrModuleDataNotFound), errors.Is(err, core.ErrFileNotFound):
		return websiteapi.ErrNotFound
	case errors.Is(err, core.ErrModuleDataConflict), errors.Is(err, core.ErrRevisionConflict), errors.Is(err, core.ErrFileSetConflict):
		return websiteapi.ErrConflict
	case errors.Is(err, core.ErrModuleDataInvalid):
		return websiteapi.ErrInvalid
	case errors.Is(err, core.ErrModuleDataLimitExceeded):
		return websiteapi.ErrTooLarge
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	default:
		return websiteapi.ErrUnavailable
	}
}

func validInterface(value any) bool {
	if value == nil {
		return false
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return !reflected.IsNil()
	default:
		return true
	}
}

func validListenAddress(value string) bool {
	if value == "" || len(value) > 255 || strings.ContainsAny(value, "\r\n\t ") {
		return false
	}
	_, portText, err := net.SplitHostPort(value)
	if err != nil || portText == "" {
		return false
	}
	port := 0
	for _, char := range portText {
		if char < '0' || char > '9' {
			return false
		}
		port = port*10 + int(char-'0')
		if port > 65535 {
			return false
		}
	}
	return port > 0
}

func validOrigin(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && value != "" && value == strings.TrimSpace(value) && len(value) <= 512 &&
		(parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != "" && parsed.Host == strings.ToLower(parsed.Host) &&
		parsed.User == nil && parsed.Path == "" && parsed.RawQuery == "" && parsed.Fragment == "" && parsed.Opaque == "" &&
		value == parsed.Scheme+"://"+parsed.Host
}

var _ websiteapi.API = (*Service)(nil)
