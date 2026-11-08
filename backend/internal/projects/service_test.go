package projects_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"relaylab/internal/identity/authtest"
	"relaylab/internal/platform/config"
	"relaylab/internal/platform/db"
	"relaylab/internal/platform/db/dbtest"
	"relaylab/internal/platform/db/store"
	"relaylab/internal/projects"
)

// env is a migrated database with two users, alice and bob.
type env struct {
	d     *dbtest.DB
	pool  *pgxpool.Pool
	admin *pgx.Conn
}

func newEnv(t *testing.T) *env {
	t.Helper()
	d := dbtest.New(t)
	authtest.Prepare(t, d)
	ctx := context.Background()
	if err := db.Migrate(ctx, config.NewSecret(d.MigratorURL), slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	pool, err := db.Open(ctx, config.NewSecret(d.RuntimeURL))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	admin, err := pgx.Connect(ctx, d.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close(context.Background()) })
	e := &env{d: d, pool: pool, admin: admin}
	e.addUser(t, "alice")
	e.addUser(t, "bob")
	return e
}

func (e *env) addUser(t *testing.T, id string) {
	t.Helper()
	if _, err := e.admin.Exec(context.Background(), `INSERT INTO auth."user" (id, name, email, "emailVerified", "createdAt", "updatedAt")
		VALUES ($1, $1, $1 || '@example.test', true, now(), now())`, id); err != nil {
		t.Fatal(err)
	}
}

func ptr[T any](v T) *T { return &v }

func validationFields(t *testing.T, err error) map[string]string {
	t.Helper()
	var ve *projects.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("err = %v, want ValidationError", err)
	}
	return ve.Fields
}

func TestCreateAndGet(t *testing.T) {
	e := newEnv(t)
	svc := projects.NewService(e.pool, 20)
	ctx := context.Background()

	p, err := svc.Create(ctx, "alice", "  Stripe sandbox  ", ptr("Payments test hooks"))
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "Stripe sandbox" || p.Description == nil || *p.Description != "Payments test hooks" {
		t.Fatalf("created %+v", p)
	}
	if p.ID.Version() != 7 {
		t.Errorf("id %s is not a UUIDv7", p.ID)
	}
	if p.CreatedAt.IsZero() || !p.CreatedAt.Equal(p.UpdatedAt) || p.CreatedAt.Location() != time.UTC {
		t.Errorf("timestamps %v %v", p.CreatedAt, p.UpdatedAt)
	}
	got, err := svc.Authorize(ctx, "alice", p.ID)
	if err != nil || got.ID != p.ID || got.Name != p.Name {
		t.Fatalf("authorize: %+v %v", got, err)
	}
}

func TestEmptyDescriptionIsStoredAsNull(t *testing.T) {
	e := newEnv(t)
	p, err := projects.NewService(e.pool, 20).Create(context.Background(), "alice", "x", ptr("   "))
	if err != nil || p.Description != nil {
		t.Fatalf("%+v %v", p, err)
	}
}

func TestCreateValidation(t *testing.T) {
	e := newEnv(t)
	svc := projects.NewService(e.pool, 20)
	ctx := context.Background()
	cases := []struct {
		name, field string
		pname       string
		desc        *string
	}{
		{"empty name", "name", "", nil},
		{"whitespace name", "name", "   ", nil},
		{"81 characters", "name", strings.Repeat("n", 81), nil},
		{"81 multibyte characters", "name", strings.Repeat("é", 81), nil},
		{"newline in name", "name", "a\nb", nil},
		{"NUL in name", "name", "a\x00b", nil},
		{"501-character description", "description", "ok", ptr(strings.Repeat("d", 501))},
		{"control character in description", "description", "ok", ptr("a\x07b")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.Create(ctx, "alice", tc.pname, tc.desc)
			if fields := validationFields(t, err); fields[tc.field] == "" {
				t.Fatalf("no error for %s: %v", tc.field, fields)
			}
		})
	}
	var n int
	_ = e.admin.QueryRow(ctx, `SELECT count(*) FROM app.projects`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d projects created by invalid requests", n)
	}

	// Boundaries that are allowed.
	if _, err := svc.Create(ctx, "alice", strings.Repeat("é", 80), ptr("line one\nline two\ttab")); err != nil {
		t.Fatalf("80 characters and multiline description: %v", err)
	}
}

func TestHTMLNameStoredAsGiven(t *testing.T) {
	e := newEnv(t)
	name := `<img src=x onerror=alert(1)>`
	p, err := projects.NewService(e.pool, 20).Create(context.Background(), "alice", name, nil)
	if err != nil || p.Name != name {
		t.Fatalf("%+v %v", p, err)
	}
}

func TestListIsOwnerScopedAndNewestFirst(t *testing.T) {
	e := newEnv(t)
	svc := projects.NewService(e.pool, 20)
	ctx := context.Background()
	for _, n := range []string{"a1", "a2", "a3"} {
		if _, err := svc.Create(ctx, "alice", n, nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.Create(ctx, "bob", "b1", nil); err != nil {
		t.Fatal(err)
	}
	list, err := svc.List(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range list {
		names = append(names, p.Name)
	}
	if strings.Join(names, ",") != "a3,a2,a1" {
		t.Fatalf("alice sees %v", names)
	}
	empty, err := svc.List(ctx, "nobody")
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("new user: %v %v (must be an empty, non-nil list)", empty, err)
	}
}

func TestEveryQueryIsScopedToTheOwner(t *testing.T) {
	e := newEnv(t)
	svc := projects.NewService(e.pool, 20)
	ctx := context.Background()
	p, err := svc.Create(ctx, "alice", "Alice's", nil)
	if err != nil {
		t.Fatal(err)
	}

	// Service level.
	if _, err := svc.Authorize(ctx, "bob", p.ID); !errors.Is(err, projects.ErrNotFound) {
		t.Errorf("authorize as bob: %v", err)
	}
	if _, err := svc.Update(ctx, "bob", p.ID, projects.Update{Name: projects.Optional[string]{Set: true, Value: "pwned"}}); !errors.Is(err, projects.ErrNotFound) {
		t.Errorf("update as bob: %v", err)
	}
	if err := svc.Delete(ctx, "bob", p.ID); !errors.Is(err, projects.ErrNotFound) {
		t.Errorf("delete as bob: %v", err)
	}
	if l, _ := svc.List(ctx, "bob"); len(l) != 0 {
		t.Errorf("bob lists %v", l)
	}

	// Data layer: every generated query returns nothing for a foreign owner.
	q := store.New(e.pool)
	id := p.ID
	pid := [16]byte(id)
	if _, err := q.GetProject(ctx, store.GetProjectParams{ID: pgID(pid), OwnerID: "bob"}); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("GetProject: %v", err)
	}
	if _, err := q.UpdateProject(ctx, store.UpdateProjectParams{ID: pgID(pid), OwnerID: "bob"}); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("UpdateProject: %v", err)
	}
	if n, err := q.DeleteProject(ctx, store.DeleteProjectParams{ID: pgID(pid), OwnerID: "bob"}); err != nil || n != 0 {
		t.Errorf("DeleteProject: %d %v", n, err)
	}
	if rows, err := q.ListProjects(ctx, "bob"); err != nil || len(rows) != 0 {
		t.Errorf("ListProjects: %v %v", rows, err)
	}
	if n, err := q.CountProjects(ctx, "bob"); err != nil || n != 0 {
		t.Errorf("CountProjects: %d %v", n, err)
	}

	got, err := svc.Authorize(ctx, "alice", p.ID)
	if err != nil || got.Name != "Alice's" {
		t.Fatalf("alice's project changed: %+v %v", got, err)
	}
}

func TestUpdateSemantics(t *testing.T) {
	e := newEnv(t)
	svc := projects.NewService(e.pool, 20)
	ctx := context.Background()
	p, _ := svc.Create(ctx, "alice", "Old", ptr("desc"))
	time.Sleep(5 * time.Millisecond)

	renamed, err := svc.Update(ctx, "alice", p.ID, projects.Update{Name: projects.Optional[string]{Set: true, Value: " GitHub app "}})
	if err != nil || renamed.Name != "GitHub app" || renamed.Description == nil || *renamed.Description != "desc" {
		t.Fatalf("rename: %+v %v", renamed, err)
	}
	if !renamed.UpdatedAt.After(p.UpdatedAt) || !renamed.CreatedAt.Equal(p.CreatedAt) {
		t.Errorf("timestamps: created %v→%v updated %v→%v", p.CreatedAt, renamed.CreatedAt, p.UpdatedAt, renamed.UpdatedAt)
	}

	cleared, err := svc.Update(ctx, "alice", p.ID, projects.Update{Description: projects.Optional[*string]{Set: true, Value: nil}})
	if err != nil || cleared.Description != nil || cleared.Name != "GitHub app" {
		t.Fatalf("clear description: %+v %v", cleared, err)
	}

	set, err := svc.Update(ctx, "alice", p.ID, projects.Update{Description: projects.Optional[*string]{Set: true, Value: ptr("new")}})
	if err != nil || set.Description == nil || *set.Description != "new" {
		t.Fatalf("set description: %+v %v", set, err)
	}

	_, err = svc.Update(ctx, "alice", p.ID, projects.Update{Name: projects.Optional[string]{Set: true, Value: ""}})
	if validationFields(t, err)["name"] == "" {
		t.Fatal("empty rename accepted")
	}
	_, err = svc.Update(ctx, "alice", p.ID, projects.Update{})
	validationFields(t, err)
}

func TestDelete(t *testing.T) {
	e := newEnv(t)
	svc := projects.NewService(e.pool, 20)
	ctx := context.Background()
	p, _ := svc.Create(ctx, "alice", "doomed", nil)
	if err := svc.Delete(ctx, "alice", p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Authorize(ctx, "alice", p.ID); !errors.Is(err, projects.ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}
	if err := svc.Delete(ctx, "alice", p.ID); !errors.Is(err, projects.ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
}

func TestLimitDefault(t *testing.T) {
	e := newEnv(t)
	limits := config.DefaultLimits()
	if limits.ProjectsPerUser != 20 {
		t.Fatalf("default limit = %d", limits.ProjectsPerUser)
	}
	svc := projects.NewService(e.pool, limits.ProjectsPerUser)
	ctx := context.Background()
	for i := range 20 {
		if _, err := svc.Create(ctx, "alice", fmt.Sprintf("p%d", i), nil); err != nil {
			t.Fatalf("project %d: %v", i+1, err)
		}
	}
	if _, err := svc.Create(ctx, "alice", "one too many", nil); !errors.Is(err, projects.ErrLimitReached) {
		t.Fatalf("21st: %v", err)
	}
	// The limit is per user.
	if _, err := svc.Create(ctx, "bob", "bob's first", nil); err != nil {
		t.Fatalf("bob: %v", err)
	}
	// Deleting frees a slot.
	list, _ := svc.List(ctx, "alice")
	_ = svc.Delete(ctx, "alice", list[0].ID)
	if _, err := svc.Create(ctx, "alice", "fits again", nil); err != nil {
		t.Fatalf("after delete: %v", err)
	}
}

func TestLimitLoweredThroughEnv(t *testing.T) {
	e := newEnv(t)
	limits, err := config.LoadLimits(func(k string) string {
		return map[string]string{"LIMIT_PROJECTS_PER_USER": "2"}[k]
	})
	if err != nil {
		t.Fatal(err)
	}
	svc := projects.NewService(e.pool, limits.ProjectsPerUser)
	ctx := context.Background()
	for _, n := range []string{"one", "two"} {
		if _, err := svc.Create(ctx, "alice", n, nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.Create(ctx, "alice", "three", nil); !errors.Is(err, projects.ErrLimitReached) {
		t.Fatalf("third: %v", err)
	}
}

func TestConcurrentCreatesAtTheLimit(t *testing.T) {
	e := newEnv(t)
	svc := projects.NewService(e.pool, 20)
	ctx := context.Background()
	for i := range 19 {
		if _, err := svc.Create(ctx, "alice", fmt.Sprintf("p%d", i), nil); err != nil {
			t.Fatal(err)
		}
	}
	const racers = 8
	var wg sync.WaitGroup
	errs := make([]error, racers)
	start := make(chan struct{})
	for i := range racers {
		wg.Go(func() {
			<-start
			_, errs[i] = svc.Create(ctx, "alice", fmt.Sprintf("racer%d", i), nil)
		})
	}
	close(start)
	wg.Wait()
	ok, limited := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, projects.ErrLimitReached):
			limited++
		default:
			t.Errorf("unexpected error: %v", err)
		}
	}
	if ok != 1 || limited != racers-1 {
		t.Fatalf("ok=%d limited=%d", ok, limited)
	}
	var n int
	_ = e.admin.QueryRow(ctx, `SELECT count(*) FROM app.projects WHERE owner_id = 'alice'`).Scan(&n)
	if n != 20 {
		t.Fatalf("alice owns %d projects", n)
	}
}

func TestParseID(t *testing.T) {
	for _, raw := range []string{"not-a-uuid", "", "00000000-0000-0000-0000-000000000000", "1", "../x"} {
		if _, err := projects.ParseID(raw); !errors.Is(err, projects.ErrNotFound) {
			t.Errorf("%q: %v", raw, err)
		}
	}
	id := uuid.Must(uuid.NewV7())
	if got, err := projects.ParseID(id.String()); err != nil || got != id {
		t.Errorf("valid id: %v %v", got, err)
	}
}

func pgID(b [16]byte) pgtype.UUID { return pgtype.UUID{Bytes: b, Valid: true} }
