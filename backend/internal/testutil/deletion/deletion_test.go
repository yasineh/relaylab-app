package deletion_test

import (
	"context"
	"log/slog"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"

	"relaylab/internal/identity/authtest"
	"relaylab/internal/platform/config"
	"relaylab/internal/platform/db"
	"relaylab/internal/platform/db/dbtest"
	"relaylab/internal/testutil/deletion"
)

func migrated(t *testing.T) (*dbtest.DB, *pgx.Conn) {
	t.Helper()
	d := dbtest.New(t)
	authtest.Prepare(t, d)
	ctx := context.Background()
	if err := db.Migrate(ctx, config.NewSecret(d.MigratorURL), slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	c, err := pgx.Connect(ctx, d.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	return d, c
}

func TestEveryProjectIDColumnCascades(t *testing.T) {
	_, admin := migrated(t)
	bad, err := deletion.UncascadedProjectColumns(context.Background(), admin)
	if err != nil {
		t.Fatal(err)
	}
	if len(bad) > 0 {
		t.Fatalf("tables with project_id but no ON DELETE CASCADE FK to app.projects: %v", bad)
	}
}

func TestSchemaCheckCatchesBrokenTables(t *testing.T) {
	d, admin := migrated(t)
	ctx := context.Background()
	mig, err := pgx.Connect(ctx, d.MigratorURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mig.Close(ctx) }()
	// A scratch "migration" that forgets the FK, one that forgets the cascade, and a correct one.
	for _, ddl := range []string{
		`CREATE TABLE app.scratch_no_fk (id int, project_id uuid)`,
		`CREATE TABLE app.scratch_restrict (id int, project_id uuid REFERENCES app.projects (id))`,
		`CREATE TABLE app.scratch_ok (id int, project_id uuid NOT NULL REFERENCES app.projects (id) ON DELETE CASCADE)`,
	} {
		if _, err := mig.Exec(ctx, ddl); err != nil {
			t.Fatal(err)
		}
	}
	bad, err := deletion.UncascadedProjectColumns(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(bad, []string{"app.scratch_no_fk", "app.scratch_restrict"}) {
		t.Fatalf("got %v", bad)
	}
}

func TestEveryProjectScopedTableIsRegistered(t *testing.T) {
	_, admin := migrated(t)
	rows, err := admin.Query(context.Background(), `
		SELECT table_schema || '.' || table_name FROM information_schema.columns
		WHERE table_schema = 'app' AND column_name = 'project_id' ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	registered := map[string]bool{}
	for _, tbl := range deletion.Tables() {
		registered[tbl.Name] = true
	}
	for rows.Next() {
		var name string
		_ = rows.Scan(&name)
		if !registered[name] {
			t.Errorf("%s has project_id but is not registered in internal/testutil/deletion/tables.go", name)
		}
	}
	if !registered["app.projects"] {
		t.Error("app.projects must be registered")
	}
}

func TestAssertNoOrphansAfterProjectAndOwnerDeletion(t *testing.T) {
	d, admin := migrated(t)
	ctx := context.Background()
	if _, err := admin.Exec(ctx, `INSERT INTO auth."user" (id, name, email, "emailVerified", "createdAt", "updatedAt")
		VALUES ('u1', 'U', 'u1@example.test', true, now(), now())`); err != nil {
		t.Fatal(err)
	}
	var p1, p2 string
	for _, p := range []*string{&p1, &p2} {
		if err := admin.QueryRow(ctx, `INSERT INTO app.projects (id, owner_id, name) VALUES (gen_random_uuid(), 'u1', 'p') RETURNING id::text`).Scan(p); err != nil {
			t.Fatal(err)
		}
	}
	app, err := pgx.Connect(ctx, d.RuntimeURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = app.Close(ctx) }()

	// Deleting one project as the runtime role, in one statement.
	if _, err := app.Exec(ctx, `DELETE FROM app.projects WHERE id = $1 AND owner_id = 'u1'`, p1); err != nil {
		t.Fatal(err)
	}
	deletion.AssertNoOrphans(t, app, p1)

	// Deleting the owner removes the rest.
	if _, err := admin.Exec(ctx, `DELETE FROM auth."user" WHERE id = 'u1'`); err != nil {
		t.Fatal(err)
	}
	deletion.AssertNoOrphans(t, app, p2)
}

func TestAssertNoOrphansDetectsLeftovers(t *testing.T) {
	_, admin := migrated(t)
	ctx := context.Background()
	if _, err := admin.Exec(ctx, `INSERT INTO auth."user" (id, name, email, "emailVerified", "createdAt", "updatedAt")
		VALUES ('u1', 'U', 'u1@example.test', true, now(), now())`); err != nil {
		t.Fatal(err)
	}
	var p string
	_ = admin.QueryRow(ctx, `INSERT INTO app.projects (id, owner_id, name) VALUES (gen_random_uuid(), 'u1', 'p') RETURNING id::text`).Scan(&p)
	fake := &recordingTB{TB: t}
	deletion.AssertNoOrphans(fake, admin, p) // project still exists
	if !fake.failed {
		t.Fatal("AssertNoOrphans did not report the remaining row")
	}
}

type recordingTB struct {
	testing.TB
	failed bool
}

func (r *recordingTB) Errorf(string, ...any) { r.failed = true }
func (r *recordingTB) Helper()               {}
