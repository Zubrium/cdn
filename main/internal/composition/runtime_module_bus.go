package composition

import (
	"context"
	"errors"

	platform "github.com/Zubrium/cdn/core/presentation"
	"github.com/Zubrium/cdn/main/internal/bus"
	"github.com/Zubrium/cdn/pkg/contracts"
)

var errRuntimeModuleBusUnavailable = errors.New("runtime module DataBus is unavailable")

// newRuntimeModuleDispatcher is a Core-facing composition adapter. It binds a
// private Runtime to typed DataBus operations and returns only a module-scoped
// dispatcher; callers never receive the Runtime or a Core capability.
func newRuntimeModuleDispatcher(r *platform.Runtime, moduleID, owner, credential string, groups []string) (contracts.Dispatcher, error) {
	if r == nil || moduleID == "" || owner == "" || credential == "" {
		return nil, errRuntimeModuleBusUnavailable
	}
	dispatcher, err := bus.NewAllowlisted(bus.ConcurrencyLimits{MaxInFlight: 64}, runtimeModuleBusKeys()...)
	if err != nil {
		return nil, err
	}
	session, _, err := r.Identity().Authenticate(context.Background(), credential)
	if err != nil {
		return nil, err
	}
	files := coreSessionFileAdapter{files: r.Files(), session: session}
	if err := bus.RegisterCoreAccess(dispatcher, coreRuntimeAccess{runtime: r}, map[string][]string{moduleID: groups}); err != nil {
		return nil, err
	}
	if err := bus.RegisterFileCreateVerified(dispatcher, func(context.Context, string) (contracts.VerifiedFileCreator, error) { return files, nil }); err != nil {
		return nil, err
	}
	if err := bus.RegisterFileRead(dispatcher, func(context.Context, string) (contracts.CoreFileReader, error) { return files, nil }); err != nil {
		return nil, err
	}
	if err := bus.RegisterFileStat(dispatcher, func(context.Context, string, contracts.AuthorizationSubject) (contracts.CoreFileStat, error) {
		return coreSubjectFileStat{files: r.Files(), session: session}, nil
	}); err != nil {
		return nil, err
	}
	if err := bus.RegisterFileDelete(dispatcher, func(context.Context, string) (contracts.CoreFileDeleter, error) { return files, nil }); err != nil {
		return nil, err
	}
	if err := bus.RegisterFileSetOperations(dispatcher, func(context.Context, string) (contracts.CoreFiles, error) { return files, nil }); err != nil {
		return nil, err
	}
	if err := bus.RegisterWorkflowOperations(dispatcher, map[string]bus.WorkflowOperations{
		moduleID: coreWorkflowOperations{runtime: r, owner: owner},
	}); err != nil {
		return nil, err
	}
	if err := dispatcher.Seal(); err != nil {
		return nil, err
	}
	return bus.NewModuleDispatcher(dispatcher, moduleID), nil
}

func runtimeModuleBusKeys() []contracts.Key {
	return []contracts.Key{
		contracts.AccessAuthenticateOperation().Key,
		contracts.AccessAuthenticateSessionOperation().Key,
		contracts.AccessRequireGroupOperation().Key,
		contracts.FileCreateVerifiedOperation().Key,
		contracts.FileReadOperation().Key,
		contracts.FileStatOperation().Key,
		contracts.FileDeleteOperation().Key,
		contracts.FileSetGetOperation().Key,
		contracts.FileSetListOperation().Key,
		contracts.FileSetMembersOperation().Key,
		contracts.FileSetAddOperation().Key,
		contracts.FileSetRemoveOperation().Key,
		contracts.WorkflowStartOperation().Key,
		contracts.WorkflowStateOperation().Key,
		contracts.WorkflowTaskResultOperation().Key,
		contracts.WorkflowTaskIDResolveOperation().Key,
	}
}
