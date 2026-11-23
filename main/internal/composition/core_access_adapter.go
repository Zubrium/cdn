package composition

import (
	"context"
	"errors"

	platform "github.com/Zubrium/cdn/core/presentation"
	"github.com/Zubrium/cdn/pkg/contracts"
)

type coreRuntimeAccess struct{ runtime *platform.Runtime }

func (adapter coreRuntimeAccess) Authenticate(ctx context.Context, credential string) (contracts.Principal, error) {
	if adapter.runtime == nil {
		return contracts.Principal{}, contracts.ErrAccessUnavailable
	}
	_, principal, err := adapter.runtime.Identity().Authenticate(ctx, credential)
	return contractPrincipal(principal), mapCoreAccessError(err)
}

func (adapter coreRuntimeAccess) AuthenticateSession(ctx context.Context, token string) (contracts.Principal, error) {
	if adapter.runtime == nil {
		return contracts.Principal{}, contracts.ErrAccessUnavailable
	}
	_, principal, err := adapter.runtime.Sessions().AuthenticateSession(ctx, token)
	return contractPrincipal(principal), mapCoreAccessError(err)
}

func (adapter coreRuntimeAccess) RequireGroupLevel(ctx context.Context, subject contracts.AuthorizationSubject, groups []string, level contracts.AccessLevel) error {
	if adapter.runtime == nil || subject.Validate() != nil || len(groups) == 0 ||
		(level != contracts.AccessLevelRead && level != contracts.AccessLevelWrite) {
		return contracts.ErrAccessInvalid
	}
	var session platform.Session
	var err error
	if subject.Credential != "" {
		session, _, err = adapter.runtime.Identity().Authenticate(ctx, subject.Credential)
	} else {
		session, _, err = adapter.runtime.Sessions().AuthenticateSession(ctx, subject.SessionToken)
	}
	if err != nil {
		return mapCoreAccessError(err)
	}
	coreLevel := platform.AccessLevelRead
	if level == contracts.AccessLevelWrite {
		coreLevel = platform.AccessLevelWrite
	}
	return mapCoreAccessError(adapter.runtime.GroupAuthorization().RequireGroupLevel(ctx, session, groups, coreLevel))
}

func contractPrincipal(principal platform.Principal) contracts.Principal {
	return contracts.Principal{ID: principal.ID, Name: principal.Name, Active: principal.Active,
		CanManageAdministrators: principal.CanManageAdministrators}
}

func mapCoreAccessError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, platform.ErrUnauthorized):
		return errors.Join(contracts.ErrAccessUnauthorized, err)
	case errors.Is(err, platform.ErrForbidden):
		return errors.Join(contracts.ErrAccessForbidden, err)
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	default:
		return errors.Join(contracts.ErrAccessUnavailable, err)
	}
}

var _ contracts.CoreAccess = coreRuntimeAccess{}
