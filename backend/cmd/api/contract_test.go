package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"relaylab/internal/endpoints"
	"relaylab/internal/identity"
	"relaylab/internal/identity/authtest"
	"relaylab/internal/platform/config"
	"relaylab/internal/platform/db"
	"relaylab/internal/platform/db/dbtest"
	"relaylab/internal/platform/db/store"
	"relaylab/internal/platform/health"
	"relaylab/internal/projects"
)

// Contract tests: the real auth service (Better Auth, pinned version) behind the
// real Go proxy, with sessions validated by the real Go validator against the
// real schema. Run in CI by the auth-contract job (backend/scripts/auth-contract.sh).

const contractPassword = "violet-anchor-89-orbit" //nolint:gosec // G101: synthetic test passphrase.

type contractEnv struct {
	t     *testing.T
	api   *httptest.Server
	admin *pgx.Conn
	svc   *authtest.Service
}

func newContractEnv(t *testing.T, authEnv ...string) *contractEnv {
	t.Helper()
	authtest.Require(t)
	d := dbtest.New(t)
	authtest.Migrate(t, d)
	svc := authtest.Start(t, d, authEnv...)

	env := map[string]string{
		"APP_ENV":                  "test",
		"APP_ORIGIN":               authtest.AppOrigin,
		"DATABASE_URL":             d.RuntimeURL,
		"BETTER_AUTH_SECRET":       authtest.SessionSecret,
		"AUTH_PROXY_SHARED_SECRET": authtest.ProxySecret,
		"AUTH_SERVICE_URL":         svc.URL,
		"TRUSTED_PROXY_CIDRS":      "127.0.0.0/8",
	}
	getenv := func(k string) string { return env[k] }
	cfg, err := config.Load(getenv)
	if err != nil {
		t.Fatal(err)
	}
	authCfg, err := config.LoadAuth(getenv, cfg.Env)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	ok := func(context.Context) error { return nil }
	api := httptest.NewServer(newRouter(routerDeps{
		cfg:       cfg,
		auth:      authCfg,
		logger:    slog.New(slog.DiscardHandler),
		health:    health.New(ok, ok),
		validator: identity.NewValidator(authCfg, store.New(pool)),
		authProxy: identity.NewAuthProxy(identity.ProxyOptions{
			Target: authCfg.ServiceURL, Secret: authCfg.ProxySharedSecret,
			Timeout: 10 * time.Second, ForwardedTo: cfg.AppOrigin.Scheme,
		}),
		projects:  projects.NewHandler(projects.NewService(pool, cfg.Limits.ProjectsPerUser)),
		endpoints: endpoints.NewHandler(endpoints.NewService(pool, nil, "", 25)), // not exercised here
	}))
	t.Cleanup(api.Close)

	admin, err := pgx.Connect(ctx, d.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close(context.Background()) })
	return &contractEnv{t: t, api: api, admin: admin, svc: svc}
}

type resp struct {
	status  int
	header  http.Header
	body    string
	cookies []*http.Cookie
}

// do sends a browser-like same-origin request through the Go API. clientIP is
// set as X-Forwarded-For from a trusted (loopback) peer.
func (e *contractEnv) do(method, path, body, cookie, clientIP string) resp {
	e.t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, _ := http.NewRequestWithContext(context.Background(), method, e.api.URL+path, rd)
	req.Header.Set("Origin", authtest.AppOrigin)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	if clientIP != "" {
		req.Header.Set("X-Forwarded-For", clientIP)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		e.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(res.Body)
	return resp{status: res.StatusCode, header: res.Header, body: string(b), cookies: res.Cookies()}
}

func sessionCookie(r resp) string {
	for _, c := range r.cookies {
		if c.Name == config.SessionCookieDev && c.Value != "" {
			return c.Name + "=" + c.Value
		}
	}
	return ""
}

func (e *contractEnv) exec(sql string, args ...any) {
	e.t.Helper()
	if _, err := e.admin.Exec(context.Background(), sql, args...); err != nil {
		e.t.Fatalf("%s: %v", sql, err)
	}
}

// signUpVerified signs up through the proxy, marks the email verified (the
// email link is covered by the auth service tests), and signs in.
func (e *contractEnv) signUpVerified(email, password string) string {
	e.t.Helper()
	up := e.do(http.MethodPost, "/api/auth/sign-up/email",
		`{"email":"`+email+`","password":"`+password+`","name":"Contract User"}`, "", "")
	if up.status != http.StatusOK || sessionCookie(up) != "" {
		e.t.Fatalf("sign-up: %d %s (cookie issued: %v)", up.status, up.body, sessionCookie(up) != "")
	}
	e.exec(`UPDATE auth."user" SET "emailVerified" = true WHERE email = $1`, email)
	return e.signIn(email, password)
}

func (e *contractEnv) signIn(email, password string) string {
	e.t.Helper()
	in := e.do(http.MethodPost, "/api/auth/sign-in/email", `{"email":"`+email+`","password":"`+password+`"}`, "", "")
	c := sessionCookie(in)
	if in.status != http.StatusOK || c == "" {
		e.t.Fatalf("sign-in: %d %s", in.status, in.body)
	}
	return c
}

func (e *contractEnv) me(cookie string) (int, string) {
	e.t.Helper()
	r := e.do(http.MethodGet, "/api/v1/me", "", cookie, "")
	var body struct {
		ID    string `json:"id"`
		Email string `json:"email"`
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal([]byte(r.body), &body)
	if r.status == http.StatusOK {
		return r.status, body.Email
	}
	return r.status, body.Error.Code
}

func TestContractSessionLifecycle(t *testing.T) {
	e := newContractEnv(t)

	cookie := e.signUpVerified("ada@example.test", contractPassword)

	// A fresh cookie from the real auth service validates in Go.
	if status, email := e.me(cookie); status != http.StatusOK || email != "ada@example.test" {
		t.Fatalf("fresh cookie: %d %s", status, email)
	}

	// Cookie attributes arrive unchanged through the proxy.
	in := e.do(http.MethodPost, "/api/auth/sign-in/email", `{"email":"ada@example.test","password":"`+contractPassword+`"}`, "", "")
	raw := strings.Join(in.header.Values("Set-Cookie"), "\n")
	for _, want := range []string{"relaylab.session_token=", "HttpOnly", "SameSite=Lax", "Path=/"} {
		if !strings.Contains(raw, want) {
			t.Errorf("Set-Cookie missing %q: %s", want, raw)
		}
	}
	if strings.Contains(strings.ToLower(raw), "domain=") {
		t.Errorf("cookie must be host-only: %s", raw)
	}

	// Tampered cookie: one character of the signature changed.
	tampered := cookie[:len(cookie)-4] + flipChar(cookie[len(cookie)-4]) + cookie[len(cookie)-3:]
	if status, code := e.me(tampered); status != http.StatusUnauthorized || code != "unauthenticated" {
		t.Fatalf("tampered: %d %s", status, code)
	}

	// Sign-out revokes the session; replaying the old cookie is rejected.
	if out := e.do(http.MethodPost, "/api/auth/sign-out", "{}", cookie, ""); out.status != http.StatusOK {
		t.Fatalf("sign-out: %d %s", out.status, out.body)
	}
	if status, code := e.me(cookie); status != http.StatusUnauthorized || code != "unauthenticated" {
		t.Fatalf("after sign-out: %d %s", status, code)
	}
}

func TestContractRevokeAllAndPasswordChange(t *testing.T) {
	e := newContractEnv(t)
	a := e.signUpVerified("grace@example.test", contractPassword)
	b := e.signIn("grace@example.test", contractPassword)

	// Sign out everywhere.
	if r := e.do(http.MethodPost, "/api/auth/revoke-sessions", "{}", a, ""); r.status != http.StatusOK {
		t.Fatalf("revoke-sessions: %d %s", r.status, r.body)
	}
	for _, c := range []string{a, b} {
		if status, code := e.me(c); status != http.StatusUnauthorized || code != "unauthenticated" {
			t.Fatalf("after revoke-all: %d %s", status, code)
		}
	}

	// Password change with revokeOtherSessions: the other session dies, the current one survives.
	c := e.signIn("grace@example.test", contractPassword)
	d := e.signIn("grace@example.test", contractPassword)
	r := e.do(http.MethodPost, "/api/auth/change-password",
		`{"currentPassword":"`+contractPassword+`","newPassword":"harbor-lantern-42-quartz","revokeOtherSessions":true}`, c, "")
	if r.status != http.StatusOK {
		t.Fatalf("change-password: %d %s", r.status, r.body)
	}
	if status, code := e.me(d); status != http.StatusUnauthorized || code != "unauthenticated" {
		t.Fatalf("other session after password change: %d %s", status, code)
	}
	current := sessionCookie(r)
	if current == "" {
		current = c
	}
	if status, _ := e.me(current); status != http.StatusOK {
		t.Fatalf("current session after password change: %d", status)
	}
}

func TestContractExpiryAndVerification(t *testing.T) {
	e := newContractEnv(t)
	cookie := e.signUpVerified("linus@example.test", contractPassword)

	// Unverified users get 403 while verification is required.
	e.exec(`UPDATE auth."user" SET "emailVerified" = false WHERE email = 'linus@example.test'`)
	if status, code := e.me(cookie); status != http.StatusForbidden || code != "email_not_verified" {
		t.Fatalf("unverified: %d %s", status, code)
	}
	e.exec(`UPDATE auth."user" SET "emailVerified" = true WHERE email = 'linus@example.test'`)

	// Expiry forced into the past.
	e.exec(`UPDATE auth."session" SET "expiresAt" = now() - interval '1 second'`)
	if status, code := e.me(cookie); status != http.StatusUnauthorized || code != "session_expired" {
		t.Fatalf("expired: %d %s", status, code)
	}
}

func TestContractGoNeverWritesSessions(t *testing.T) {
	e := newContractEnv(t)
	cookie := e.signUpVerified("barbara@example.test", contractPassword)
	var before time.Time
	if err := e.admin.QueryRow(context.Background(), `SELECT "updatedAt" FROM auth."session"`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	for range 5 {
		if status, _ := e.me(cookie); status != http.StatusOK {
			t.Fatalf("me: %d", status)
		}
	}
	var after time.Time
	_ = e.admin.QueryRow(context.Background(), `SELECT "updatedAt" FROM auth."session"`).Scan(&after)
	if !after.Equal(before) {
		t.Fatalf("Go API changed the session row: %v -> %v", before, after)
	}
}

func TestContractRateLimitThroughProxy(t *testing.T) {
	e := newContractEnv(t, "AUTH_RATE_LIMIT_ENABLED=true")
	body := `{"email":"nobody@example.test","password":"not-the-password-1"}`
	ip := "203.0.113.50"
	for i := range 10 {
		if r := e.do(http.MethodPost, "/api/auth/sign-in/email", body, "", ip); r.status != http.StatusUnauthorized {
			t.Fatalf("attempt %d: %d %s", i+1, r.status, r.body)
		}
	}
	blocked := e.do(http.MethodPost, "/api/auth/sign-in/email", body, "", ip)
	if blocked.status != http.StatusTooManyRequests {
		t.Fatalf("11th attempt: %d %s", blocked.status, blocked.body)
	}
	if ra := blocked.header.Get("Retry-After"); ra == "" || ra == "0" {
		t.Fatalf("Retry-After = %q", ra)
	}
	// Another client IP is not affected: the limit is keyed on the IP the Go proxy derived.
	if r := e.do(http.MethodPost, "/api/auth/sign-in/email", body, "", "203.0.113.51"); r.status != http.StatusUnauthorized {
		t.Fatalf("other IP: %d", r.status)
	}
	// The derived client IP, not a value a client could forge past a trusted proxy, is the key.
	var keys []string
	rows, err := e.admin.Query(context.Background(), `SELECT key FROM auth."rateLimit" ORDER BY key`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var k string
		_ = rows.Scan(&k)
		keys = append(keys, k)
	}
	if len(keys) != 2 || !strings.HasPrefix(keys[0], ip+"|") {
		t.Fatalf("rate-limit keys = %v", keys)
	}
	if _, err := netip.ParseAddr(strings.SplitN(keys[0], "|", 2)[0]); err != nil {
		t.Fatalf("key is not keyed by IP: %v", keys)
	}
}

func TestContractResetEnumerationThroughProxy(t *testing.T) {
	e := newContractEnv(t)
	e.signUpVerified("known@example.test", contractPassword)
	known := e.do(http.MethodPost, "/api/auth/request-password-reset", `{"email":"known@example.test","redirectTo":"/reset-password"}`, "", "")
	unknown := e.do(http.MethodPost, "/api/auth/request-password-reset", `{"email":"unknown@example.test","redirectTo":"/reset-password"}`, "", "")
	if known.status != http.StatusOK || unknown.status != known.status || unknown.body != known.body {
		t.Fatalf("known %d %s / unknown %d %s", known.status, known.body, unknown.status, unknown.body)
	}
}

func TestContractDirectAccessRefused(t *testing.T) {
	e := newContractEnv(t)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, e.svc.URL+"/api/auth/sign-in/email",
		strings.NewReader(`{"email":"a@example.test","password":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("direct access: %d", res.StatusCode)
	}
}

func TestContractServiceLogsAreCredentialFree(t *testing.T) {
	e := newContractEnv(t)
	cookie := e.signUpVerified("logs@example.test", contractPassword)
	e.do(http.MethodPost, "/api/auth/sign-out", "{}", cookie, "")
	logs := e.svc.Logs()
	for _, s := range []string{"logs@example.test", contractPassword, strings.SplitN(cookie, "=", 2)[1], authtest.SessionSecret, authtest.ProxySecret} {
		if strings.Contains(logs, s) {
			t.Fatalf("auth service log contains a credential")
		}
	}
}

func flipChar(b byte) string {
	if b == 'A' {
		return "B"
	}
	return "A"
}
