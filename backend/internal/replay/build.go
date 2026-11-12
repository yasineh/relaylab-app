// Package replay resends stored webhook events (add-event-replay): request
// construction, the API, and the worker's executor. Every delivery to a URL
// goes through outbound.Client; sink deliveries never open a socket.
package replay

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"relaylab/internal/platform/redact"
	"relaylab/internal/replay/outbound"
)

// ReplayIDHeader carries the replay id on every delivery, so destinations
// can de-duplicate (delivery is at least once).
const ReplayIDHeader = "X-Relaylab-Replay-Id"

// Override limits (event-replay spec).
const (
	MaxOverrides     = 20
	MaxOverrideValue = 4 << 10
)

// Methods a replay may use.
var Methods = []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}

// Override is one header the user adds explicitly.
type Override struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// StoredEvent is the part of a received event a replay re-sends.
type StoredEvent struct {
	Method  string
	Headers [][2]string // as stored: sensitive values already [REDACTED]
	Body    []byte
}

// SentHeader is one header as shown to users. Values of overrides and of
// sensitive names are shown as [REDACTED].
type SentHeader struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	Redacted bool   `json:"redacted"`
}

// Built is the request a replay sends, plus what to show about it.
type Built struct {
	Method   string
	Header   http.Header // from the event, after filtering
	Explicit http.Header // overrides and the replay id
	Body     []byte
	Sent     []SentHeader // in send order: forwarded headers, then overrides
	Stripped []string     // names from the event that are not forwarded
}

// signatureNames are provider signature headers. Names containing
// "signature" or "hmac" are matched too. They are stripped whatever the
// redaction configuration says, so a replay never carries a stale signature.
var signatureNames = map[string]bool{
	"stripe-signature": true, "x-hub-signature": true, "x-hub-signature-256": true, "x-signature": true,
	"svix-signature": true, "webhook-signature": true, "x-shopify-hmac-sha256": true,
	"x-twilio-signature": true, "x-slack-signature": true,
}

func isSignature(lower string) bool {
	return signatureNames[lower] || strings.Contains(lower, "signature") || strings.Contains(lower, "hmac")
}

// forwarded reports whether an original header is re-sent.
func forwarded(red *redact.Redactor, name, value string) bool {
	lower := strings.ToLower(name)
	switch {
	case outbound.Stripped(name), // hop-by-hop, forwarding, framing, credentials
		red.SensitiveHeader(name), // the central credential set
		value == redact.Marker,    // removed at receipt: only a marker is stored
		isSignature(lower),
		lower == strings.ToLower(ReplayIDHeader):
		return false
	}
	return true
}

// BuildRequest returns the request to send for ev. It is pure: preview and
// delivery call it with the same inputs and get the same request. The body
// is passed through byte for byte; the target URL is used as entered (the
// original path and query are not appended).
func BuildRequest(ev StoredEvent, method string, overrides []Override, replayID uuid.UUID, red *redact.Redactor) Built {
	b := Built{Method: method, Header: http.Header{}, Explicit: http.Header{}, Body: ev.Body}
	overridden := map[string]bool{}
	for _, o := range overrides {
		overridden[http.CanonicalHeaderKey(o.Name)] = true
	}
	seenStripped := map[string]bool{}
	for _, h := range ev.Headers {
		name, value := h[0], h[1]
		if !forwarded(red, name, value) {
			if key := http.CanonicalHeaderKey(name); !seenStripped[key] {
				seenStripped[key] = true
				b.Stripped = append(b.Stripped, name)
			}
			continue
		}
		if overridden[http.CanonicalHeaderKey(name)] {
			continue // replaced by the override
		}
		b.Header.Add(name, value)
		b.Sent = append(b.Sent, SentHeader{Name: name, Value: value})
	}
	for _, o := range overrides {
		b.Explicit.Add(o.Name, o.Value)
		b.Sent = append(b.Sent, SentHeader{Name: o.Name, Value: redact.Marker, Redacted: true})
	}
	b.Explicit.Set(ReplayIDHeader, replayID.String())
	b.Sent = append(b.Sent, SentHeader{Name: ReplayIDHeader, Value: replayID.String()})
	return b
}

// ValidateOverrides checks user-supplied headers (event-replay spec): at most
// MaxOverrides, token names, values up to MaxOverrideValue bytes, no CR, LF,
// or NUL, no duplicate names, and no names the user may not set (Cookie,
// Host, hop-by-hop, forwarding, the replay id). It returns a user-facing
// reason, or "" when valid.
func ValidateOverrides(overrides []Override) string {
	if len(overrides) > MaxOverrides {
		return fmt.Sprintf("Add at most %d headers.", MaxOverrides)
	}
	seen := map[string]bool{}
	for _, o := range overrides {
		key := http.CanonicalHeaderKey(o.Name)
		switch {
		case !isToken(o.Name):
			return "Header names may contain only letters, digits, and !#$%&'*+-.^_`|~."
		case strings.ContainsAny(o.Name+o.Value, "\r\n\x00"):
			return "Header names and values must not contain line breaks or NUL characters."
		case len(o.Value) > MaxOverrideValue:
			return fmt.Sprintf("Header values must be at most %d bytes.", MaxOverrideValue)
		case outbound.ExplicitForbidden(o.Name) || strings.EqualFold(o.Name, ReplayIDHeader):
			return o.Name + " cannot be set on a replay."
		case seen[key]:
			return "Each header name may be added once."
		}
		seen[key] = true
	}
	return ""
}

func isToken(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
			strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0
		if !ok {
			return false
		}
	}
	return true
}
