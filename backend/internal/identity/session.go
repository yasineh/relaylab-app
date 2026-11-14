package identity

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"relaylab/internal/platform/config"
	"relaylab/internal/platform/db/store"
)

// Validation outcomes. Handlers map them to the error envelope.
var (
	// ErrUnauthenticated covers absent, malformed, tampered, unknown, and revoked sessions.
	ErrUnauthenticated = errors.New("unauthenticated")
	// ErrSessionExpired is a known session whose expiry time has passed.
	ErrSessionExpired = errors.New("session expired")
)

// Identity is the authenticated caller, derived only from a validated session.
type Identity struct {
	UserID        string
	Email         string
	Name          string
	EmailVerified bool
	// IsDemo marks a demo user (a Better Auth anonymous user). DemoExpiresAt
	// is when the demo workspace expires; zero for real users.
	IsDemo        bool
	DemoExpiresAt time.Time
	// ExpiresAt is the session's expiry at validation time. Better Auth may
	// extend it later, so long-lived requests re-validate instead of trusting it.
	ExpiresAt time.Time
}

// SessionStore is the read-only lookup Go performs against Better Auth's tables.
type SessionStore interface {
	GetSessionByToken(ctx context.Context, token string) (store.GetSessionByTokenRow, error)
}

// Validator checks Better Auth session cookies (design decision 2, option A).
// It never writes to the database and never refreshes sessions.
type Validator struct {
	cookieName string
	secret     []byte
	store      SessionStore
	now        func() time.Time
	demoTTL    time.Duration
}

// WithDemoTTL sets how long demo users live (DEMO_TTL). Their sessions are
// refused once the demo expires, even before the auth service deletes them.
func (v *Validator) WithDemoTTL(ttl time.Duration) *Validator {
	v.demoTTL = ttl
	return v
}

// NewValidator builds a validator for the environment's session cookie name.
func NewValidator(cfg *config.Auth, s SessionStore) *Validator {
	return &Validator{
		cookieName: cfg.CookieName,
		secret:     []byte(cfg.SessionSecret.Reveal()),
		store:      s,
		now:        time.Now,
		demoTTL:    24 * time.Hour,
	}
}

const (
	maxCookieLen = 256
	sigLen       = sha256.Size // HMAC-SHA256
)

// Better Auth 1.7.23 tokens are 32 alphanumeric characters; accept a slightly wider,
// still strictly bounded alphabet so a format tweak fails the contract test, not users.
var tokenPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`)

// parseCookie splits the cookie value into token and signature and verifies the
// signature in constant time. Spike 1.1 confirmed the format: the URL-encoded value
// is `<token>.<base64.StdEncoding(HMAC-SHA256(secret, token))>`.
func (v *Validator) parseCookie(raw string) (string, bool) {
	if raw == "" || len(raw) > maxCookieLen {
		return "", false
	}
	// PathUnescape, not QueryUnescape: a literal '+' must stay '+' (as decodeURIComponent does).
	value, err := url.PathUnescape(raw)
	if err != nil {
		return "", false
	}
	dot := strings.LastIndexByte(value, '.')
	if dot <= 0 || dot == len(value)-1 {
		return "", false
	}
	token, sigText := value[:dot], value[dot+1:]
	if !tokenPattern.MatchString(token) {
		return "", false
	}
	sig, err := base64.StdEncoding.Strict().DecodeString(sigText)
	if err != nil || len(sig) != sigLen {
		return "", false
	}
	mac := hmac.New(sha256.New, v.secret)
	mac.Write([]byte(token))
	if !hmac.Equal(mac.Sum(nil), sig) {
		return "", false
	}
	return token, true
}

// Validate authenticates r from its session cookie. Errors other than
// ErrUnauthenticated and ErrSessionExpired are infrastructure failures.
func (v *Validator) Validate(ctx context.Context, r *http.Request) (Identity, error) {
	cookies := r.CookiesNamed(v.cookieName)
	if len(cookies) == 0 {
		return Identity{}, ErrUnauthenticated
	}
	// Refuse ambiguous requests carrying different values for the session cookie.
	for _, c := range cookies[1:] {
		if c.Value != cookies[0].Value {
			return Identity{}, ErrUnauthenticated
		}
	}
	token, ok := v.parseCookie(cookies[0].Value)
	if !ok {
		// Tampered or malformed: rejected without touching the database.
		return Identity{}, ErrUnauthenticated
	}
	row, err := v.store.GetSessionByToken(ctx, token)
	if errors.Is(err, pgx.ErrNoRows) {
		// Unknown or revoked (sign-out, sign-out everywhere, password change delete the row).
		return Identity{}, ErrUnauthenticated
	}
	if err != nil {
		return Identity{}, fmt.Errorf("session lookup: %w", err)
	}
	if !row.ExpiresAt.Valid || !row.ExpiresAt.Time.After(v.now()) {
		return Identity{}, ErrSessionExpired
	}
	id := Identity{
		UserID:        row.UserID,
		Email:         row.Email,
		Name:          row.Name,
		EmailVerified: row.EmailVerified,
		ExpiresAt:     row.ExpiresAt.Time,
	}
	if row.IsDemo {
		id.IsDemo = true
		id.DemoExpiresAt = row.UserCreatedAt.Time.Add(v.demoTTL)
		if !row.UserCreatedAt.Valid || !id.DemoExpiresAt.After(v.now()) {
			return Identity{}, ErrSessionExpired // the demo is over; the purge deletes the user
		}
	}
	return id, nil
}

type identityKey struct{}

// WithIdentity stores the authenticated identity in ctx.
func WithIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, identityKey{}, id)
}

// FromContext returns the authenticated identity. Handlers must take the acting
// user only from here, never from request bodies, query strings, or headers.
func FromContext(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(identityKey{}).(Identity)
	return id, ok
}
