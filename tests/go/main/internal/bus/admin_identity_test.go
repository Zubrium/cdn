package bus_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Zubrium/cdn/main/internal/bus"
	"github.com/Zubrium/cdn/pkg/contracts"
)

type adminIdentityStub struct {
	createdSubject contracts.AuthorizationSubject
	createdInput   contracts.CreatePrincipalInput
	createdCalls   int
	sessionCalls   int
}

func (stub *adminIdentityStub) CreatePrincipal(_ context.Context, subject contracts.AuthorizationSubject, input contracts.CreatePrincipalInput) (contracts.AdminCreatePrincipalResponse, error) {
	stub.createdCalls++
	stub.createdSubject = subject
	stub.createdInput = input
	return contracts.AdminCreatePrincipalResponse{ID: "principal-1", Name: input.Name, Credential: "one-time-credential"}, nil
}

func (*adminIdentityStub) ListPrincipals(context.Context, contracts.AuthorizationSubject) ([]contracts.Principal, error) {
	return []contracts.Principal{{ID: "principal-1", Active: true}}, nil
}

func (*adminIdentityStub) SetPrincipalManagement(context.Context, contracts.AuthorizationSubject, string, bool) error {
	return nil
}

func (*adminIdentityStub) DeactivatePrincipal(context.Context, contracts.AuthorizationSubject, string) error {
	return nil
}

func (*adminIdentityStub) IssueCredential(context.Context, contracts.AuthorizationSubject, string, *time.Time) (contracts.AdminIssueCredentialResponse, error) {
	return contracts.AdminIssueCredentialResponse{ID: "credential-1", Credential: "one-time-credential"}, nil
}

func (*adminIdentityStub) ListCredentials(context.Context, contracts.AuthorizationSubject, string) ([]contracts.Credential, error) {
	return []contracts.Credential{{ID: "credential-1", PrincipalID: "principal-1"}}, nil
}

func (*adminIdentityStub) RevokeCredential(context.Context, contracts.AuthorizationSubject, string) error {
	return nil
}

func (*adminIdentityStub) SetGroupAccess(context.Context, contracts.AuthorizationSubject, string, string, contracts.AccessLevel) error {
	return nil
}

func (*adminIdentityStub) GroupAccess(context.Context, contracts.AuthorizationSubject, string, string) (contracts.AccessLevel, error) {
	return contracts.AccessLevelWrite, nil
}

func (*adminIdentityStub) ListAccessGroups(context.Context, contracts.AuthorizationSubject) ([]contracts.AccessGroup, error) {
	return []contracts.AccessGroup{{Key: "features", Level: contracts.AccessLevelWrite}}, nil
}

func (stub *adminIdentityStub) CreateSession(_ context.Context, credential string) (contracts.CreatedSession, error) {
	stub.sessionCalls++
	return contracts.CreatedSession{Token: "session-token", Principal: contracts.Principal{ID: "principal-1", Active: true}}, nil
}

func (*adminIdentityStub) RevokeSession(context.Context, string) error { return nil }

func TestAdminIdentityBusKeepsSubjectAndModuleScope(t *testing.T) {
	dispatcher, err := bus.NewAllowlisted(bus.ConcurrencyLimits{MaxInFlight: 4}, bus.AdminIdentityOperationKeys()...)
	if err != nil {
		t.Fatal(err)
	}
	backend := &adminIdentityStub{}
	if err := bus.RegisterAdminIdentity(dispatcher, backend); err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Seal(); err != nil {
		t.Fatal(err)
	}
	client, err := contracts.NewAdminIdentityClient(bus.NewModuleDispatcher(dispatcher, "admin.identity"))
	if err != nil {
		t.Fatal(err)
	}
	subject := contracts.AuthorizationSubject{SessionToken: "admin-session"}
	input := contracts.CreatePrincipalInput{Name: "operator", CanManageAdministrators: true, GroupAccess: map[string]contracts.AccessLevel{"features": contracts.AccessLevelWrite}}
	created, err := client.CreatePrincipal(context.Background(), subject, input)
	if err != nil || created.ID != "principal-1" || created.Credential != "one-time-credential" {
		t.Fatalf("CreatePrincipal() = %#v, %v", created, err)
	}
	if backend.createdCalls != 1 || backend.createdSubject != subject || backend.createdInput.Name != "operator" || backend.createdInput.GroupAccess["features"] != contracts.AccessLevelWrite {
		t.Fatalf("backend received unexpected request: %#v, %#v", backend.createdSubject, backend.createdInput)
	}
	session, err := client.CreateSession(context.Background(), "credential")
	if err != nil || session.Token != "session-token" || backend.sessionCalls != 1 {
		t.Fatalf("CreateSession() = %#v, %v; calls = %d", session, err, backend.sessionCalls)
	}

	wrongModule, err := contracts.NewAdminIdentityClient(bus.NewModuleDispatcher(dispatcher, "engine.website"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wrongModule.CreatePrincipal(context.Background(), subject, input); !errors.Is(err, bus.ErrOperationNotAllowedForModule) {
		t.Fatalf("CreatePrincipal() from wrong module error = %v, want module allowlist rejection", err)
	}
	if backend.createdCalls != 1 {
		t.Fatalf("backend calls after rejected dispatch = %d, want 1", backend.createdCalls)
	}
}
