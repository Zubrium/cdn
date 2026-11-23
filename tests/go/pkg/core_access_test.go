package pkg

import (
	"testing"

	"github.com/Zubrium/cdn/pkg/contracts"
)

func TestAuthorizationSubjectMustHaveExactlyOneCredential(t *testing.T) {
	valid := []contracts.AuthorizationSubject{
		{Credential: "service-credential"},
		{SessionToken: "opaque-session-token"},
	}
	for _, subject := range valid {
		if err := subject.Validate(); err != nil {
			t.Errorf("valid subject rejected: %v", err)
		}
	}
	invalid := []contracts.AuthorizationSubject{{}, {Credential: "credential", SessionToken: "token"}}
	for _, subject := range invalid {
		if err := subject.Validate(); err == nil {
			t.Errorf("invalid subject accepted: %#v", subject)
		}
	}
}

func TestCoreAccessOperationsHaveStableStaticKeys(t *testing.T) {
	operations := []contracts.Key{
		contracts.AccessAuthenticateOperation().Key,
		contracts.AccessAuthenticateSessionOperation().Key,
		contracts.AccessRequireGroupOperation().Key,
	}
	want := []string{
		"core.access.authenticate",
		"core.access.authenticate-session",
		"core.access.require-group",
	}
	for index, operation := range operations {
		if operation.ID != want[index] || operation.Version != 1 {
			t.Errorf("operation[%d] = %#v, want %s/v1", index, operation, want[index])
		}
	}
}
