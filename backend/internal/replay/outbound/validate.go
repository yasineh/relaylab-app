package outbound

import (
	"net/netip"
	"net/url"
	"strconv"

	"relaylab/internal/replay/outbound/ippolicy"
)

// MaxURLBytes is the longest destination URL accepted (spec).
const MaxURLBytes = 2048

// policy is the pre-flight and dial-time destination policy.
type policy struct {
	allowHTTP bool
	ports     map[int]bool
	hosts     hostBlocklist
	// isPublic is ippolicy.IsPublic. Tests replace it to prove that the
	// self-check fails closed when the policy is broken.
	isPublic func(netip.Addr) bool
	// allow lets package tests reach exact loopback test servers. It is nil
	// outside tests and cannot be set from other packages.
	allow func(netip.AddrPort) bool
	// testAllow holds REPLAY_TEST_ALLOWLIST "host:port" entries (end-to-end
	// stack only; refused in production by config).
	testAllow map[string]bool
}

// testAllowed reports whether name:port is an allowlisted test destination.
func (p policy) testAllowed(name string, port int) bool {
	return len(p.testAllow) > 0 && p.testAllow[name+":"+strconv.Itoa(port)]
}

func (p policy) addrAllowed(a netip.Addr, port int) bool {
	if p.allow != nil && p.allow(netip.AddrPortFrom(a, uint16(port))) { //nolint:gosec // port validated 1-65535
		return true
	}
	return p.isPublic(a)
}

// destination is a validated URL with its canonical host and port.
type destination struct {
	url    *url.URL
	target target
	port   int
}

// validateURL is the pre-flight check: everything that can be decided
// without DNS. It never resolves names or opens connections.
func (p policy) validateURL(raw string) (destination, *Error) {
	if len(raw) > MaxURLBytes {
		return destination{}, fail(InvalidURL)
	}
	for i := 0; i < len(raw); i++ {
		if c := raw[i]; c <= ' ' || c == 0x7f { // control characters and whitespace
			return destination{}, fail(InvalidURL)
		}
	}
	u, err := url.Parse(raw)
	if err != nil {
		return destination{}, fail(InvalidURL)
	}
	switch u.Scheme {
	case "https", "http": // http is checked below, once the host is known
	default: // file:, gopher:, data:, ws:, scheme-relative //host, ...
		return destination{}, fail(BlockedScheme)
	}
	if u.Opaque != "" || u.User != nil || u.Host == "" {
		return destination{}, fail(InvalidURL)
	}
	t, ferr := canonicalHost(u.Hostname())
	if ferr != nil {
		return destination{}, ferr
	}
	port := 443
	if u.Scheme == "http" {
		port = 80
	}
	if ps := u.Port(); ps != "" {
		n, err := strconv.Atoi(ps)
		if err != nil || n > 65535 {
			return destination{}, fail(InvalidURL)
		}
		port = n
	}
	if !t.isIP() && p.testAllowed(t.name, port) {
		// An allowlisted test destination: its scheme, port, and internal
		// name are accepted; DNS answers are pinned at dial time.
		u.Fragment, u.RawFragment = "", ""
		return destination{url: u, target: t, port: port}, nil
	}
	if u.Scheme == "http" && !p.allowHTTP {
		return destination{}, fail(BlockedScheme)
	}
	if !p.ports[port] {
		return destination{}, fail(BlockedPort)
	}
	if t.isIP() {
		if !p.addrAllowed(t.ip, port) {
			return destination{}, fail(BlockedAddress)
		}
	} else if p.hosts.blocked(t.name) {
		return destination{}, fail(BlockedHost)
	}
	u.Fragment, u.RawFragment = "", ""
	return destination{url: u, target: t, port: port}, nil
}

func newPolicy(allowHTTP bool, ports []int, blocked, testAllow []string) policy {
	p := policy{allowHTTP: allowHTTP, ports: map[int]bool{}, hosts: hostBlocklist{configured: blocked}, isPublic: ippolicy.IsPublic}
	for _, n := range ports {
		p.ports[n] = true
	}
	if len(testAllow) > 0 {
		p.testAllow = map[string]bool{}
		for _, e := range testAllow {
			p.testAllow[e] = true
		}
	}
	return p
}
