package postgres

import (
	"errors"
	"testing"

	"github.com/Zubrium/cdn/core/application/storage"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestFileSetUniqueConstraintMapsToConflict(t *testing.T) {
	duplicate := &pgconn.PgError{Code: "23505", ConstraintName: "sets_namespace_name_key"}
	if got := mapFileSetUniqueConflict(duplicate); !errors.Is(got, storage.ErrFileSetConflict) {
		t.Fatalf("duplicate set name returned %v", got)
	}

	other := &pgconn.PgError{Code: "23503", ConstraintName: "file_index.sets_management_group_fkey"}
	if got := mapFileSetUniqueConflict(other); !errors.Is(got, other) {
		t.Fatalf("unrelated database error was remapped: %v", got)
	}
}
