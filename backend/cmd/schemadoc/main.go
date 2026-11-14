// Command schemadoc regenerates the generated blocks in the documentation
// (add-project-documentation):
//
//	schemadoc <database-url>
//
// The database must have every migration applied (Better Auth, then goose).
// It rewrites the "schema" block of docs/architecture/erd.md and the
// "limits" block of docs/security.md. Tests in internal/doccheck compare
// the same output with the files and fail on any difference.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jackc/pgx/v5"

	"relaylab/internal/platform/db/schemadoc"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "schemadoc:", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) != 2 {
		return fmt.Errorf("usage: schemadoc <database-url> (run from backend/)")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, os.Args[1])
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	tables, err := schemadoc.Read(ctx, conn)
	if err != nil {
		return err
	}
	limits, err := schemadoc.Limits()
	if err != nil {
		return err
	}
	for _, f := range []struct{ path, block, body string }{
		{filepath.Join("..", "docs", "architecture", "erd.md"), "schema", schemadoc.Markdown(tables)},
		{filepath.Join("..", "docs", "security.md"), "limits", limits},
	} {
		doc, err := os.ReadFile(f.path)
		if err != nil {
			return err
		}
		out, err := schemadoc.Replace(string(doc), f.block, f.body)
		if err != nil {
			return fmt.Errorf("%s: %w", f.path, err)
		}
		if err := os.WriteFile(f.path, []byte(out), 0o644); err != nil { //nolint:gosec // documentation file
			return err
		}
		fmt.Println("updated", f.path)
	}
	return nil
}
