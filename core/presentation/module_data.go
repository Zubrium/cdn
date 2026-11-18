package presentation

import (
	"errors"

	appengine "github.com/Zubrium/cdn/core/application/engine_data"
)

var (
	ErrModuleDataNotFound         = appengine.ErrModuleDataNotFound
	ErrModuleDataConflict         = appengine.ErrModuleDataConflict
	ErrModuleDataInvalid          = appengine.ErrModuleDataInvalid
	ErrModuleDataLimitExceeded    = appengine.ErrModuleDataLimitExceeded
	ErrModuleDataUnavailable      = appengine.ErrModuleDataUnavailable
	ErrModuleDataCapabilityIssued = errors.New("core engine data capability already issued")
)

type SelectorType = appengine.SelectorType

const (
	SelectorString      = appengine.SelectorString
	SelectorNumber      = appengine.SelectorNumber
	SelectorBoolean     = appengine.SelectorBoolean
	SelectorStringArray = appengine.SelectorStringArray
)

type SelectorOperator = appengine.SelectorOperator

const (
	SelectorEqual    = appengine.SelectorEqual
	SelectorIn       = appengine.SelectorIn
	SelectorContains = appengine.SelectorContains
)

type SelectorField = appengine.SelectorField
type ModuleDataNamespace = appengine.ModuleDataNamespace
type ModuleDataDescriptor = appengine.ModuleDataDescriptor
type ModuleDataLimits = appengine.ModuleDataLimits
type ModuleDataKey = appengine.ModuleDataKey
type ModuleDataValue = appengine.ModuleDataValue
type ModuleDataRecord = appengine.ModuleDataRecord
type ModuleDataMutation = appengine.ModuleDataMutation
type ModuleDataSelector = appengine.ModuleDataSelector

// ModuleDataStore is a capability bound to one engine ID and its declared
// namespaces. Its implementation is an application service with no database
// or storage dependency in the public Core contract.
type ModuleDataStore = appengine.Store

func ValidModuleDataLimits(limits ModuleDataLimits) bool {
	return appengine.ValidModuleDataLimits(limits)
}

// RegisterDataModule issues the engine-scoped data capability from the Core
// composition root after validating registration and its declared contract.
func (r *Runtime) RegisterDataModule(descriptor ModuleDataDescriptor) (*ModuleDataStore, error) {
	if err := r.valid(); err != nil {
		return nil, err
	}
	if r.Registry == nil {
		return nil, ErrModuleDataInvalid
	}
	if r.engineDataRepository == nil {
		return nil, ErrModuleDataUnavailable
	}
	if _, err := r.Registry.Lookup(descriptor.ModuleID); err != nil {
		return nil, ErrModuleDataInvalid
	}

	r.moduleDataMu.Lock()
	defer r.moduleDataMu.Unlock()
	if _, exists := r.moduleData[descriptor.ModuleID]; exists {
		return nil, ErrModuleDataCapabilityIssued
	}
	store, err := appengine.NewStore(r.engineDataRepository, descriptor, r.moduleDataLimits)
	if err != nil {
		return nil, err
	}
	if r.moduleData == nil {
		r.moduleData = make(map[string]struct{})
	}
	r.moduleData[descriptor.ModuleID] = struct{}{}
	return store, nil
}
