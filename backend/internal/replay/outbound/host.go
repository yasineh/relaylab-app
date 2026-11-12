package outbound

import (
	"net/netip"
	"strconv"
	"strings"
)

// target is a canonical destination host: an IP literal or a DNS name.
type target struct {
	name string     // lower-case, no trailing dot; empty for IP literals
	ip   netip.Addr // valid for IP literals
}

func (t target) isIP() bool { return t.ip.IsValid() }

const maxHostLen = 253

// canonicalHost turns url.Hostname() into a target, or fails with
// invalid_url. Numeric hosts are accepted only in their canonical form:
// a strict dotted quad, or an IPv6 literal. Any host a resolver or browser
// could read as a number in another spelling (2130706433, 0x7f.1, 0177.0.0.1,
// 127.1, 1.2.3.0x4, example.123) is rejected rather than interpreted, so no
// spelling can reach an address the policy would block.
func canonicalHost(raw string) (target, *Error) {
	h := strings.ToLower(raw)
	if h == "" || len(h) > maxHostLen+1 {
		return target{}, fail(InvalidURL)
	}
	if strings.Contains(h, ":") { // url.Hostname has removed the brackets
		a, err := netip.ParseAddr(h)
		if err != nil {
			return target{}, fail(InvalidURL)
		}
		return target{ip: a}, nil
	}
	h = strings.TrimSuffix(h, ".")
	if h == "" || strings.HasSuffix(h, ".") || len(h) > maxHostLen {
		return target{}, fail(InvalidURL)
	}
	labels := strings.Split(h, ".")
	numeric := 0
	for _, l := range labels {
		if isNumberToken(l) {
			numeric++
		}
	}
	if numeric == len(labels) || isNumberToken(labels[len(labels)-1]) {
		a, ok := strictDottedQuad(labels)
		if !ok {
			return target{}, fail(InvalidURL)
		}
		return target{ip: a}, nil
	}
	for _, l := range labels {
		if !validLabel(l) {
			return target{}, fail(InvalidURL)
		}
	}
	return target{name: h}, nil
}

// isNumberToken reports whether a label is a number in any notation that
// inet_aton or the WHATWG URL parser accepts: decimal, octal (leading 0),
// or hexadecimal (0x prefix, possibly with no digits).
func isNumberToken(l string) bool {
	if l == "" {
		return false
	}
	if strings.HasPrefix(l, "0x") {
		for _, c := range l[2:] {
			if !strings.ContainsRune("0123456789abcdef", c) {
				return false
			}
		}
		return true
	}
	for _, c := range l {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// strictDottedQuad accepts exactly four decimal parts, each 0-255 with no
// leading zeros.
func strictDottedQuad(labels []string) (netip.Addr, bool) {
	if len(labels) != 4 {
		return netip.Addr{}, false
	}
	var b [4]byte
	for i, l := range labels {
		if l == "" || len(l) > 3 || (len(l) > 1 && l[0] == '0') {
			return netip.Addr{}, false
		}
		n, err := strconv.Atoi(l)
		if err != nil || n > 255 {
			return netip.Addr{}, false
		}
		b[i] = byte(n) //nolint:gosec // n is 0-255, checked above
	}
	return netip.AddrFrom4(b), true
}

// validLabel accepts ASCII letters, digits, hyphen, and underscore (some
// providers use underscores), 1-63 characters, not starting or ending with a
// hyphen. Internationalized names must be given in punycode (xn--).
func validLabel(l string) bool {
	if l == "" || len(l) > 63 || l[0] == '-' || l[len(l)-1] == '-' {
		return false
	}
	for _, c := range l {
		ok := c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_'
		if !ok {
			return false
		}
	}
	return true
}
