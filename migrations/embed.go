// Package migrations embeds the schema migrations applied at startup.
//
// Files are named NNN_name.sql and applied in NNN order, each exactly once, recorded in
// schema_migrations. Feature modules add their own as NNN_<feature>_<change>.sql.
package migrations

import "embed"

// FS holds every *.sql at this directory's root.
//
//go:embed *.sql
var FS embed.FS
