package doccheck

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"relaylab/internal/identity/authtest"
	"relaylab/internal/platform/config"
	"relaylab/internal/platform/db"
	"relaylab/internal/platform/db/dbtest"
	"relaylab/internal/platform/db/schemadoc"
)

// update rewrites the generated blocks instead of failing:
// RELAYLAB_UPDATE_DOCS=1 (pnpm docs:generate).
var update = os.Getenv("RELAYLAB_UPDATE_DOCS") == "1"

func checkBlock(t *testing.T, rel, block, want string) {
	t.Helper()
	path := filepath.Join(repoRoot(t), rel)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	doc := string(b)
	if update {
		out, err := schemadoc.Replace(doc, block, want)
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(out), 0o644); err != nil { //nolint:gosec // documentation file
			t.Fatal(err)
		}
		return
	}
	got, ok := schemadoc.Block(doc, block)
	if !ok {
		t.Fatalf("%s has no %q generated block", rel, block)
	}
	if got == strings.TrimSpace(want) {
		return
	}
	gotLines := map[string]bool{}
	for _, l := range strings.Split(got, "\n") {
		gotLines[l] = true
	}
	wantLines := map[string]bool{}
	var missing, stale []string
	for _, l := range strings.Split(strings.TrimSpace(want), "\n") {
		wantLines[l] = true
		if !gotLines[l] {
			missing = append(missing, l)
		}
	}
	for _, l := range strings.Split(got, "\n") {
		if !wantLines[l] {
			stale = append(stale, l)
		}
	}
	t.Fatalf("%s is out of date (run pnpm docs:generate).\nmissing:\n  %s\nno longer true:\n  %s",
		rel, strings.Join(missing, "\n  "), strings.Join(stale, "\n  "))
}

// The ERD's generated listing matches a database with every migration
// applied: Better Auth's, then goose's (spec: "ERD matches migrations"). It
// needs the real auth migrations, so it runs where Node.js is available
// (pnpm test, the CI auth-contract job).
func TestAuthDBERDMatchesSchema(t *testing.T) {
	authtest.Require(t)
	d := dbtest.New(t)
	authtest.Migrate(t, d)
	ctx := context.Background()
	if err := db.Migrate(ctx, config.NewSecret(d.MigratorURL), slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	conn, err := pgx.Connect(ctx, d.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	tables, err := schemadoc.Read(ctx, conn)
	if err != nil {
		t.Fatal(err)
	}
	if len(tables) < 12 {
		t.Fatalf("only %d tables", len(tables))
	}
	checkBlock(t, filepath.Join("docs", "architecture", "erd.md"), "schema", schemadoc.Markdown(tables))
	if update {
		return
	}
	// The hand-drawn diagram names every table and, for app tables, every column.
	erd, _ := os.ReadFile(filepath.Join(repoRoot(t), "docs", "architecture", "erd.md"))
	diagram := regexp.MustCompile("(?s)```mermaid\n(erDiagram.*?)```").FindStringSubmatch(string(erd))
	if diagram == nil {
		t.Fatal("no Mermaid erDiagram")
	}
	for _, tb := range tables {
		entity := tb.Schema + "_" + tb.Name
		if tb.Name == "goose_db_version" {
			continue // migration bookkeeping, documented in the tables section
		}
		if !strings.Contains(diagram[1], entity) {
			t.Errorf("erDiagram has no entity %s", entity)
			continue
		}
		body := regexp.MustCompile(`(?s)\n  ` + regexp.QuoteMeta(entity) + ` \{\n(.*?)\n  \}`).FindStringSubmatch(diagram[1])
		if body == nil {
			t.Errorf("erDiagram has no attribute block for %s", entity)
			continue
		}
		for _, c := range tb.Columns {
			if !regexp.MustCompile(`(?m)^\s+\S+ "?` + regexp.QuoteMeta(c.Name) + `"?\b`).MatchString(body[1]) {
				t.Errorf("erDiagram entity %s lacks column %s", entity, c.Name)
			}
		}
	}
}

// The security doc's limits table matches the configuration defaults (spec:
// "Limits match configuration").
func TestSecurityDocLimitsMatchConfig(t *testing.T) {
	limits, err := schemadoc.Limits()
	if err != nil {
		t.Fatal(err)
	}
	checkBlock(t, "docs/security.md", "limits", limits)
}
