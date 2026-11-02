// Package db embeds the migrations in the binary: the production image does not
// ship the source code, and the migrate service applies them from here.
package db

import "embed"

// Migrations are the files in db/migrations.
//
//go:embed migrations/*.sql
var Migrations embed.FS
