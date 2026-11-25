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

// adminIdentityAdapter exposes Admin's neutral identity ports through the
// module-scoped DataBus. The Admin module never receives a Core runtime or a
// Core session.
type adminIdentityAdapter struct {
	access   contracts.CoreAccess
	identity *contracts.AdminIdentityClient
}

func (a adminIdentityAdapter) Authenticate(ctx context.Context, credential string) (contracts.AuthorizationSubject, error) {
	principal, err := a.access.Authenticate(ctx, credential)
	if err != nil {
		return contracts.AuthorizationSubject{}, mapCoreIdentityError(err)
	}
	_ = principal
	return contracts.AuthorizationSubject{Credential: credential}, nil
}

func (a adminIdentityAdapter) CreatePrincipal(ctx context.Context, subject contracts.AuthorizationSubject, input adminapi.CreatePrincipalRequest) (adminapi.PrincipalCreated, error) {
	created, err := a.identity.CreatePrincipal(ctx, subject, contracts.CreatePrincipalInput{
		Name:                    input.Name,
		ExpiresAt:               input.ExpiresAt,
		CanManageAdministrators: input.CanManageAdministrators,
		GroupAccess:             input.GroupAccess,
	})
	if err != nil {
		return adminapi.PrincipalCreated{}, mapCoreIdentityError(err)
	}
	return adminapi.PrincipalCreated{ID: created.ID, Name: created.Name, Credential: created.Credential}, nil
}

func (a adminIdentityAdapter) ListPrincipals(ctx context.Context, subject contracts.AuthorizationSubject) ([]contracts.Principal, error) {
	principals, err := a.identity.ListPrincipals(ctx, subject)
	if err != nil {
		return nil, mapCoreIdentityError(err)
	}
	return principals, nil
}

func (a adminIdentityAdapter) SetPrincipalManagement(ctx context.Context, subject contracts.AuthorizationSubject, id string, enabled bool) error {
	return mapCoreIdentityError(a.identity.SetPrincipalManagement(ctx, subject, id, enabled))
}

func (a adminIdentityAdapter) DeactivatePrincipal(ctx context.Context, subject contracts.AuthorizationSubject, id string) error {
	return mapCoreIdentityError(a.identity.DeactivatePrincipal(ctx, subject, id))
}

func (a adminIdentityAdapter) IssueCredential(ctx context.Context, subject contracts.AuthorizationSubject, principalID string, expires *time.Time) (string, string, error) {
	issued, err := a.identity.IssueCredential(ctx, subject, principalID, expires)
	if err != nil {
		return "", "", mapCoreIdentityError(err)
	}
	return issued.ID, issued.Credential, nil
}

func (a adminIdentityAdapter) ListCredentials(ctx context.Context, subject contracts.AuthorizationSubject, principalID string) ([]contracts.Credential, error) {
	credentials, err := a.identity.ListCredentials(ctx, subject, principalID)
	if err != nil {
		return nil, mapCoreIdentityError(err)
	}
	return credentials, nil
}

func (a adminIdentityAdapter) RevokeCredential(ctx context.Context, subject contracts.AuthorizationSubject, credentialID string) error {
	return mapCoreIdentityError(a.identity.RevokeCredential(ctx, subject, credentialID))
}

func (a adminIdentityAdapter) SetGroupAccess(ctx context.Context, subject contracts.AuthorizationSubject, principalID, group string, level contracts.AccessLevel) error {
	return mapCoreIdentityError(a.identity.SetGroupAccess(ctx, subject, principalID, group, level))
}

func (a adminIdentityAdapter) GroupAccess(ctx context.Context, subject contracts.AuthorizationSubject, principalID, group string) (contracts.AccessLevel, error) {
	level, err := a.identity.GroupAccess(ctx, subject, principalID, group)
	return level, mapCoreIdentityError(err)
}

func (a adminIdentityAdapter) ListAccessGroups(ctx context.Context, subject contracts.AuthorizationSubject) ([]contracts.AccessGroup, error) {
	groups, err := a.identity.ListAccessGroups(ctx, subject)
	if err != nil {
		return nil, mapCoreIdentityError(err)
	}
	return groups, nil
}

func (a adminIdentityAdapter) CreateSession(ctx context.Context, credential string) (contracts.CreatedSession, error) {
	created, err := a.identity.CreateSession(ctx, credential)
	if err != nil {
		return contracts.CreatedSession{}, mapCoreIdentityError(err)
	}
	return created, nil
}

func (a adminIdentityAdapter) AuthenticateSession(ctx context.Context, token string) (contracts.AuthorizationSubject, contracts.Principal, error) {
	principal, err := a.access.AuthenticateSession(ctx, token)
	if err != nil {
		return contracts.AuthorizationSubject{}, contracts.Principal{}, mapCoreIdentityError(err)
	}
	return contracts.AuthorizationSubject{SessionToken: token}, principal, nil
}

func (a adminIdentityAdapter) RevokeSession(ctx context.Context, token string) error {
	return mapCoreIdentityError(a.identity.RevokeSession(ctx, token))
}

// coreAdminIdentityOperations is the Core-facing implementation registered on
// the DataBus. It resolves the caller's already authenticated subject to a Core
// session inside the composition root; the JSON payload never carries a Core
// capability.
type coreAdminIdentityOperations struct{ core *coreComponent }

func (a coreAdminIdentityOperations) session(ctx context.Context, subject contracts.AuthorizationSubject) (platform.Session, error) {
	if a.core == nil || a.core.runtime == nil {
		return platform.Session{}, contracts.ErrAccessUnavailable
	}
	session, err := resolveCoreSession(ctx, a.core.runtime, subject)
	if err != nil {
		return platform.Session{}, mapCoreIdentityError(err)
	}
	return session, nil
}

func (a coreAdminIdentityOperations) CreatePrincipal(ctx context.Context, subject contracts.AuthorizationSubject, input contracts.CreatePrincipalInput) (contracts.AdminCreatePrincipalResponse, error) {
	session, err := a.session(ctx, subject)
	if err != nil {
		return contracts.AdminCreatePrincipalResponse{}, err
	}
	groups := make(map[string]platform.AccessLevel, len(input.GroupAccess))
	for group, level := range input.GroupAccess {
		groups[group] = platform.AccessLevel(level)
	}
	id, credential, err := a.core.runtime.IdentityAdmin().CreatePrincipalWithAccess(ctx, session, input.Name, input.ExpiresAt, input.CanManageAdministrators, groups)
	if err != nil {
		return contracts.AdminCreatePrincipalResponse{}, mapCoreIdentityError(err)
	}
	return contracts.AdminCreatePrincipalResponse{ID: id, Name: input.Name, Credential: credential}, nil
}

func (a coreAdminIdentityOperations) ListPrincipals(ctx context.Context, subject contracts.AuthorizationSubject) ([]contracts.Principal, error) {
	session, err := a.session(ctx, subject)
	if err != nil {
		return nil, err
	}
	principals, err := a.core.runtime.IdentityAdmin().ListPrincipals(ctx, session)
	if err != nil {
		return nil, mapCoreIdentityError(err)
	}
	result := make([]contracts.Principal, len(principals))
	for i, principal := range principals {
		result[i] = contractPrincipal(principal)
	}
	return result, nil
}

func (a coreAdminIdentityOperations) SetPrincipalManagement(ctx context.Context, subject contracts.AuthorizationSubject, id string, enabled bool) error {
	session, err := a.session(ctx, subject)
	if err != nil {
		return err
	}
	return mapCoreIdentityError(a.core.runtime.IdentityAdmin().SetPrincipalManagement(ctx, session, id, enabled))
}

func (a coreAdminIdentityOperations) DeactivatePrincipal(ctx context.Context, subject contracts.AuthorizationSubject, id string) error {
	session, err := a.session(ctx, subject)
	if err != nil {
		return err
	}
	return mapCoreIdentityError(a.core.runtime.IdentityAdmin().DeactivatePrincipal(ctx, session, id))
}

func (a coreAdminIdentityOperations) IssueCredential(ctx context.Context, subject contracts.AuthorizationSubject, principalID string, expires *time.Time) (contracts.AdminIssueCredentialResponse, error) {
	session, err := a.session(ctx, subject)
	if err != nil {
		return contracts.AdminIssueCredentialResponse{}, err
	}
	id, secret, err := a.core.runtime.IdentityAdmin().IssueCredential(ctx, session, principalID, expires)
	if err != nil {
		return contracts.AdminIssueCredentialResponse{}, mapCoreIdentityError(err)
	}
	return contracts.AdminIssueCredentialResponse{ID: id, Credential: secret}, nil
}

func (a coreAdminIdentityOperations) ListCredentials(ctx context.Context, subject contracts.AuthorizationSubject, principalID string) ([]contracts.Credential, error) {
	session, err := a.session(ctx, subject)
	if err != nil {
		return nil, err
	}
	credentials, err := a.core.runtime.IdentityAdmin().ListCredentials(ctx, session, principalID)
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

func (a coreAdminIdentityOperations) RevokeCredential(ctx context.Context, subject contracts.AuthorizationSubject, credentialID string) error {
	session, err := a.session(ctx, subject)
	if err != nil {
		return err
	}
	return mapCoreIdentityError(a.core.runtime.IdentityAdmin().RevokeCredential(ctx, session, credentialID))
}

func (a coreAdminIdentityOperations) SetGroupAccess(ctx context.Context, subject contracts.AuthorizationSubject, principalID, group string, level contracts.AccessLevel) error {
	session, err := a.session(ctx, subject)
	if err != nil {
		return err
	}
	return mapCoreIdentityError(a.core.runtime.IdentityAdmin().SetGroupAccess(ctx, session, principalID, group, platform.AccessLevel(level)))
}

func (a coreAdminIdentityOperations) GroupAccess(ctx context.Context, subject contracts.AuthorizationSubject, principalID, group string) (contracts.AccessLevel, error) {
	session, err := a.session(ctx, subject)
	if err != nil {
		return contracts.AccessLevelDenied, err
	}
	level, err := a.core.runtime.IdentityAdmin().GroupAccess(ctx, session, principalID, group)
	return contracts.AccessLevel(level), mapCoreIdentityError(err)
}

func (a coreAdminIdentityOperations) ListAccessGroups(ctx context.Context, subject contracts.AuthorizationSubject) ([]contracts.AccessGroup, error) {
	session, err := a.session(ctx, subject)
	if err != nil {
		return nil, err
	}
	groups, err := a.core.runtime.IdentityAdmin().ListAccessGroups(ctx, session)
	if err != nil {
		return nil, mapCoreIdentityError(err)
	}
	result := make([]contracts.AccessGroup, len(groups))
	for i, group := range groups {
		result[i] = contracts.AccessGroup{Key: group.Key, Active: group.Active, Level: contracts.AccessLevel(group.Level)}
	}
	return result, nil
}

func (a coreAdminIdentityOperations) CreateSession(ctx context.Context, credential string) (contracts.CreatedSession, error) {
	created, err := a.core.runtime.Sessions().CreateSession(ctx, credential)
	if err != nil {
		return contracts.CreatedSession{}, mapCoreIdentityError(err)
	}
	return contracts.CreatedSession{Token: created.Token, Principal: contractPrincipal(created.Principal),
		IdleExpiresAt: created.IdleExpiresAt, AbsoluteExpiresAt: created.AbsoluteExpiresAt}, nil
}

func (a coreAdminIdentityOperations) RevokeSession(ctx context.Context, token string) error {
	return mapCoreIdentityError(a.core.runtime.Sessions().RevokeSession(ctx, token))
}

// adminGroupAccessResolver probes the caller's direct levels through the
// module-scoped Core access operations. Group keys come from startup config,
// never from the browser request.
type adminGroupAccessResolver struct {
	access contracts.CoreAccess
	groups []string
}

func (r adminGroupAccessResolver) ResolveGroupAccess(ctx context.Context, token string) (map[string]contracts.AccessLevel, error) {
	if r.access == nil || token == "" {
		return nil, contracts.ErrAccessUnavailable
	}
	subject := contracts.AuthorizationSubject{SessionToken: token}
	if _, err := r.access.AuthenticateSession(ctx, token); err != nil {
		return nil, err
	}
	access := make(map[string]contracts.AccessLevel)
	for _, group := range r.groups {
		err := r.access.RequireGroupLevel(ctx, subject, []string{group}, contracts.AccessLevelWrite)
		if err == nil {
			access[group] = contracts.AccessLevelWrite
			continue
		}
		if !errors.Is(err, contracts.ErrAccessForbidden) {
			return nil, err
		}
		err = r.access.RequireGroupLevel(ctx, subject, []string{group}, contracts.AccessLevelRead)
		if err == nil {
			access[group] = contracts.AccessLevelRead
		} else if !errors.Is(err, contracts.ErrAccessForbidden) {
			return nil, err
		}
	}
	return access, nil
}

// adminCoordinatorAuthorization adapts the neutral Core access client to the
// Admin coordinator authorization port.
type adminCoordinatorAuthorization struct{ access contracts.CoreAccess }

func (a adminCoordinatorAuthorization) Authenticate(ctx context.Context, credential string) (contracts.AuthorizationSubject, error) {
	if _, err := a.access.Authenticate(ctx, credential); err != nil {
		return contracts.AuthorizationSubject{}, mapCoreAccessError(err)
	}
	return contracts.AuthorizationSubject{Credential: credential}, nil
}

func (a adminCoordinatorAuthorization) RequireGroupLevel(ctx context.Context, subject contracts.AuthorizationSubject, group string, level contracts.AccessLevel) error {
	return mapCoreAccessError(a.access.RequireGroupLevel(ctx, subject, []string{group}, level))
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
