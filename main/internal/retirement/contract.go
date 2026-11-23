// Package retirement owns the trusted, durable page-retirement workflow. It
// withdraws every server-registered Router route target with an independent
// CAS and only then marks the Website page retired. No visitor input selects a
// page, route, locale or Router operation.
package retirement

import (
	"context"
	"regexp"

	routerapi "github.com/Zubrium/cdn/engines/router/presentation"
	websiteapi "github.com/Zubrium/cdn/engines/website/presentation"
	"github.com/Zubrium/cdn/pkg/contracts"
)

const (
	moduleID  = "retirement"
	namespace = "main/retirement"
	scopeType = "branch"

	recordTypeJob       = "retirement_job"
	recordTypeReceipt   = "retirement_receipt"
	recordTypeTombstone = "retired_page"

	stateRunning   = "running"
	stateSucceeded = "succeeded"
	stateRetryable = "retryable_failed"

	routeStatePending   = "pending"
	routeStateRemoved   = "removed"
	routeStateRetryable = "retryable_failed"

	maxRoutes = 64
)

var (
	ErrUnavailable = contracts.ErrRetirementUnavailable
	ErrInvalid     = contracts.ErrInvalidRetirementRequest
	ErrConflict    = contracts.ErrRetirementConflict
	ErrNotFound    = contracts.ErrRetirementNotFound

	uuidPattern    = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	idempotentKey  = regexp.MustCompile(`^[A-Za-z0-9._:-]{16,128}$`)
	ifMatchPattern = regexp.MustCompile(`^"[1-9][0-9]*"$`)
)

func ModuleDataDescriptor() contracts.RecordDescriptor {
	return contracts.RecordDescriptor{
		OwnerID: moduleID,
		Namespaces: []contracts.RecordNamespace{{
			Name:       namespace,
			ScopeTypes: []string{scopeType},
			Fields: map[string]contracts.RecordSelectorField{
				"record_type": {Type: contracts.RecordSelectorString},
				"page_id":     {Type: contracts.RecordSelectorString},
				"state":       {Type: contracts.RecordSelectorString},
			},
		}},
	}
}

// Module satisfies Core's generic component registry without exposing an
// engine-specific operation to other modules.
type Module struct{}

func (Module) ComponentDescriptor() contracts.ComponentDescriptor {
	return contracts.ComponentDescriptor{ID: moduleID, Kind: "composition", Version: 1}
}

// Website is the narrow, authenticated lifecycle facade. Route selection and
// withdrawal never enter this interface.
type Website interface {
	Authenticate(context.Context, string) (context.Context, error)
	GetPage(context.Context, string) (websiteapi.Page, error)
	RetirePage(context.Context, websiteapi.RetirePageRequest) (websiteapi.Page, error)
}

// Router is the narrow, authenticated route-CAS facade used only by the
// trusted composition root.
type Router interface {
	Authenticate(context.Context, string) (context.Context, error)
	GetRoute(context.Context, string) (routerapi.Route, error)
	DeleteRoute(context.Context, routerapi.DeleteRouteRequest) error
}

// Config is complete only when the static composition root supplies every
// trusted capability and the page-to-route registry.
type Config struct {
	BranchID          string
	Store             contracts.RecordDataStore
	Routes            map[string][]string
	Website           Website
	WebsiteCredential string
	Router            Router
	RouterCredential  string
}

// Service coordinates durable route withdrawal and page lifecycle closure.
type Service struct {
	config Config
}

func NewService(config Config) (*Service, error) {
	if config.BranchID == "" || config.Store == nil || config.Website == nil || config.Router == nil ||
		config.WebsiteCredential == "" || config.RouterCredential == "" || len(config.Routes) == 0 {
		return nil, ErrUnavailable
	}
	seen := make(map[string]struct{}, len(config.Routes))
	for pageID, routes := range config.Routes {
		if !uuidPattern.MatchString(pageID) || len(routes) == 0 || len(routes) > maxRoutes {
			return nil, ErrInvalid
		}
		for _, routeID := range routes {
			if !uuidPattern.MatchString(routeID) {
				return nil, ErrInvalid
			}
			key := pageID + "\x00" + routeID
			if _, duplicate := seen[key]; duplicate {
				return nil, ErrInvalid
			}
			seen[key] = struct{}{}
		}
	}
	return &Service{config: config}, nil
}
