package postgres

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestDisposableBootstrapIsSerializedAcrossConnections(t *testing.T) {
	pool := openDisposablePool(t, "TEST_CORE_BOOTSTRAP_DATABASE_URL")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := migrateForTest(t, ctx, pool); err != nil {
		t.Fatal(err)
	}
	const callers = 8
	start := make(chan struct{})
	type result struct {
		secret  string
		created bool
		err     error
	}
	results := make(chan result, callers)
	var group sync.WaitGroup
	for i := 0; i < callers; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			secret, created, err := Bootstrap(ctx, pool, "")
			results <- result{secret: secret, created: created, err: err}
		}()
	}
	close(start)
	group.Wait()
	close(results)
	createdCount := 0
	returnedSecret := ""
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.created {
			createdCount++
			returnedSecret = result.secret
		} else if result.secret != "" {
			t.Fatal("a caller that did not create bootstrap received a secret")
		}
	}
	if createdCount != 1 || returnedSecret == "" {
		t.Fatalf("bootstrap creators=%d secret=%q", createdCount, returnedSecret)
	}
	if _, err := Authenticate(ctx, pool, returnedSecret); err != nil {
		t.Fatalf("winning bootstrap secret is unusable: %v", err)
	}
	var principals, credentials int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM access.users`).Scan(&principals); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM access.credentials`).Scan(&credentials); err != nil {
		t.Fatal(err)
	}
	if principals != 1 || credentials != 1 {
		t.Fatalf("concurrent bootstrap created %d users and %d credentials", principals, credentials)
	}
}

func TestDisposableLastManagerDeactivationIsSerialized(t *testing.T) {
	pool := openDisposablePool(t, "TEST_CORE_MANAGER_RACE_DATABASE_URL")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := migrateForTest(t, ctx, pool); err != nil {
		t.Fatal(err)
	}
	secret, created, err := Bootstrap(ctx, pool, "bootstrap-secret-that-is-longer-than-32-chars")
	if err != nil || !created {
		t.Fatalf("bootstrap: created=%t err=%v", created, err)
	}
	manager, err := Authenticate(ctx, pool, secret)
	if err != nil {
		t.Fatal(err)
	}
	secondID, _, err := CreatePrincipal(ctx, pool, manager, "second manager", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE access.users SET can_manage_administrators=true WHERE id=$1`, secondID); err != nil {
		t.Fatal(err)
	}
	bootstrapID, err := uuid.Parse(manager.ID)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	type result struct {
		id  string
		err error
	}
	results := make(chan result, 2)
	var group sync.WaitGroup
	for _, id := range []uuid.UUID{bootstrapID, secondID} {
		group.Add(1)
		go func(id uuid.UUID) {
			defer group.Done()
			<-start
			results <- result{id: id.String(), err: DeactivatePrincipal(ctx, pool, manager, id)}
		}(id)
	}
	close(start)
	group.Wait()
	close(results)
	succeeded, refused := 0, 0
	for result := range results {
		if result.err == nil {
			succeeded++
		} else {
			refused++
		}
	}
	if succeeded != 1 || refused != 1 {
		t.Fatalf("concurrent deactivation: succeeded=%d refused=%d", succeeded, refused)
	}
	var managers int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM access.users p WHERE p.active AND p.can_manage_administrators AND EXISTS (SELECT 1 FROM access.credentials c WHERE c.user_id=p.id AND c.revoked_at IS NULL AND (c.expires_at IS NULL OR c.expires_at>now()))`).Scan(&managers); err != nil {
		t.Fatal(err)
	}
	if managers != 1 {
		t.Fatalf("concurrent deactivation left %d active managers", managers)
	}
}

func TestDisposableLastManagerCredentialRevocationIsSerialized(t *testing.T) {
	pool := openDisposablePool(t, "TEST_CORE_CREDENTIAL_RACE_DATABASE_URL")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := migrateForTest(t, ctx, pool); err != nil {
		t.Fatal(err)
	}
	secret, created, err := Bootstrap(ctx, pool, "bootstrap-secret-that-is-longer-than-32-chars")
	if err != nil || !created {
		t.Fatalf("bootstrap: created=%t err=%v", created, err)
	}
	manager, err := Authenticate(ctx, pool, secret)
	if err != nil {
		t.Fatal(err)
	}
	secondID, _, err := CreatePrincipal(ctx, pool, manager, "second manager", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE access.users SET can_manage_administrators=true WHERE id=$1`, secondID); err != nil {
		t.Fatal(err)
	}
	firstCredentials, err := ListCredentials(ctx, pool, manager, mustParsePrincipal(t, manager.ID))
	if err != nil || len(firstCredentials) != 1 {
		t.Fatalf("bootstrap credentials: %d %v", len(firstCredentials), err)
	}
	secondCredentials, err := ListCredentials(ctx, pool, manager, secondID)
	if err != nil || len(secondCredentials) != 1 {
		t.Fatalf("second manager credentials: %d %v", len(secondCredentials), err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var group sync.WaitGroup
	for _, id := range []uuid.UUID{firstCredentials[0].ID, secondCredentials[0].ID} {
		group.Add(1)
		go func(id uuid.UUID) {
			defer group.Done()
			<-start
			results <- RevokeCredential(ctx, pool, manager, id)
		}(id)
	}
	close(start)
	group.Wait()
	close(results)
	succeeded, refused := 0, 0
	for err := range results {
		if err == nil {
			succeeded++
		} else {
			refused++
		}
	}
	if succeeded != 1 || refused != 1 {
		t.Fatalf("concurrent credential revocation: succeeded=%d refused=%d", succeeded, refused)
	}
	var managers int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM access.users p WHERE p.active AND p.can_manage_administrators AND EXISTS (SELECT 1 FROM access.credentials c WHERE c.user_id=p.id AND c.revoked_at IS NULL AND (c.expires_at IS NULL OR c.expires_at>now()))`).Scan(&managers); err != nil {
		t.Fatal(err)
	}
	if managers != 1 {
		t.Fatalf("concurrent credential revocation left %d active managers", managers)
	}
}

func mustParsePrincipal(t *testing.T, value string) uuid.UUID {
	t.Helper()
	id, err := uuid.Parse(value)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
