package bus

import (
	"context"

	"github.com/Zubrium/cdn/pkg/contracts"
)

// RegisterCoreAccess exposes only typed identity and group checks through the
// static DataBus allowlist. Module identity is fixed by NewModuleDispatcher;
// each module's allowed group keys come from the selected profile.
func RegisterCoreAccess(dispatcher *Dispatcher, access contracts.CoreAccess, groupsByModule map[string][]string) error {
	if dispatcher == nil || access == nil || len(groupsByModule) == 0 {
		return contracts.ErrAccessUnavailable
	}
	allowed := make(map[string]map[string]struct{}, len(groupsByModule))
	for moduleID, groups := range groupsByModule {
		if !moduleIdentityPattern.MatchString(moduleID) {
			return ErrInvalidModuleIdentity
		}
		set := make(map[string]struct{}, len(groups))
		for _, group := range groups {
			request := contracts.AccessRequireGroupRequest{
				Subject: contracts.AuthorizationSubject{Credential: "validation-only"},
				Groups:  []string{group}, Level: contracts.AccessLevelRead,
			}
			if contracts.AccessRequireGroupOperation().Validate(request) != nil {
				return contracts.ErrAccessInvalid
			}
			set[group] = struct{}{}
		}
		allowed[moduleID] = set
	}
	module := func(ctx context.Context) (string, map[string]struct{}, error) {
		moduleID := ModuleIDFromContext(ctx)
		groups, ok := allowed[moduleID]
		if !ok {
			return "", nil, ErrInvalidModuleIdentity
		}
		return moduleID, groups, nil
	}
	if err := Register(dispatcher, contracts.AccessAuthenticateOperation(), func(ctx context.Context, request contracts.AccessAuthenticateRequest) (contracts.AccessAuthenticateResponse, error) {
		if _, _, err := module(ctx); err != nil {
			return contracts.AccessAuthenticateResponse{}, err
		}
		principal, err := access.Authenticate(ctx, request.Credential)
		return contracts.AccessAuthenticateResponse{Principal: principal}, err
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.AccessAuthenticateSessionOperation(), func(ctx context.Context, request contracts.AccessAuthenticateSessionRequest) (contracts.AccessAuthenticateSessionResponse, error) {
		if _, _, err := module(ctx); err != nil {
			return contracts.AccessAuthenticateSessionResponse{}, err
		}
		principal, err := access.AuthenticateSession(ctx, request.Token)
		return contracts.AccessAuthenticateSessionResponse{Principal: principal}, err
	}); err != nil {
		return err
	}
	return Register(dispatcher, contracts.AccessRequireGroupOperation(), func(ctx context.Context, request contracts.AccessRequireGroupRequest) (contracts.AccessRequireGroupResponse, error) {
		_, moduleGroups, err := module(ctx)
		if err != nil {
			return contracts.AccessRequireGroupResponse{}, err
		}
		for _, group := range request.Groups {
			if _, allowed := moduleGroups[group]; !allowed {
				return contracts.AccessRequireGroupResponse{}, contracts.ErrAccessForbidden
			}
		}
		if err := access.RequireGroupLevel(ctx, request.Subject, request.Groups, request.Level); err != nil {
			return contracts.AccessRequireGroupResponse{}, err
		}
		return contracts.AccessRequireGroupResponse{}, nil
	})
}
