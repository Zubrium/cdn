package auth

import (
	"strings"
	"testing"
)

func TestSecretsAreRandomAndHashed(t *testing.T) {
	one, err := NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	two, err := NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	if one == two {
		t.Fatal("secrets are not random")
	}
	if !strings.HasPrefix(one, tokenPrefix) {
		t.Fatalf("unexpected prefix: %s", one)
	}
	if HashHex(one) == HashHex(two) {
		t.Fatal("hashes collide")
	}
	if strings.Contains(HashHex(one), one) {
		t.Fatal("hash contains plaintext secret")
	}
}

func TestRolePolicyHelpers(t *testing.T) {
	if !IsStaff(RoleAdmin) || !IsStaff(RoleModerator) || IsStaff(RoleUser) {
		t.Fatal("staff role policy is wrong")
	}
	if !IsAdmin(RoleAdmin) || IsAdmin(RoleModerator) {
		t.Fatal("admin role policy is wrong")
	}
	if _, ok := CleanName(strings.Repeat("x", 121)); ok {
		t.Fatal("long names must be rejected")
	}
}
