package migrations

import "embed"

// FS holds the versioned SQL migrations (ADR-012).
//
//go:embed *.sql
var FS embed.FS
