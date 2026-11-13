package outbound

import (
	"crypto/x509"
	"net/netip"
	"testing"
)

// WithTestReachability returns o with test hooks set: exact loopback
// addresses that may be reached, a resolver, and trusted test CAs. It lets
// other packages' tests deliver to httptest servers through the real guard.
// It panics outside test binaries, so production code cannot weaken the
// policy with it.
func WithTestReachability(o Options, allow func(netip.AddrPort) bool, r Resolver, roots *x509.CertPool) Options {
	if !testing.Testing() {
		panic("outbound.WithTestReachability is for tests only")
	}
	o.allow, o.resolver, o.rootCAs = allow, r, roots
	return o
}
