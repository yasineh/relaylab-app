package config

import (
	"errors"
	"net/url"
	"strings"
)

// Session cookie names set by the auth service (Better Auth 1.7.23 with
// cookiePrefix "relaylab"; confirmed by add-user-auth spike 1.1).
const (
	SessionCookieDev  = "relaylab.session_token"
	SessionCookieProd = "__Secure-relaylab.session_token"
)

// minSecretLen matches the auth service's minimum for both shared secrets.
const minSecretLen = 32

// Auth is the API-only configuration for authentication. The worker never
// loads it, so it never holds the session secret.
type Auth struct {
	// SessionSecret is BETTER_AUTH_SECRET: verifies session cookie signatures.
	SessionSecret Secret
	// ProxySharedSecret is sent to the auth service on every proxied request.
	ProxySharedSecret Secret
	// ServiceURL is the internal auth service base URL (AUTH_SERVICE_URL).
	ServiceURL *url.URL
	// SignupEnabled mirrors AUTH_SIGNUP_ENABLED for GET /api/v1/config.
	SignupEnabled bool
	// RequireEmailVerification makes unverified users get 403 email_not_verified.
	RequireEmailVerification bool
	// CookieName is the only session cookie name accepted in this environment.
	CookieName string
}

// LoadAuth reads the authentication settings for env. Defaults match the auth
// service: sign-up on outside production and off in production; verification
// required everywhere unless explicitly disabled.
func LoadAuth(getenv Getenv, env Env) (*Auth, error) {
	var errs []error
	fail := func(v, reason string) { errs = append(errs, &FieldError{Var: v, Reason: reason}) }
	prod := env == EnvProduction
	a := &Auth{CookieName: SessionCookieDev}
	if prod {
		a.CookieName = SessionCookieProd
	}

	sessionSecret := strings.TrimSpace(getenv("BETTER_AUTH_SECRET"))
	if len(sessionSecret) < minSecretLen {
		fail("BETTER_AUTH_SECRET", "must be at least 32 characters")
	}
	proxySecret := strings.TrimSpace(getenv("AUTH_PROXY_SHARED_SECRET"))
	if len(proxySecret) < minSecretLen {
		fail("AUTH_PROXY_SHARED_SECRET", "must be at least 32 characters")
	} else if proxySecret == sessionSecret {
		fail("AUTH_PROXY_SHARED_SECRET", "must differ from BETTER_AUTH_SECRET")
	}
	a.SessionSecret = NewSecret(sessionSecret)
	a.ProxySharedSecret = NewSecret(proxySecret)

	raw := strings.TrimSpace(getenv("AUTH_SERVICE_URL"))
	if raw == "" && !prod {
		raw = "http://localhost:3001"
	}
	if raw == "" {
		fail("AUTH_SERVICE_URL", "is required in production")
	} else if u, err := url.Parse(raw); err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.User != nil {
		fail("AUTH_SERVICE_URL", "must be an http(s) base URL without path, query, or credentials")
	} else {
		u.Path = ""
		a.ServiceURL = u
	}

	var err error
	if a.SignupEnabled, err = parseBool(getenv("AUTH_SIGNUP_ENABLED"), !prod); err != nil {
		fail("AUTH_SIGNUP_ENABLED", err.Error())
	}
	if a.RequireEmailVerification, err = parseBool(getenv("AUTH_REQUIRE_EMAIL_VERIFICATION"), true); err != nil {
		fail("AUTH_REQUIRE_EMAIL_VERIFICATION", err.Error())
	}
	// Public sign-up in production is a deliberate decision (D16): it needs an
	// explicit acknowledgement and email verification.
	if prod && a.SignupEnabled {
		if ack, _ := parseBool(getenv("AUTH_ALLOW_PUBLIC_SIGNUP_ACK"), false); !ack {
			fail("AUTH_SIGNUP_ENABLED", "public sign-up in production also needs AUTH_ALLOW_PUBLIC_SIGNUP_ACK=true")
		}
		if !a.RequireEmailVerification {
			fail("AUTH_REQUIRE_EMAIL_VERIFICATION", "must be true in production while sign-up is enabled")
		}
	}

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return a, nil
}

func parseBool(raw string, def bool) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		return def, nil
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	default:
		return def, errors.New("must be true or false")
	}
}
