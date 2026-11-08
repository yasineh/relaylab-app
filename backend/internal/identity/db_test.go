package identity

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"relaylab/internal/identity/authtest"
	"relaylab/internal/platform/config"
	"relaylab/internal/platform/db"
	"relaylab/internal/platform/db/dbtest"
	"relaylab/internal/platform/db/store"
)

// These tests run against the real Better Auth schema, created by the real
// auth migrate command (authtest.Migrate).

func migratedAuthDB(t *testing.T) *dbtest.DB {
	t.Helper()
	authtest.Require(t)
	d := dbtest.New(t)
	authtest.Migrate(t, d)
	return d
}

func connect(t *testing.T, url string) *pgx.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	return c
}

func isPermissionDenied(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "42501"
}

func TestAuthDBAppRuntimeGrantsAreReadOnly(t *testing.T) {
	d := migratedAuthDB(t)
	ctx := context.Background()
	app := connect(t, d.RuntimeURL)

	for _, q := range []string{
		`SELECT count(*) FROM auth."session"`,
		`SELECT count(*) FROM auth."user"`,
	} {
		var n int
		if err := app.QueryRow(ctx, q).Scan(&n); err != nil {
			t.Errorf("app_runtime cannot read: %s: %v", q, err)
		}
	}

	denied := []string{
		// Writes to the two readable tables.
		`INSERT INTO auth."user" (id, name, email, "emailVerified", "createdAt", "updatedAt") VALUES ('x', 'x', 'x@example.test', true, now(), now())`,
		`UPDATE auth."user" SET name = 'pwned'`,
		`DELETE FROM auth."user"`,
		`INSERT INTO auth."session" (id, "expiresAt", token, "createdAt", "updatedAt", "userId") VALUES ('s', now(), 't', now(), now(), 'x')`,
		`UPDATE auth."session" SET "expiresAt" = now() + interval '1 year'`,
		`DELETE FROM auth."session"`,
		`TRUNCATE auth."session"`,
		// Every access to the tables Go has no business reading (password hashes, tokens).
		`SELECT count(*) FROM auth.account`,
		`SELECT count(*) FROM auth.verification`,
		`SELECT count(*) FROM auth."rateLimit"`,
		`UPDATE auth.account SET password = 'x'`,
		`DELETE FROM auth.verification`,
		// No DDL in the auth schema.
		`CREATE TABLE auth.evil (id int)`,
		`ALTER TABLE auth."session" ADD COLUMN evil int`,
	}
	for _, q := range denied {
		_, err := app.Exec(ctx, q)
		if !isPermissionDenied(err) {
			t.Errorf("app_runtime was not denied (err=%v): %s", err, q)
		}
	}
}

func TestAuthDBValidatorAgainstRealSchema(t *testing.T) {
	d := migratedAuthDB(t)
	ctx := context.Background()

	// Seed as auth_runtime, the role the auth service writes with.
	authConn := connect(t, d.AuthRuntimeURL)
	seed := func(userID, token string, expires time.Time, verified bool) {
		t.Helper()
		if _, err := authConn.Exec(ctx, `INSERT INTO auth."user" (id, name, email, "emailVerified", "createdAt", "updatedAt")
			VALUES ($1, $2, $3, $4, now(), now())`, userID, "Name "+userID, userID+"@example.test", verified); err != nil {
			t.Fatal(err)
		}
		if _, err := authConn.Exec(ctx, `INSERT INTO auth."session" (id, "expiresAt", token, "createdAt", "updatedAt", "userId")
			VALUES ($1, $2, $3, now(), now(), $4)`, "s-"+userID, expires, token, userID); err != nil {
			t.Fatal(err)
		}
	}
	const (
		liveToken    = "LiveTokenLiveTokenLiveTokenLive0"
		expiredToken = "ExpiredTokenExpiredTokenExpired0"
	)
	seed("live", liveToken, time.Now().Add(time.Hour), false)
	seed("old", expiredToken, time.Now().Add(-time.Minute), true)

	pool, err := db.Open(ctx, config.NewSecret(d.RuntimeURL))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	v := NewValidator(&config.Auth{CookieName: config.SessionCookieDev, SessionSecret: config.NewSecret(authtest.SessionSecret)}, store.New(pool))

	check := func(token string) (Identity, error) {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
		r.AddCookie(&http.Cookie{Name: config.SessionCookieDev, Value: cookieValue(authtest.SessionSecret, token)})
		return v.Validate(ctx, r)
	}

	id, err := check(liveToken)
	if err != nil || id.UserID != "live" || id.EmailVerified || id.Email != "live@example.test" {
		t.Fatalf("live: %+v %v", id, err)
	}
	if _, err := check(expiredToken); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("expired: %v", err)
	}
	if _, err := check("UnknownTokenUnknownTokenUnknown0"); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("unknown: %v", err)
	}
	// Revocation: the auth service deletes the row; the next request is rejected.
	if _, err := authConn.Exec(ctx, `DELETE FROM auth."session" WHERE token = $1`, liveToken); err != nil {
		t.Fatal(err)
	}
	if _, err := check(liveToken); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("revoked: %v", err)
	}
	// Deleting the user cascades to its sessions.
	if _, err := authConn.Exec(ctx, `DELETE FROM auth."user" WHERE id = 'old'`); err != nil {
		t.Fatal(err)
	}
	if _, err := check(expiredToken); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("deleted user: %v", err)
	}
}
