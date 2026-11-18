package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Zubrium/cdn/core/infrastructure/identity"
	"github.com/google/uuid"
)

// This test runs only against an independently created disposable database.
func TestDisposableIdentityLifecycle(t *testing.T) {
	pool := openDisposablePool(t, "TEST_CORE_DATABASE_URL")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := migrateForTest(t, ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := migrateForTest(t, ctx, pool); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
	secret, created, err := Bootstrap(ctx, pool, "")
	if err != nil || !created || secret == "" {
		t.Fatalf("bootstrap: created=%v err=%v", created, err)
	}
	if next, created, err := Bootstrap(ctx, pool, ""); err != nil || created || next != "" {
		t.Fatalf("repeat bootstrap: created=%v err=%v", created, err)
	}
	manager, err := Authenticate(ctx, pool, secret)
	if err != nil || !manager.CanManageAdministrators {
		t.Fatalf("bootstrap authentication: %v", err)
	}
	if _, err := Authenticate(ctx, pool, "invalid"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("invalid credential: %v", err)
	}
	user, first, err := CreatePrincipal(ctx, pool, manager, "engine worker", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Authenticate(ctx, pool, first); err != nil {
		t.Fatal(err)
	}
	var storedHash []byte
	if err := pool.QueryRow(ctx, `SELECT secret_hash FROM access.credentials WHERE user_id=$1 ORDER BY created_at LIMIT 1`, user).Scan(&storedHash); err != nil {
		t.Fatal(err)
	}
	if len(storedHash) != 32 || string(storedHash) == first || string(storedHash) != string(identity.HashSecret(first)) {
		t.Fatal("credential must be stored only as its SHA-256 hash")
	}
	var assignedGroups int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM access.grants WHERE user_id=$1`, user).Scan(&assignedGroups); err != nil {
		t.Fatal(err)
	}
	if assignedGroups != 0 {
		t.Fatalf("new user received implicit group access: %d assignments", assignedGroups)
	}
	creds, err := ListCredentials(ctx, pool, manager, user)
	if err != nil || len(creds) != 1 {
		t.Fatalf("credentials: %d %v", len(creds), err)
	}
	secondID, second, err := IssueCredential(ctx, pool, manager, user, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := RevokeCredential(ctx, pool, manager, creds[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := Authenticate(ctx, pool, first); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("revoked credential: %v", err)
	}
	if _, err := Authenticate(ctx, pool, second); err != nil {
		t.Fatal(err)
	}
	if err := RevokeCredential(ctx, pool, manager, secondID); err != nil {
		t.Fatal(err)
	}
	if err := DeactivatePrincipal(ctx, pool, manager, user); err != nil {
		t.Fatal(err)
	}
	managerID, err := uuid.Parse(manager.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := DeactivatePrincipal(ctx, pool, manager, managerID); err == nil {
		t.Fatal("last manager deactivated")
	}
	recoveryID, recoverySecret, err := RecoverBootstrapCredential(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Authenticate(ctx, pool, recoverySecret); err != nil {
		t.Fatal(err)
	}
	if err := RevokeCredential(ctx, pool, manager, recoveryID); err != nil {
		t.Fatal(err)
	}
	creds, err = ListCredentials(ctx, pool, manager, managerID)
	if err != nil {
		t.Fatal(err)
	}
	for _, cred := range creds {
		if cred.RevokedAt == nil && RevokeCredential(ctx, pool, manager, cred.ID) == nil {
			t.Fatal("last manager credential revoked")
		}
	}
}
