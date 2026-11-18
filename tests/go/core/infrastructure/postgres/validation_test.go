package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Zubrium/cdn/core/domain/access"
	"github.com/google/uuid"
)

func TestRejectsNilDependenciesAndUnauthorizedIdentity(t *testing.T) {
	ctx := context.Background()
	admin := access.Principal{ID: "admin", Active: true, CanManageAdministrators: true}
	if err := Migrate(ctx, nil, "atlas", time.Second); err == nil {
		t.Fatal("nil migration pool accepted")
	}
	if _, _, err := Bootstrap(ctx, nil, ""); err == nil {
		t.Fatal("nil bootstrap pool accepted")
	}
	if _, err := Authenticate(ctx, nil, "x"); err == nil {
		t.Fatal("nil auth pool accepted")
	}
	if _, err := (Objects{}).Find(ctx, "id"); err == nil {
		t.Fatal("nil object pool accepted")
	}
	if _, _, err := CreatePrincipal(ctx, nil, access.Principal{}, "name", nil); !errors.Is(err, ErrUnauthorized) {
		t.Fatal(err)
	}
	if _, _, err := CreatePrincipal(ctx, nil, admin, "name", nil); err == nil {
		t.Fatal("nil pool accepted")
	}
	if err := RevokeCredential(ctx, nil, access.Principal{}, uuid.New()); !errors.Is(err, ErrUnauthorized) {
		t.Fatal(err)
	}
	if err := RevokeCredential(ctx, nil, admin, uuid.Nil); err == nil {
		t.Fatal("invalid credential accepted")
	}
	if err := SetGroupAccess(ctx, nil, uuid.New(), uuid.New(), "content", access.LevelRead); err == nil {
		t.Fatal("group assignment accepted a nil database")
	}
}
