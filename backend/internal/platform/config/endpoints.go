package config

import (
	"encoding/base64"
	"errors"
	"net/url"
	"strings"
)

// Endpoints is the API-only configuration for webhook endpoints (D3, D4).
type Endpoints struct {
	// IngestBaseURL prefixes receiving URLs: <IngestBaseURL>/h/<token>.
	// Defaults to APP_ORIGIN (one host serves the app and ingestion).
	IngestBaseURL *url.URL
	// TokenHMACKey derives the token lookup hash (32+ bytes).
	TokenHMACKey Secret
	// TokenEncKey encrypts tokens at rest with AES-256-GCM (exactly 32 bytes).
	TokenEncKey Secret
}

// HMACKey returns the decoded HMAC key bytes.
func (e *Endpoints) HMACKey() []byte { return []byte(e.TokenHMACKey.Reveal()) }

// EncKey returns the decoded encryption key bytes.
func (e *Endpoints) EncKey() []byte { return []byte(e.TokenEncKey.Reveal()) }

// LoadEndpoints reads INGEST_BASE_URL, ENDPOINT_TOKEN_HMAC_KEY, and
// ENDPOINT_TOKEN_ENC_KEY. Keys are base64 (standard or URL alphabet, padding
// optional); generate them with `openssl rand -base64 32`.
func LoadEndpoints(getenv Getenv, env Env, appOrigin *url.URL) (*Endpoints, error) {
	var errs []error
	fail := func(v, reason string) { errs = append(errs, &FieldError{Var: v, Reason: reason}) }
	e := &Endpoints{}

	raw := strings.TrimSpace(getenv("INGEST_BASE_URL"))
	if raw == "" {
		if appOrigin != nil {
			e.IngestBaseURL = appOrigin
		}
	} else if u, err := parseOrigin(raw); err != nil {
		fail("INGEST_BASE_URL", err.Error())
	} else if env == EnvProduction && u.Scheme != "https" {
		fail("INGEST_BASE_URL", "must use https in production")
	} else {
		e.IngestBaseURL = u
	}

	hmacKey, err := decodeKey(getenv("ENDPOINT_TOKEN_HMAC_KEY"))
	switch {
	case err != nil:
		fail("ENDPOINT_TOKEN_HMAC_KEY", err.Error())
	case len(hmacKey) < 32:
		fail("ENDPOINT_TOKEN_HMAC_KEY", "must decode to at least 32 bytes")
	}
	encKey, err := decodeKey(getenv("ENDPOINT_TOKEN_ENC_KEY"))
	switch {
	case err != nil:
		fail("ENDPOINT_TOKEN_ENC_KEY", err.Error())
	case len(encKey) != 32:
		fail("ENDPOINT_TOKEN_ENC_KEY", "must decode to exactly 32 bytes (AES-256)")
	}
	if len(hmacKey) > 0 && string(hmacKey) == string(encKey) {
		fail("ENDPOINT_TOKEN_ENC_KEY", "must differ from ENDPOINT_TOKEN_HMAC_KEY")
	}
	e.TokenHMACKey = NewSecret(string(hmacKey))
	e.TokenEncKey = NewSecret(string(encKey))

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return e, nil
}

func decodeKey(raw string) ([]byte, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("is required (generate with: openssl rand -base64 32)")
	}
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(raw); err == nil {
			return b, nil
		}
	}
	return nil, errors.New("must be base64 (generate with: openssl rand -base64 32)")
}
