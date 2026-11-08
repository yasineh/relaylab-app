package identity

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"relaylab/internal/platform/config"
	"relaylab/internal/platform/db/store"
	"relaylab/internal/platform/httpx"
)

//nolint:gosec // G101: test-only value.
const testSessionSecret = "test-only-better-auth-secret-0123456789abcdef"

const validToken = "OSsmUnAfkn3ueRXSDK8HJVOqRyNbSGO7" // 32 chars, Better Auth 1.7.18 shape

var testNow = time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)

type fakeStore struct {
	rows  map[string]store.GetSessionByTokenRow
	err   error
	calls int
}

func (f *fakeStore) GetSessionByToken(_ context.Context, token string) (store.GetSessionByTokenRow, error) {
	f.calls++
	if f.err != nil {
		return store.GetSessionByTokenRow{}, f.err
	}
	row, ok := f.rows[token]
	if !ok {
		return store.GetSessionByTokenRow{}, pgx.ErrNoRows
	}
	return row, nil
}

func sign(secret, token string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(token))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// cookieValue encodes like Better Auth: encodeURIComponent(token + "." + signature).
func cookieValue(secret, token string) string {
	return url.QueryEscape(token + "." + sign(secret, token))
}

func row(userID string, expires time.Time, verified bool) store.GetSessionByTokenRow {
	return store.GetSessionByTokenRow{
		UserID:        userID,
		ExpiresAt:     pgtype.Timestamptz{Time: expires, Valid: true},
		Email:         userID + "@example.test",
		Name:          "User " + userID,
		EmailVerified: verified,
	}
}

func newTestValidator(s SessionStore) *Validator {
	v := NewValidator(&config.Auth{
		CookieName:    config.SessionCookieDev,
		SessionSecret: config.NewSecret(testSessionSecret),
	}, s)
	v.now = func() time.Time { return testNow }
	return v
}

func reqWithCookie(name, value string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	if name != "" {
		r.Header.Set("Cookie", name+"="+value)
	}
	return r
}

func TestValidatorDecisions(t *testing.T) {
	good := cookieValue(testSessionSecret, validToken)
	tamperedTokenValue := cookieValue(testSessionSecret, validToken)
	tamperedToken := "X" + tamperedTokenValue[1:] // one character of the token changed
	sigStart := strings.Index(good, ".") + 1
	tamperedSig := good[:sigStart] + flip(good[sigStart]) + good[sigStart+1:]

	cases := []struct {
		name      string
		cookie    string // full Cookie header value; "" = none
		rows      map[string]store.GetSessionByTokenRow
		want      error
		wantQuery bool
	}{
		{name: "absent", cookie: "", want: ErrUnauthenticated},
		{name: "other cookie only", cookie: "theme=dark", want: ErrUnauthenticated},
		{name: "empty value", cookie: config.SessionCookieDev + "=", want: ErrUnauthenticated},
		{name: "no signature", cookie: config.SessionCookieDev + "=" + validToken, want: ErrUnauthenticated},
		{name: "empty token", cookie: config.SessionCookieDev + "=." + url.QueryEscape(sign(testSessionSecret, "")), want: ErrUnauthenticated},
		{name: "bad base64", cookie: config.SessionCookieDev + "=" + validToken + ".%21%21%21", want: ErrUnauthenticated},
		{name: "short signature", cookie: config.SessionCookieDev + "=" + validToken + "." + url.QueryEscape(base64.StdEncoding.EncodeToString([]byte("short"))), want: ErrUnauthenticated},
		{name: "base64url signature", cookie: config.SessionCookieDev + "=" + validToken + "." + base64.RawURLEncoding.EncodeToString(hmacBytes(testSessionSecret, validToken)), want: ErrUnauthenticated},
		{name: "bad escape", cookie: config.SessionCookieDev + "=%zz", want: ErrUnauthenticated},
		{name: "oversized", cookie: config.SessionCookieDev + "=" + strings.Repeat("a", 300), want: ErrUnauthenticated},
		{name: "wrong secret", cookie: config.SessionCookieDev + "=" + cookieValue("another-secret-another-secret-0000", validToken), want: ErrUnauthenticated},
		{name: "tampered token", cookie: config.SessionCookieDev + "=" + tamperedToken, want: ErrUnauthenticated},
		{name: "tampered signature", cookie: config.SessionCookieDev + "=" + tamperedSig, want: ErrUnauthenticated},
		{name: "production cookie name in development", cookie: config.SessionCookieProd + "=" + good, want: ErrUnauthenticated},
		{
			name: "conflicting duplicate cookies", cookie: config.SessionCookieDev + "=" + good + "; " + config.SessionCookieDev + "=" + cookieValue(testSessionSecret, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"),
			want: ErrUnauthenticated,
		},
		{name: "unknown or revoked", cookie: config.SessionCookieDev + "=" + good, want: ErrUnauthenticated, wantQuery: true},
		{
			name: "expired", cookie: config.SessionCookieDev + "=" + good,
			rows: map[string]store.GetSessionByTokenRow{validToken: row("u1", testNow.Add(-time.Second), true)},
			want: ErrSessionExpired, wantQuery: true,
		},
		{
			name: "expires exactly now", cookie: config.SessionCookieDev + "=" + good,
			rows: map[string]store.GetSessionByTokenRow{validToken: row("u1", testNow, true)},
			want: ErrSessionExpired, wantQuery: true,
		},
		{
			name: "valid", cookie: config.SessionCookieDev + "=" + good,
			rows: map[string]store.GetSessionByTokenRow{validToken: row("u1", testNow.Add(time.Hour), true)},
			want: nil, wantQuery: true,
		},
		{
			name: "valid, unescaped value", cookie: config.SessionCookieDev + "=" + validToken + "." + sign(testSessionSecret, validToken),
			rows: map[string]store.GetSessionByTokenRow{validToken: row("u1", testNow.Add(time.Hour), true)},
			want: nil, wantQuery: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs := &fakeStore{rows: tc.rows}
			v := newTestValidator(fs)
			r := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
			if tc.cookie != "" {
				r.Header.Set("Cookie", tc.cookie)
			}
			id, err := v.Validate(context.Background(), r)
			if !errors.Is(err, tc.want) || (tc.want == nil && err != nil) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if (fs.calls > 0) != tc.wantQuery {
				t.Errorf("store queried %d times, wantQuery=%v", fs.calls, tc.wantQuery)
			}
			if tc.want == nil && (id.UserID != "u1" || !id.EmailVerified || id.Email != "u1@example.test") {
				t.Errorf("identity = %+v", id)
			}
		})
	}
}

func TestValidatorStoreFailure(t *testing.T) {
	v := newTestValidator(&fakeStore{err: errors.New("connection refused")})
	_, err := v.Validate(context.Background(), reqWithCookie(config.SessionCookieDev, cookieValue(testSessionSecret, validToken)))
	if err == nil || errors.Is(err, ErrUnauthenticated) || errors.Is(err, ErrSessionExpired) {
		t.Fatalf("err = %v, want infrastructure error", err)
	}
}

func TestProductionCookieName(t *testing.T) {
	fs := &fakeStore{rows: map[string]store.GetSessionByTokenRow{validToken: row("u1", testNow.Add(time.Hour), true)}}
	v := NewValidator(&config.Auth{CookieName: config.SessionCookieProd, SessionSecret: config.NewSecret(testSessionSecret)}, fs)
	v.now = func() time.Time { return testNow }
	good := cookieValue(testSessionSecret, validToken)
	if _, err := v.Validate(context.Background(), reqWithCookie(config.SessionCookieProd, good)); err != nil {
		t.Fatalf("prod cookie: %v", err)
	}
	if _, err := v.Validate(context.Background(), reqWithCookie(config.SessionCookieDev, good)); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("dev cookie accepted in production: %v", err)
	}
}

func TestRequireSessionResponses(t *testing.T) {
	good := cookieValue(testSessionSecret, validToken)
	cases := []struct {
		name        string
		cookie      string
		row         *store.GetSessionByTokenRow
		storeErr    error
		requireVer  bool
		wantStatus  int
		wantCode    string
		wantHandler bool
	}{
		{name: "missing cookie", wantStatus: 401, wantCode: "unauthenticated"},
		{name: "tampered", cookie: "Z" + good[1:], wantStatus: 401, wantCode: "unauthenticated"},
		{name: "expired", cookie: good, row: ptr(row("u1", testNow.Add(-time.Minute), true)), wantStatus: 401, wantCode: "session_expired"},
		{name: "unverified, verification required", cookie: good, row: ptr(row("u1", testNow.Add(time.Hour), false)), requireVer: true, wantStatus: 403, wantCode: "email_not_verified"},
		{name: "unverified, verification off", cookie: good, row: ptr(row("u1", testNow.Add(time.Hour), false)), wantStatus: 200, wantHandler: true},
		{name: "store down", cookie: good, storeErr: errors.New("boom"), wantStatus: 500, wantCode: "internal_error"},
		{name: "valid", cookie: good, row: ptr(row("u1", testNow.Add(time.Hour), true)), requireVer: true, wantStatus: 200, wantHandler: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs := &fakeStore{rows: map[string]store.GetSessionByTokenRow{}, err: tc.storeErr}
			if tc.row != nil {
				fs.rows[validToken] = *tc.row
			}
			v := newTestValidator(fs)
			called := false
			h := httpx.RequestIDMiddleware(discardLogger())(RequireSession(v, tc.requireVer)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				if id, ok := FromContext(r.Context()); !ok || id.UserID != "u1" {
					t.Errorf("identity missing from context: %+v", id)
				}
				w.WriteHeader(http.StatusOK)
			})))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, reqWithCookie(cookieNameIf(tc.cookie), tc.cookie))
			if rec.Code != tc.wantStatus || called != tc.wantHandler {
				t.Fatalf("status=%d called=%v body=%s", rec.Code, called, rec.Body)
			}
			if tc.wantCode != "" {
				var body httpx.ErrorBody
				if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Error.Code != tc.wantCode || body.Error.RequestID == "" {
					t.Errorf("body = %s", rec.Body)
				}
				if strings.Contains(rec.Body.String(), "boom") {
					t.Error("internal error detail leaked")
				}
			}
		})
	}
}

func TestMeReturnsSessionUserAndIgnoresForgedHeaders(t *testing.T) {
	good := cookieValue(testSessionSecret, validToken)
	fs := &fakeStore{rows: map[string]store.GetSessionByTokenRow{validToken: row("u1", testNow.Add(time.Hour), true)}}
	h := RequireSession(newTestValidator(fs), true)(http.HandlerFunc(Me))

	r := reqWithCookie(config.SessionCookieDev, good)
	r.Header.Set("X-User-Id", "u2")
	r.URL.RawQuery = "user_id=u2"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var got map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	want := map[string]any{"id": "u1", "email": "u1@example.test", "name": "User u1", "email_verified": true}
	if len(got) != len(want) {
		t.Fatalf("body = %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, want %v", k, got[k], v)
		}
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Error("me response must not be cached")
	}
}

func hmacBytes(secret, token string) []byte {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(token))
	return mac.Sum(nil)
}

func flip(b byte) string {
	if b == 'A' {
		return "B"
	}
	return "A"
}

func ptr[T any](v T) *T { return &v }

func cookieNameIf(v string) string {
	if v == "" {
		return ""
	}
	return config.SessionCookieDev
}
