// Package redact is RelayLab's single implementation of credential redaction
// (D9). Ingestion uses it to replace sensitive header and query values before
// an event is stored, logged, or published; the event explorer and replay use
// the same matcher, so there is exactly one definition of "sensitive".
//
// Values are replaced with Marker, never removed, so names, order, and
// duplicates stay inspectable. Configuration can add names but never remove
// the built-in rules.
package redact

import (
	"net/url"
	"strings"
)

// Marker replaces every sensitive value.
const Marker = "[REDACTED]"

// markerEscaped is Marker as it appears in a rewritten raw query string.
var markerEscaped = url.QueryEscape(Marker)

// Built-in rules. These are unexported and copied into each Redactor, so they
// cannot be changed at runtime.
var (
	// builtinHeaderNames are credential headers matched by exact (lowercased) name.
	builtinHeaderNames = []string{
		"authorization", "proxy-authorization", "cookie", "set-cookie",
		"x-api-key", "api-key", "x-auth-token", "x-access-token", "x-csrf-token",
		"x-amz-security-token", "x-goog-api-key",
	}
	// builtinSubstrings mark a header or query parameter as sensitive when its
	// lowercased name contains any of them. "hmac" covers provider signature
	// headers such as X-Shopify-Hmac-Sha256.
	builtinSubstrings = []string{
		"token", "secret", "password", "passwd", "api-key", "apikey", "api_key",
		"signature", "credential", "session", "hmac",
	}
	// builtinQueryNames are query parameters matched by exact (lowercased) name.
	builtinQueryNames = []string{"key", "code", "sig", "auth", "signature"}
)

// Redactor decides which header and query values are sensitive and replaces
// them. It is immutable after New and safe for concurrent use.
type Redactor struct {
	headerNames map[string]bool
	queryNames  map[string]bool
	substrings  []string
}

// New returns a Redactor with the built-in rules plus extra exact header and
// query parameter names (matched case-insensitively). Blank extras are ignored.
func New(extraHeaders, extraParams []string) *Redactor {
	r := &Redactor{
		headerNames: map[string]bool{},
		queryNames:  map[string]bool{},
		substrings:  append([]string(nil), builtinSubstrings...),
	}
	for _, n := range append(append([]string(nil), builtinHeaderNames...), extraHeaders...) {
		if n = strings.ToLower(strings.TrimSpace(n)); n != "" {
			r.headerNames[n] = true
		}
	}
	for _, n := range append(append([]string(nil), builtinQueryNames...), extraParams...) {
		if n = strings.ToLower(strings.TrimSpace(n)); n != "" {
			r.queryNames[n] = true
		}
	}
	return r
}

func (r *Redactor) hasSubstring(lower string) bool {
	for _, s := range r.substrings {
		if strings.Contains(lower, s) {
			return true
		}
	}
	return false
}

// SensitiveHeader reports whether a header's value must be redacted.
func (r *Redactor) SensitiveHeader(name string) bool {
	lower := strings.ToLower(name)
	return r.headerNames[lower] || r.hasSubstring(lower)
}

// SensitiveParam reports whether a query parameter's value must be redacted.
func (r *Redactor) SensitiveParam(name string) bool {
	lower := strings.ToLower(name)
	return r.queryNames[lower] || r.hasSubstring(lower)
}

// Headers returns a copy of headers with sensitive values replaced by Marker,
// keeping every name, position, and duplicate, plus the number replaced.
func (r *Redactor) Headers(headers [][2]string) ([][2]string, int) {
	out := make([][2]string, len(headers))
	n := 0
	for i, h := range headers {
		out[i] = h
		if r.SensitiveHeader(h[0]) {
			out[i][1] = Marker
			n++
		}
	}
	return out, n
}

// Param is one decoded query parameter occurrence.
type Param struct {
	Name  string
	Value string
}

// Query rewrites a raw query string so sensitive values become the escaped
// Marker, leaving every other byte as received. It also returns the decoded
// parameters in order (sensitive values replaced) and the number replaced.
//
// It never fails: pieces that are not valid percent-encoding are decoded as
// their literal text, and an empty piece (as in "a=1&&b=2") is kept in the raw
// query but yields no parameter.
func (r *Redactor) Query(raw string) (string, []Param, int) {
	if raw == "" {
		return "", nil, 0
	}
	pieces := strings.Split(raw, "&")
	params := make([]Param, 0, len(pieces))
	n := 0
	for i, piece := range pieces {
		if piece == "" {
			continue
		}
		rawName, rawValue, hasValue := strings.Cut(piece, "=")
		name := unescape(rawName)
		value := unescape(rawValue)
		if r.SensitiveParam(name) {
			// A bare name ("?token") has no value to hide.
			if hasValue {
				pieces[i] = rawName + "=" + markerEscaped
				value = Marker
				n++
			}
		}
		params = append(params, Param{Name: name, Value: value})
	}
	return strings.Join(pieces, "&"), params, n
}

func unescape(s string) string {
	if v, err := url.QueryUnescape(s); err == nil {
		return v
	}
	return s
}
