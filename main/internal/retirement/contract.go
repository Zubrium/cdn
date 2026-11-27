// Package retirement owns the trusted, durable page-retirement workflow. It
// withdraws every server-registered Router route target with an independent
// CAS and only then marks the Website page retired. No visitor input selects a
// page, route, locale or Router operation.
package retirement

import (
	"context"
	"regexp"

	"github.com/Zubrium/cdn/pkg/contracts"
)

const (
	moduleID  = "retirement"
	namespace = "main/retirement"
	scopeType = "global"
	scopeID   = "global"

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

// Module satisfies Core's generic component registration without exposing an
// engine-specific operation to other modules.
type Module struct{}

func (Module) ComponentDescriptor() contracts.ComponentDescriptor {
	return contracts.ComponentDescriptor{ID: moduleID, Kind: "composition", Version: 1}
}

// WorkflowStarter is the narrow, module-scoped port used to start the root
// task. It carries no Core runtime, capability or engine handle.
type WorkflowStarter interface {
	Start(context.Context, contracts.WorkflowStartInput) (contracts.WorkflowStartReceipt, error)
}

// Config is complete only when the static composition root supplies every
// trusted capability and the page-to-route registry.
type Config struct {
	Store       contracts.RecordDataStore
	Starter     WorkflowStarter
	Routes      map[string][]string
	MaxAttempts int
}

// Service coordinates durable route withdrawal and page lifecycle closure.
type Service struct {
	config Config
}

func NewService(config Config) (*Service, error) {
	if config.Store == nil || len(config.Routes) == 0 ||
		config.MaxAttempts < 1 || config.MaxAttempts > 100 {
		return nil, ErrUnavailable
	}
	seen := make(map[string]struct{}, len(config.Routes))
	for pageID, routes := range config.Routes {
		if len(routes) == 0 || len(routes) > maxRoutes || !validID(pageID) {
			return nil, ErrInvalid
		}
		for _, routeID := range routes {
			if !validID(routeID) {
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

func validID(value string) bool {
	// The same strict identifier grammar is shared with the root payload
	// validator; no engine package is imported to obtain it.
	return contracts.ValidWorkflowID(value)
}
