// Package migrations embeds the goose SQL migrations for the app schema.
package migrations

import "embed"

// FS holds all *.sql migrations.
//
//go:embed *.sql
var FS embed.FS
