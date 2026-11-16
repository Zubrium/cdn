// Package migrations embeds the SQL migrations so the server binary is
// self-contained.
package migrations

import "embed"

// FS contains all migration files in lexical order, including the atlas.sum
// integrity file of the migration directory.
//
//go:embed *.sql atlas.sum
var FS embed.FS
