package models

import (
	"fmt"
	"io/fs"

	"websiteengine/migrations"
)

// SchemaDDL returns the clean initial schema used by new installations. The
// init migration is the canonical schema contract consumed by both the
// migration runner and Atlas' external-schema loader.
func SchemaDDL() (string, error) {
	ddl, err := fs.ReadFile(migrations.FS, "0001_init.sql")
	if err != nil {
		return "", fmt.Errorf("read clean initial schema: %w", err)
	}
	return string(ddl), nil
}
