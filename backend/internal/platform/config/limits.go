package config

import (
	"errors"
	"strconv"
	"strings"
)

// Limits holds every product limit (D17). Backend code must read limits only
// from this struct; a test fails if a known limit value is hard-coded elsewhere.
// Later changes add their keys to limitSpecs with a documented default.
type Limits struct {
	// HTTPMaxHeaderBytes caps the total size of request headers (431 when exceeded).
	HTTPMaxHeaderBytes int
	// APIMaxBodyBytes caps dashboard JSON request bodies (413 when exceeded).
	// It also caps bodies proxied to the auth service under /api/auth.
	APIMaxBodyBytes int
	// AuthProxyTimeoutSeconds bounds a proxied /api/auth request end to end (502/504 when exceeded).
	AuthProxyTimeoutSeconds int
}

type limitSpec struct {
	env  string
	def  int
	doc  string
	bind func(*Limits) *int
}

// limitSpecs is the single registry of limits, their env overrides, and defaults.
var limitSpecs = []limitSpec{
	{
		env:  "LIMIT_HTTP_MAX_HEADER_BYTES",
		def:  32 << 10,
		doc:  "Maximum total request header size in bytes",
		bind: func(l *Limits) *int { return &l.HTTPMaxHeaderBytes },
	},
	{
		env:  "LIMIT_API_MAX_BODY_BYTES",
		def:  64 << 10,
		doc:  "Maximum dashboard API JSON body size in bytes",
		bind: func(l *Limits) *int { return &l.APIMaxBodyBytes },
	},
	{
		env:  "LIMIT_AUTH_PROXY_TIMEOUT_SECONDS",
		def:  10,
		doc:  "Maximum time for a request proxied to the auth service, in seconds",
		bind: func(l *Limits) *int { return &l.AuthProxyTimeoutSeconds },
	},
}

// DefaultLimits returns the documented defaults.
func DefaultLimits() Limits {
	var l Limits
	for _, s := range limitSpecs {
		*s.bind(&l) = s.def
	}
	return l
}

// LoadLimits applies LIMIT_* overrides on top of the defaults. Each override
// must be a positive integer; errors name the variable, never the value.
func LoadLimits(getenv Getenv) (Limits, error) {
	l := DefaultLimits()
	var errs []error
	for _, s := range limitSpecs {
		raw := strings.TrimSpace(getenv(s.env))
		if raw == "" {
			continue
		}
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			errs = append(errs, &FieldError{Var: s.env, Reason: "must be a positive integer"})
			continue
		}
		*s.bind(&l) = n
	}
	if len(errs) > 0 {
		return Limits{}, errors.Join(errs...)
	}
	return l, nil
}

// LimitDoc describes one limit for documentation output.
type LimitDoc struct {
	Env     string
	Default int
	Doc     string
}

// LimitDocs lists all limits with defaults, for docs generation and tests.
func LimitDocs() []LimitDoc {
	out := make([]LimitDoc, 0, len(limitSpecs))
	for _, s := range limitSpecs {
		out = append(out, LimitDoc{Env: s.env, Default: s.def, Doc: s.doc})
	}
	return out
}
