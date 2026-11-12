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

func strippedHeader(lower string) bool {
	return strippedHeaders[lower] || strings.HasPrefix(lower, "proxy-") || strings.HasPrefix(lower, "x-forwarded-")
}

// outboundHeader returns the headers to send. A name or value containing CR,
// LF, or NUL, or a name that is not an HTTP token, fails the whole request
// with invalid_request: nothing is sent.
func (c *Client) outboundHeader(in http.Header) (http.Header, *Error) {
	out := http.Header{}
	for name, values := range in {
		if !isToken(name) {
			return nil, fail(InvalidRequest)
		}
		for _, v := range values {
			if strings.ContainsAny(v, "\r\n\x00") {
				return nil, fail(InvalidRequest)
			}
		}
		if strippedHeader(strings.ToLower(name)) {
			continue
		}
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
