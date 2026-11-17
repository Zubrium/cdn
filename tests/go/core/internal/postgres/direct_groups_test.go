package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Zubrium/cdn/core/access"
	"github.com/google/uuid"
)

func TestDisposableDirectGroupAssignmentLifecycle(t *testing.T) {
	pool := openDisposablePool(t, "TEST_CORE_GROUPS_DATABASE_URL")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := migrateForTest(t, ctx, pool); err != nil {
		t.Fatal(err)
	}
	const bootstrapSecret = "ssssssssssssssssssssssssssssssss"
	if _, created, err := Bootstrap(ctx, pool, bootstrapSecret); err != nil || !created {
		t.Fatalf("bootstrap: created=%v err=%v", created, err)
	}
	bootstrap, err := Authenticate(ctx, pool, bootstrapSecret)
	if err != nil || !bootstrap.CanManageAdministrators {
		t.Fatalf("bootstrap must have explicit management permission: %t: %v", bootstrap.CanManageAdministrators, err)
	}
	bootstrapID := mustParsePrincipal(t, bootstrap.ID)
	initial := map[string]access.Level{
		"content": access.LevelWrite, "features": access.LevelWrite, "restricted": access.LevelRead,
	}
	if err := ReconcileAccessGroups(ctx, pool, initial); err != nil {
		t.Fatal(err)
	}

	createdID, createdSecret, err := CreatePrincipalWithAccess(ctx, pool, bootstrap, "explicit group editor", nil, false, map[string]access.Level{
		"content": access.LevelRead,
	})
	if err != nil || createdID == uuid.Nil || createdSecret == "" {
		t.Fatalf("create principal with explicit access: id=%s err=%v", createdID, err)
	}
	if _, err := Authenticate(ctx, pool, createdSecret); err != nil {
		t.Fatalf("created principal credential: %v", err)
	}
	var createdLevel int
	if err := pool.QueryRow(ctx, `SELECT access_level FROM core_group_access WHERE principal_id=$1 AND group_key='content'`, createdID).Scan(&createdLevel); err != nil || createdLevel != int(access.LevelRead) {
		t.Fatalf("explicit access level = %d, err = %v", createdLevel, err)
	}

	failedID, _, err := CreatePrincipalWithAccess(ctx, pool, bootstrap, "rejected partial editor", nil, false, map[string]access.Level{
		"content": access.LevelWrite,
		"restricted": access.LevelWrite,
	})
	if !errors.Is(err, ErrForbidden) || failedID != uuid.Nil {
		t.Fatalf("unauthorized multi-group create = %s, %v; want no principal and forbidden", failedID, err)
	}
	var partialPrincipalCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM core_principals WHERE name='rejected partial editor'`).Scan(&partialPrincipalCount); err != nil || partialPrincipalCount != 0 {
		t.Fatalf("failed creation left %d principal records: %v", partialPrincipalCount, err)
	}

	for group, want := range initial {
		var level int
		if err := pool.QueryRow(ctx, `SELECT access_level FROM core_group_access WHERE principal_id=$1 AND group_key=$2`, bootstrapID, group).Scan(&level); err != nil || level != int(want) {
			t.Fatalf("bootstrap initial level for %s: got %d want %d: %v", group, level, want, err)
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE core_group_access SET access_level=0 WHERE principal_id=$1 AND group_key='features'`, bootstrapID); err != nil {
		t.Fatal(err)
	}
	if err := ReconcileAccessGroups(ctx, pool, map[string]access.Level{"content": access.LevelWrite, "restricted": access.LevelWrite}); err != nil {
		t.Fatal(err)
	}
	var active bool
	if err := pool.QueryRow(ctx, `SELECT active FROM core_access_groups WHERE group_key='features'`).Scan(&active); err != nil || active {
		t.Fatalf("removed group remains active: %t: %v", active, err)
	}
	var retained int
	if err := pool.QueryRow(ctx, `SELECT access_level FROM core_group_access WHERE principal_id=$1 AND group_key='features'`, bootstrapID).Scan(&retained); err != nil || retained != int(access.LevelNone) {
		t.Fatalf("removed group did not retain its saved assignment: level=%d err=%v", retained, err)
	}
	if err := ReconcileAccessGroups(ctx, pool, map[string]access.Level{
		"content": access.LevelWrite, "features": access.LevelWrite, "restricted": access.LevelWrite, "new": access.LevelWrite,
	}); err != nil {
		t.Fatal(err)
	}
	for group, want := range map[string]int{"content": 2, "features": 0, "restricted": 1, "new": 2} {
		var got int
		if err := pool.QueryRow(ctx, `SELECT access_level FROM core_group_access WHERE principal_id=$1 AND group_key=$2`, bootstrapID, group).Scan(&got); err != nil || got != want {
			t.Fatalf("bootstrap group %s: got %d, want %d: %v", group, got, want, err)
		}
	}

	userID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO core_principals(id,name) VALUES($1,'single-type user')`, userID); err != nil {
		t.Fatal(err)
	}
	if err := SetGroupAccess(ctx, pool, bootstrapID, userID, "content", access.LevelRead); err != nil {
		t.Fatalf("assign direct read access: %v", err)
	}
	var targetLevel int
	if err := pool.QueryRow(ctx, `SELECT access_level FROM core_group_access WHERE principal_id=$1 AND group_key='content'`, userID).Scan(&targetLevel); err != nil || targetLevel != int(access.LevelRead) {
		t.Fatalf("direct assignment persisted level=%d err=%v", targetLevel, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE core_group_access SET access_level=3 WHERE principal_id=$1 AND group_key='content'`, userID); err == nil {
		t.Fatal("database accepted an access level outside 0/1/2")
	}
	if err := SetGroupAccess(ctx, pool, userID, bootstrapID, "content", access.LevelWrite); !errors.Is(err, ErrForbidden) {
		t.Fatalf("account without administrator-management permission changed access: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE core_principals SET can_manage_administrators=true WHERE id=$1`, userID); err != nil {
		t.Fatal(err)
	}
	if err := SetGroupAccess(ctx, pool, userID, bootstrapID, "content", access.LevelNone); !errors.Is(err, ErrForbidden) {
		t.Fatalf("management permission without group level 2 changed access: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE core_access_groups SET active=false WHERE group_key='content'`); err != nil {
		t.Fatal(err)
	}
	if err := SetGroupAccess(ctx, pool, bootstrapID, userID, "content", access.LevelWrite); !errors.Is(err, ErrForbidden) {
		t.Fatalf("inactive configured group accepted a permission change: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT access_level FROM core_group_access WHERE principal_id=$1 AND group_key='content'`, userID).Scan(&targetLevel); err != nil || targetLevel != int(access.LevelRead) {
		t.Fatalf("denied permission change partially modified assignment: level=%d err=%v", targetLevel, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE core_principals SET can_manage_administrators=false WHERE id=$1`, bootstrapID); err != nil {
		t.Fatal(err)
	}
	credentialID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO core_credentials(id,principal_id,secret_hash) VALUES($1,$2,$3)`, credentialID, userID, []byte(strings.Repeat("x", 32))); err != nil {
		t.Fatal(err)
	}
	manager := access.Principal{ID: userID.String(), Active: true, CanManageAdministrators: true}
	if err := DeactivatePrincipal(ctx, pool, manager, userID); err == nil {
		t.Fatal("last active account with administrator-management permission was deactivated")
	}
	var userActive bool
	if err := pool.QueryRow(ctx, `SELECT active FROM core_principals WHERE id=$1`, userID).Scan(&userActive); err != nil || !userActive {
		t.Fatalf("last management account must remain active: %t: %v", userActive, err)
	}
	if err := RevokeCredential(ctx, pool, manager, credentialID); err == nil {
		t.Fatal("last active manager credential was revoked")
	}
}
