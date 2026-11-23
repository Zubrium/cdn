package composition

import (
	"context"

	"github.com/Zubrium/cdn/pkg/contracts"
)

// moduleAccess is an engine-facing identity/access port implemented over the
// caller module's DataBus dispatcher.
type moduleAccess struct{ client contracts.CoreAccess }

func (access moduleAccess) Authenticate(ctx context.Context, credential string) (contracts.AuthorizationSubject, contracts.Principal, error) {
	principal, err := access.client.Authenticate(ctx, credential)
	return contracts.AuthorizationSubject{Credential: credential}, principal, err
}

func (access moduleAccess) AuthenticateSession(ctx context.Context, token string) (contracts.AuthorizationSubject, contracts.Principal, error) {
	principal, err := access.client.AuthenticateSession(ctx, token)
	return contracts.AuthorizationSubject{SessionToken: token}, principal, err
}

func (access moduleAccess) RequireGroupLevel(ctx context.Context, subject contracts.AuthorizationSubject, groups []string, level contracts.AccessLevel) error {
	return access.client.RequireGroupLevel(ctx, subject, groups, level)
}

type contentFileStat struct{ dispatcher contracts.Dispatcher }

func (files contentFileStat) StatFile(ctx context.Context, subject contracts.AuthorizationSubject, fileID string) (contracts.File, error) {
	return contracts.CallFileStatForSubject(ctx, files.dispatcher, subject, fileID)
}
