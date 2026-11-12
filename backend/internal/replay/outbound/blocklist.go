package outbound

import "strings"

// internalSuffixes are name suffixes that never belong to public internet
// destinations (outbound-request-guard spec, "Internal host names").
var internalSuffixes = []string{
	".localhost", ".local", ".internal", ".localdomain", ".home.arpa", ".cluster.local", ".svc",
}

// hostBlocklist decides blocked_host for DNS names, before resolution.
type hostBlocklist struct {
	configured []string // lower-case, no trailing dot; their subdomains match too
}

// blocked reports whether name (canonical: lower-case, no trailing dot) is an
// internal or configured host.
func (b hostBlocklist) blocked(name string) bool {
	if name == "localhost" || !strings.Contains(name, ".") { // single-label names
		return true
	}
	for _, s := range internalSuffixes {
		if strings.HasSuffix(name, s) {
			return true
		}
	}
	for _, h := range b.configured {
		if name == h || strings.HasSuffix(name, "."+h) {
			return true
		}
	}
	return false
}
