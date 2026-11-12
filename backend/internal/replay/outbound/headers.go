package outbound

import (
	"net/http"
	"strings"
)

// strippedHeaders are never forwarded from the caller (spec "Outbound header
// hygiene"): credentials, hop-by-hop and framing headers, and headers that
// claim client identity. Host and Content-Length are computed by the client.
// Accept-Encoding is dropped so the transport negotiates gzip itself and the
// response cap applies to decompressed bytes. User-Agent is always ours.
var strippedHeaders = map[string]bool{
	"authorization": true, "proxy-authorization": true, "cookie": true, "set-cookie": true,
	"host": true, "connection": true, "keep-alive": true, "transfer-encoding": true, "te": true,
	"trailer": true, "upgrade": true, "forwarded": true, "x-real-ip": true, "content-length": true,
	"accept-encoding": true, "user-agent": true,
}

// Stripped reports whether the client drops this header when it comes from
// derived (non-explicit) headers.
func Stripped(name string) bool { return strippedHeader(strings.ToLower(name)) }

func strippedHeader(lower string) bool {
	return strippedHeaders[lower] || strings.HasPrefix(lower, "proxy-") || strings.HasPrefix(lower, "x-forwarded-")
}

// ExplicitForbidden reports whether a user may not set this header
// explicitly: everything stripped from derived headers except Authorization,
// which a user may add on purpose (encrypted at rest by replay).
func ExplicitForbidden(name string) bool {
	lower := strings.ToLower(name)
	return lower != "authorization" && strippedHeader(lower)
}

func validHeader(name string, values []string) bool {
	if !isToken(name) {
		return false
	}
	for _, v := range values {
		if strings.ContainsAny(v, "\r\n\x00") {
			return false
		}
	}
	return true
}

// outboundHeader returns the headers to send. A name or value containing CR,
// LF, or NUL, or a name that is not an HTTP token, fails the whole request
// with invalid_request: nothing is sent. So does a forbidden explicit header.
func (c *Client) outboundHeader(derived, explicit http.Header) (http.Header, *Error) {
	out := http.Header{}
	for name, values := range derived {
		if !validHeader(name, values) {
			return nil, fail(InvalidRequest)
		}
		if strippedHeader(strings.ToLower(name)) {
			continue
		}
		for _, v := range values {
			out.Add(name, v)
		}
	}
	for name, values := range explicit {
		if !validHeader(name, values) || ExplicitForbidden(name) {
			return nil, fail(InvalidRequest)
		}
		out.Del(name)
		for _, v := range values {
			out.Add(name, v)
		}
	}
	out.Set("User-Agent", c.o.UserAgent)
	return out, nil
}

// isToken reports whether s is a valid HTTP header field name (RFC 9110 token).
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
