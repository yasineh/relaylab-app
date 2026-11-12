// Package outbound is the only way RelayLab sends user-directed HTTP requests
// (replay). It lets requests reach public internet destinations only: every
// URL is validated, every resolved address and the actual socket address are
// checked against ippolicy, redirects are not followed, and time, size, and
// concurrency are bounded (outbound-request-guard spec).
package outbound

// Category is the outcome of one guarded request: Succeeded, or exactly one
// failure category. Values are stable; the UI and replay history store them.
type Category string

const (
	Succeeded        Category = "succeeded"
	InvalidURL       Category = "invalid_url"
	BlockedScheme    Category = "blocked_scheme"
	BlockedPort      Category = "blocked_port"
	BlockedHost      Category = "blocked_host"
	BlockedAddress   Category = "blocked_address"
	DNSFailure       Category = "dns_failure"
	ConnectFailure   Category = "connect_failure"
	TLSFailure       Category = "tls_failure"
	Timeout          Category = "timeout"
	ResponseTooLarge Category = "response_too_large"
	CapacityExceeded Category = "capacity_exceeded"
	InvalidRequest   Category = "invalid_request"
	GuardInactive    Category = "guard_inactive"
)

// messages are the only user-visible failure texts. They never contain
// resolved addresses, internal host names, or raw error strings.
var messages = map[Category]string{
	InvalidURL:       "The destination URL is not valid. Use an absolute https:// URL with a host name and no credentials.",
	BlockedScheme:    "Only https:// destinations are allowed.",
	BlockedPort:      "This destination port is not allowed.",
	BlockedHost:      "This host name is internal or reserved and cannot be used as a destination.",
	BlockedAddress:   "The destination is, or resolves to, a non-public address.",
	DNSFailure:       "The destination host name could not be resolved.",
	ConnectFailure:   "A connection to the destination could not be established.",
	TLSFailure:       "The destination's TLS certificate or handshake was not valid.",
	Timeout:          "The destination did not respond in time.",
	ResponseTooLarge: "The destination's response headers were too large.",
	CapacityExceeded: "Too many outbound requests are in progress. Try again shortly.",
	InvalidRequest:   "The request contains a header that is not allowed or a control character.",
	GuardInactive:    "Outbound requests are disabled because the safety self-check did not pass.",
}

// Error is a guarded-request failure. Message is safe to show to users;
// internal details are never included.
type Error struct {
	Category Category
}

func (e *Error) Error() string { return string(e.Category) }

// Message returns the user-visible text for the category.
func (e *Error) Message() string { return messages[e.Category] }

func fail(c Category) *Error { return &Error{Category: c} }
