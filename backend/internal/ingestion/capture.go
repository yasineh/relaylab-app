// Package ingestion receives webhook requests at public endpoint URLs
// (/h/{token}[/subpath]) and stores each accepted request as an event before
// acknowledging it. It is RelayLab's only unauthenticated write path: it never
// reads sessions or cookies, and all inbound data is treated as untrusted.
package ingestion

import (
	"net/http"
	"net/netip"
	"sort"
	"strings"
	"unicode/utf8"

	"relaylab/internal/platform/redact"
)

// Field limits that mirror the webhook_events CHECK constraints.
const (
	maxPathChars        = 2048
	maxRawQueryChars    = 8192
	maxContentTypeChars = 255
	maxUserAgentChars   = 512
)

// captured is the redacted, size-checked request data ingestion persists. It
// never holds an original sensitive header or query value.
type captured struct {
	Method              string
	Path                string // subpath after the token, "" for none
	RawQuery            string // sensitive values replaced
	Params              map[string][]string
	Headers             [][2]string // sorted by name, duplicates in arrival order, redacted
	RedactedHeaderCount int
	ContentType         *string
	UserAgent           *string
	SourceIPPrefix      *netip.Addr
}

// headerList returns every received header as [name, value] pairs sorted by
// canonical name; repeated values of one name keep their arrival order. Go's
// server does not expose the order across different names (design: Header
// order). Host is included because net/http moves it out of r.Header.
func headerList(r *http.Request) [][2]string {
	out := make([][2]string, 0, len(r.Header)+1)
	for name, values := range r.Header {
		for _, v := range values {
			out = append(out, [2]string{name, v})
		}
	}
	if r.Host != "" {
		out = append(out, [2]string{"Host", r.Host})
	}
	// Stable: values of one name stay in arrival order.
	sort.SliceStable(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return out
}

// headerFieldCount counts header fields as received (each repeated value counts).
func headerFieldCount(r *http.Request) int {
	n := 0
	for _, vs := range r.Header {
		n += len(vs)
	}
	if r.Host != "" {
		n++ // Host, which net/http keeps out of r.Header
	}
	return n
}

// capture extracts and redacts the request metadata. subpath and
// escapedSubpath are the decoded and percent-encoded parts of the path after
// the token. ok is false when the path or query exceeds the
// stored limits (the caller responds 414).
func capture(r *http.Request, subpath, escapedSubpath string, red *redact.Redactor, clientIP netip.Addr, haveIP bool) (captured, bool) {
	c := captured{Method: r.Method}

	c.Path = textSafe(subpath, escapedSubpath)
	if utf8.RuneCountInString(c.Path) > maxPathChars || utf8.RuneCountInString(r.URL.RawQuery) > maxRawQueryChars {
		return captured{}, false
	}

	rawQuery, params, _ := red.Query(r.URL.RawQuery)
	c.RawQuery = rawQuery
	c.Params = make(map[string][]string, len(params))
	for _, p := range params {
		name := jsonSafe(p.Name)
		c.Params[name] = append(c.Params[name], jsonSafe(p.Value))
	}

	c.Headers, c.RedactedHeaderCount = red.Headers(headerList(r))

	if ct := r.Header.Get("Content-Type"); ct != "" {
		v := truncateChars(jsonSafe(ct), maxContentTypeChars)
		c.ContentType = &v
	}
	if ua := r.Header.Get("User-Agent"); ua != "" {
		v := truncateChars(jsonSafe(ua), maxUserAgentChars)
		c.UserAgent = &v
	}
	if haveIP {
		p := truncateIP(clientIP)
		c.SourceIPPrefix = &p
	}
	return c, true
}

// truncateIP keeps the network part only: IPv4 /24, IPv6 /48.
func truncateIP(ip netip.Addr) netip.Addr {
	ip = ip.Unmap()
	bits := 48
	if ip.Is4() {
		bits = 24
	}
	p, err := ip.Prefix(bits)
	if err != nil {
		return netip.Addr{}
	}
	return p.Addr()
}

// textSafe returns decoded when PostgreSQL text can store it unchanged (valid
// UTF-8, no NUL), otherwise the percent-encoded form, so evidence is kept
// without altering bytes.
func textSafe(decoded, escaped string) string {
	if utf8.ValidString(decoded) && !strings.ContainsRune(decoded, 0) {
		return decoded
	}
	return escaped
}

// jsonSafe makes a string storable in PostgreSQL text and jsonb, which reject
// NUL and invalid UTF-8: invalid bytes and NUL become U+FFFD. The raw query
// keeps the original percent-encoded form.
func jsonSafe(s string) string {
	s = strings.ToValidUTF8(s, "�")
	return strings.ReplaceAll(s, "\x00", "�")
}

func truncateChars(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	runes := []rune(s)
	return string(runes[:n])
}
