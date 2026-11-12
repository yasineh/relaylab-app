// Package ippolicy decides whether an IP address is a public internet
// destination that user-directed outbound requests (replay) may reach.
//
// The policy is an allowlist with exclusions: IPv4 must be outside every
// special-purpose range below; IPv6 must be global unicast (2000::/3) and
// outside the special-purpose ranges below. Review this table when IANA
// updates its special-purpose registries (docs/security/outbound-replay.md).
package ippolicy

import "net/netip"

// blocked4 are the non-public IPv4 ranges (outbound-request-guard spec).
var blocked4 = prefixes(
	"0.0.0.0/8",          // "this network"; 0.0.0.0 reaches localhost on many stacks
	"10.0.0.0/8",         // private
	"100.64.0.0/10",      // carrier-grade NAT
	"127.0.0.0/8",        // loopback
	"169.254.0.0/16",     // link-local, including 169.254.169.254 cloud metadata
	"172.16.0.0/12",      // private
	"192.0.0.0/24",       // IETF protocol assignments
	"192.0.2.0/24",       // documentation (TEST-NET-1)
	"192.88.99.0/24",     // 6to4 relay anycast
	"192.168.0.0/16",     // private
	"198.18.0.0/15",      // benchmarking
	"198.51.100.0/24",    // documentation (TEST-NET-2)
	"203.0.113.0/24",     // documentation (TEST-NET-3)
	"224.0.0.0/4",        // multicast
	"240.0.0.0/4",        // reserved
	"255.255.255.255/32", // limited broadcast
)

// global6 is the only IPv6 space that may be public (global unicast).
var global6 = netip.MustParsePrefix("2000::/3")

// blocked6 are special-purpose ranges inside or outside 2000::/3. Ranges
// outside 2000::/3 (::, ::1, fc00::/7, fe80::/10, ...) are already excluded by
// the global unicast rule; they are listed so the table matches the spec.
var blocked6 = prefixes(
	"::/128",         // unspecified
	"::1/128",        // loopback
	"::/96",          // IPv4-compatible (deprecated), any embedded address
	"::ffff:0:0/96",  // IPv4-mapped, any embedded address
	"64:ff9b::/96",   // NAT64, any embedded address
	"64:ff9b:1::/48", // local-use NAT64
	"100::/64",       // discard-only
	"2001::/32",      // Teredo, any embedded address
	"2001:10::/28",   // ORCHID (deprecated)
	"2001:20::/28",   // ORCHIDv2
	"2001:db8::/32",  // documentation
	"2002::/16",      // 6to4, any embedded address
	"3fff::/20",      // documentation (RFC 9637)
	"5f00::/16",      // SRv6 SIDs
	"fc00::/7",       // unique local, including AWS metadata fd00:ec2::254
	"fe80::/10",      // link-local
	"fec0::/10",      // site-local (deprecated)
	"ff00::/8",       // multicast
)

func prefixes(ss ...string) []netip.Prefix {
	out := make([]netip.Prefix, len(ss))
	for i, s := range ss {
		out[i] = netip.MustParsePrefix(s)
	}
	return out
}

// IsPublic reports whether a may be reached by a user-directed outbound
// request. An address with a zone (fe80::1%eth0) is never public.
//
// The address is checked as given: an IPv4-mapped IPv6 literal such as
// ::ffff:8.8.8.8 is rejected like every other IPv4-embedding form. Callers
// checking an address that a resolver or socket produced for an IPv4
// destination should pass Normalize(a) instead.
func IsPublic(a netip.Addr) bool {
	if !a.IsValid() || a.Zone() != "" {
		return false
	}
	if a.Is4() {
		for _, p := range blocked4 {
			if p.Contains(a) {
				return false
			}
		}
		return true
	}
	if !global6.Contains(a) {
		return false
	}
	for _, p := range blocked6 {
		if p.Contains(a) {
			return false
		}
	}
	return true
}

// Normalize unmaps an IPv4-mapped IPv6 address that stands for an IPv4
// socket address (resolver answers and dial addresses can use that form).
func Normalize(a netip.Addr) netip.Addr { return a.Unmap() }
