package composition

import (
	"context"
	"errors"
	"time"

	platform "github.com/Zubrium/cdn/core/presentation"
	"github.com/Zubrium/cdn/pkg/contracts"
	adminapi "github.com/Zubrium/cdn/presentation/admin/base/admin/main"
	"github.com/google/uuid"
)

// adminIdentityAdapter maps Core identity administration and credential-bound
// sessions onto the neutral Admin HTTP ports. It is supplied only by the
// composition root; the Admin module never imports Core.
type adminIdentityAdapter struct{ runtime *platform.Runtime }

func (a adminIdentityAdapter) session(ctx context.Context, subject contracts.AuthorizationSubject) (platform.Session, error) {
	return resolveCoreSession(ctx, a.runtime, subject)
}

func (a adminIdentityAdapter) Authenticate(ctx context.Context, credential string) (contracts.AuthorizationSubject, error) {
	if _, _, err := a.runtime.Identity().Authenticate(ctx, credential); err != nil {
		return contracts.AuthorizationSubject{}, mapCoreIdentityError(err)
	}
	return contracts.AuthorizationSubject{Credential: credential}, nil
}

func (a adminIdentityAdapter) CreatePrincipal(ctx context.Context, subject contracts.AuthorizationSubject, input adminapi.CreatePrincipalRequest) (adminapi.PrincipalCreated, error) {
	session, err := a.session(ctx, subject)
	if err != nil {
		return adminapi.PrincipalCreated{}, mapCoreIdentityError(err)
	}
	groups := make(map[string]platform.AccessLevel, len(input.GroupAccess))
	for group, level := range input.GroupAccess {
		groups[group] = platform.AccessLevel(level)
	}
	id, credential, err := a.runtime.IdentityAdmin().CreatePrincipalWithAccess(ctx, session, input.Name, input.ExpiresAt, input.CanManageAdministrators, groups)
	if err != nil {
		return adminapi.PrincipalCreated{}, mapCoreIdentityError(err)
	}
	return adminapi.PrincipalCreated{ID: id, Name: input.Name, Credential: credential}, nil
}

func (a adminIdentityAdapter) ListPrincipals(ctx context.Context, subject contracts.AuthorizationSubject) ([]contracts.Principal, error) {
	session, err := a.session(ctx, subject)
	if err != nil {
		return nil, mapCoreIdentityError(err)
	}
	principals, err := a.runtime.IdentityAdmin().ListPrincipals(ctx, session)
	if err != nil {
		return nil, mapCoreIdentityError(err)
	}
	result := make([]contracts.Principal, len(principals))
	for i, principal := range principals {
		result[i] = contractPrincipal(principal)
	}
	return result, nil
}

func (a adminIdentityAdapter) SetPrincipalManagement(ctx context.Context, subject contracts.AuthorizationSubject, id string, enabled bool) error {
	session, err := a.session(ctx, subject)
	if err != nil {
		return mapCoreIdentityError(err)
	}
	return mapCoreIdentityError(a.runtime.IdentityAdmin().SetPrincipalManagement(ctx, session, id, enabled))
}

func (a adminIdentityAdapter) DeactivatePrincipal(ctx context.Context, subject contracts.AuthorizationSubject, id string) error {
	session, err := a.session(ctx, subject)
	if err != nil {
		return mapCoreIdentityError(err)
	}
	return mapCoreIdentityError(a.runtime.IdentityAdmin().DeactivatePrincipal(ctx, session, id))
}

func (a adminIdentityAdapter) IssueCredential(ctx context.Context, subject contracts.AuthorizationSubject, principalID string, expires *time.Time) (string, string, error) {
	session, err := a.session(ctx, subject)
	if err != nil {
		return "", "", mapCoreIdentityError(err)
	}
	id, secret, err := a.runtime.IdentityAdmin().IssueCredential(ctx, session, principalID, expires)
	return id, secret, mapCoreIdentityError(err)
}

func (a adminIdentityAdapter) ListCredentials(ctx context.Context, subject contracts.AuthorizationSubject, principalID string) ([]contracts.Credential, error) {
	session, err := a.session(ctx, subject)
	if err != nil {
		return nil, mapCoreIdentityError(err)
	}
	credentials, err := a.runtime.IdentityAdmin().ListCredentials(ctx, session, principalID)
	if err != nil {
		return nil, mapCoreIdentityError(err)
	}
	result := make([]contracts.Credential, len(credentials))
	for i, credential := range credentials {
		result[i] = contracts.Credential{ID: credential.ID, PrincipalID: credential.PrincipalID,
			CreatedAt: credential.CreatedAt, ExpiresAt: credential.ExpiresAt, RevokedAt: credential.RevokedAt}
	}
	return result, nil
}

func (a adminIdentityAdapter) RevokeCredential(ctx context.Context, subject contracts.AuthorizationSubject, credentialID string) error {
	session, err := a.session(ctx, subject)
	if err != nil {
		return mapCoreIdentityError(err)
	}
	return mapCoreIdentityError(a.runtime.IdentityAdmin().RevokeCredential(ctx, session, credentialID))
}

func (a adminIdentityAdapter) SetGroupAccess(ctx context.Context, subject contracts.AuthorizationSubject, principalID, group string, level contracts.AccessLevel) error {
	session, err := a.session(ctx, subject)
	if err != nil {
		return mapCoreIdentityError(err)
	}
	return mapCoreIdentityError(a.runtime.IdentityAdmin().SetGroupAccess(ctx, session, principalID, group, platform.AccessLevel(level)))
}

func (a adminIdentityAdapter) GroupAccess(ctx context.Context, subject contracts.AuthorizationSubject, principalID, group string) (contracts.AccessLevel, error) {
	session, err := a.session(ctx, subject)
	if err != nil {
		return contracts.AccessLevelDenied, mapCoreIdentityError(err)
	}
	level, err := a.runtime.IdentityAdmin().GroupAccess(ctx, session, principalID, group)
	return contracts.AccessLevel(level), mapCoreIdentityError(err)
}

func (a adminIdentityAdapter) ListAccessGroups(ctx context.Context, subject contracts.AuthorizationSubject) ([]contracts.AccessGroup, error) {
	session, err := a.session(ctx, subject)
	if err != nil {
		return nil, mapCoreIdentityError(err)
	}
	groups, err := a.runtime.IdentityAdmin().ListAccessGroups(ctx, session)
	if err != nil {
		return nil, mapCoreIdentityError(err)
	}
	result := make([]contracts.AccessGroup, len(groups))
	for i, group := range groups {
		result[i] = contracts.AccessGroup{Key: group.Key, Active: group.Active, Level: contracts.AccessLevel(group.Level)}
	}
	return result, nil
}

func (a adminIdentityAdapter) CreateSession(ctx context.Context, credential string) (contracts.CreatedSession, error) {
	created, err := a.runtime.Sessions().CreateSession(ctx, credential)
	if err != nil {
		return contracts.CreatedSession{}, mapCoreIdentityError(err)
	}
	return contracts.CreatedSession{Token: created.Token, Principal: contractPrincipal(created.Principal),
		IdleExpiresAt: created.IdleExpiresAt, AbsoluteExpiresAt: created.AbsoluteExpiresAt}, nil
}

func (a adminIdentityAdapter) AuthenticateSession(ctx context.Context, token string) (contracts.AuthorizationSubject, contracts.Principal, error) {
	_, principal, err := a.runtime.Sessions().AuthenticateSession(ctx, token)
	if err != nil {
		return contracts.AuthorizationSubject{}, contracts.Principal{}, mapCoreIdentityError(err)
	}
	return contracts.AuthorizationSubject{SessionToken: token}, contractPrincipal(principal), nil
}

func (a adminIdentityAdapter) RevokeSession(ctx context.Context, token string) error {
	return mapCoreIdentityError(a.runtime.Sessions().RevokeSession(ctx, token))
}

// adminGroupAccessResolver exposes the session principal's direct levels in
// the configured static groups by probing Core authorization per request.
type adminGroupAccessResolver struct {
	runtime *platform.Runtime
	groups  []string
}

func (r adminGroupAccessResolver) ResolveGroupAccess(ctx context.Context, token string) (map[string]contracts.AccessLevel, error) {
	if r.runtime == nil || token == "" {
		return nil, contracts.ErrAccessUnavailable
	}
	session, _, err := r.runtime.Sessions().AuthenticateSession(ctx, token)
	if err != nil {
		return nil, err
	}
	access := make(map[string]contracts.AccessLevel)
	for _, group := range r.groups {
		err := r.runtime.GroupAuthorization().RequireGroupLevel(ctx, session, []string{group}, platform.AccessLevelWrite)
		if err == nil {
			access[group] = contracts.AccessLevelWrite
			continue
		}
		if !errors.Is(err, platform.ErrForbidden) {
			return nil, err
		}
		err = r.runtime.GroupAuthorization().RequireGroupLevel(ctx, session, []string{group}, platform.AccessLevelRead)
		if err == nil {
			access[group] = contracts.AccessLevelRead
		} else if !errors.Is(err, platform.ErrForbidden) {
			return nil, err
		}
	}
	return access, nil
}

// adminCoordinatorAuthorization adapts Core's coordinator-scoped authorization
// capability to the neutral Admin coordinator port.
type adminCoordinatorAuthorization struct{ runtime *platform.Runtime }

func (a adminCoordinatorAuthorization) Authenticate(ctx context.Context, credential string) (contracts.AuthorizationSubject, error) {
	if _, err := a.runtime.CoordinatorAuthorization().Authenticate(ctx, credential); err != nil {
		return contracts.AuthorizationSubject{}, mapCoreAccessError(err)
	}
	return contracts.AuthorizationSubject{Credential: credential}, nil
}

func (a adminCoordinatorAuthorization) RequireGroupLevel(ctx context.Context, subject contracts.AuthorizationSubject, group string, level contracts.AccessLevel) error {
	session, err := resolveCoreSession(ctx, a.runtime, subject)
	if err != nil {
		return mapCoreAccessError(err)
	}
	return mapCoreAccessError(a.runtime.CoordinatorAuthorization().RequireGroupLevel(ctx, session, group, platform.AccessLevel(level)))
}

// adminWorkflowControl converts Core's workflow-control projections into the
// neutral Admin DTOs used by the bus and the Admin coordinator HTTP adapter.
type adminWorkflowControl struct{ core *coreComponent }

func (c adminWorkflowControl) control() (*platform.WorkflowControl, error) {
	if c.core == nil || c.core.runtime == nil {
		return nil, contracts.ErrWorkflowUnavailable
	}
	control, err := c.core.runtime.NewWorkflowControl()
	if err != nil {
		return nil, mapCoreWorkflowControlError(err)
	}
	return control, nil
}

func (c adminWorkflowControl) List(ctx context.Context, filter contracts.AdminWorkflowListFilter) (contracts.AdminWorkflowPage, error) {
	control, err := c.control()
	if err != nil {
		return contracts.AdminWorkflowPage{}, err
	}
	coreFilter := platform.WorkflowListFilter{State: filter.State, Limit: filter.Limit}
	if filter.Before != nil {
		coreFilter.Before = &platform.WorkflowCursor{CreatedAt: filter.Before.CreatedAt, ID: filter.Before.ID}
	}
	page, err := control.List(ctx, coreFilter)
	if err != nil {
		return contracts.AdminWorkflowPage{}, mapCoreWorkflowControlError(err)
	}
	result := contracts.AdminWorkflowPage{Items: make([]contracts.AdminWorkflowSummary, len(page.Items))}
	for i, item := range page.Items {
		result.Items[i] = contracts.AdminWorkflowSummary{ID: item.ID, OwnerClassID: item.OwnerClassID, State: item.State,
			TaskCount: item.TaskCount, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt}
	}
	if page.Next != nil {
		result.Next = &contracts.AdminWorkflowCursor{CreatedAt: page.Next.CreatedAt, ID: page.Next.ID}
	}
	return result, nil
}

func (c adminWorkflowControl) Get(ctx context.Context, id uuid.UUID, filter contracts.AdminWorkflowDetailFilter) (contracts.AdminWorkflowDetail, error) {
	control, err := c.control()
	if err != nil {
		return contracts.AdminWorkflowDetail{}, err
	}
	coreFilter := platform.WorkflowDetailFilter{Limit: filter.Limit}
	if filter.Before != nil {
		coreFilter.Before = &platform.TaskCursor{CreatedAt: filter.Before.CreatedAt, ID: filter.Before.ID}
	}
	detail, err := control.Get(ctx, id, coreFilter)
	if err != nil {
		return contracts.AdminWorkflowDetail{}, mapCoreWorkflowControlError(err)
	}
	result := contracts.AdminWorkflowDetail{
		Workflow: contracts.AdminWorkflow{ID: detail.Workflow.ID, OwnerClassID: detail.Workflow.OwnerClassID,
			State: detail.Workflow.State, RootTaskID: detail.Workflow.RootTaskID, Result: detail.Workflow.Result,
			CreatedAt: detail.Workflow.CreatedAt, UpdatedAt: detail.Workflow.UpdatedAt},
		TaskPage: contracts.AdminTaskPage{Items: make([]contracts.AdminTaskSummary, len(detail.TaskPage.Items))},
	}
	for i, item := range detail.TaskPage.Items {
		summary := contracts.AdminTaskSummary{ID: item.ID, WorkflowID: item.WorkflowID, Kind: contracts.AdminTaskKind(item.Kind),
			ParentTaskID: item.ParentTaskID, DependsOn: item.DependsOn, EmissionKey: item.EmissionKey, State: item.State,
			Attempts: item.Attempts, MaxAttempts: item.MaxAttempts, AvailableAt: item.AvailableAt, LeaseUntil: item.LeaseUntil,
			ErrorCode: item.ErrorCode, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt}
		if item.Operation != nil {
			summary.Operation = &contracts.AdminOperationRef{ClassID: item.Operation.ClassID, ID: item.Operation.ID, Version: item.Operation.Version}
		}
		result.TaskPage.Items[i] = summary
	}
	if detail.TaskPage.Next != nil {
		result.TaskPage.Next = &contracts.AdminTaskCursor{CreatedAt: detail.TaskPage.Next.CreatedAt, ID: detail.TaskPage.Next.ID}
	}
	return result, nil
}

func (c adminWorkflowControl) RetryTask(ctx context.Context, workflowID, taskID uuid.UUID) error {
	control, err := c.control()
	if err != nil {
		return err
	}
	return mapCoreWorkflowControlError(control.RetryTask(ctx, workflowID, taskID))
}

func mapCoreIdentityError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, platform.ErrManagerInvariant):
		return errors.Join(contracts.ErrAccessManagerInvariant, err)
	case errors.Is(err, platform.ErrModuleDataConflict):
		return errors.Join(contracts.ErrRecordConflict, err)
	case errors.Is(err, platform.ErrFileSetConflict):
		return errors.Join(contracts.ErrFileSetConflict, err)
	default:
		return mapCoreAccessError(err)
	}
}

func mapCoreWorkflowControlError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, platform.ErrCoordinatorInputInvalid):
		return errors.Join(contracts.ErrWorkflowInputInvalid, err)
	case errors.Is(err, platform.ErrCoordinatorConflict):
		return errors.Join(contracts.ErrWorkflowConflict, err)
	case errors.Is(err, platform.ErrCoordinatorStale):
		return errors.Join(contracts.ErrWorkflowStale, err)
	case errors.Is(err, platform.ErrTaskMissing):
		return errors.Join(contracts.ErrWorkflowTaskMissing, err)
	case errors.Is(err, platform.ErrCoordinatorNotReady):
		return errors.Join(contracts.ErrWorkflowUnavailable, err)
	default:
		return err
	}
}