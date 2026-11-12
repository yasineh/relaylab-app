package ippolicy

import (
	"net/netip"
	"testing"
)

// lastAddr returns the highest address in p.
func lastAddr(p netip.Prefix) netip.Addr {
	b := p.Masked().Addr().AsSlice()
	for i := p.Bits(); i < len(b)*8; i++ {
		b[i/8] |= 1 << (7 - i%8)
	}
	a, _ := netip.AddrFromSlice(b)
	return a
}

// TestEveryBlockedRangeBothEdges: the first and last address of every range
// in the spec are blocked.
func TestEveryBlockedRangeBothEdges(t *testing.T) {
	for _, p := range append(append([]netip.Prefix{}, blocked4...), blocked6...) {
		for _, a := range []netip.Addr{p.Masked().Addr(), lastAddr(p)} {
			if IsPublic(a) {
				t.Errorf("%s (edge of %s) is public", a, p)
			}
		}
	}
}

// TestNeighboursOfIPv4RangesArePublic: the addresses just outside each IPv4
// range are public unless another range covers them, so the prefixes are not
// wider than intended.
func TestNeighboursOfIPv4RangesArePublic(t *testing.T) {
	covered := func(a netip.Addr) bool {
		for _, p := range blocked4 {
			if p.Contains(a) {
				return true
			}
		}
		return false
	}
	for _, p := range blocked4 {
		for _, a := range []netip.Addr{p.Masked().Addr().Prev(), lastAddr(p).Next()} {
			if !a.IsValid() || covered(a) {
				continue
			}
			if !IsPublic(a) {
				t.Errorf("%s (just outside %s) is blocked", a, p)
			}
		}
	}
}

func TestSpecExamples(t *testing.T) {
	blocked := []string{
		// IPv4 from the spec scenarios.
		"127.0.0.1", "127.1.2.3", "10.0.0.5", "172.16.4.4", "192.168.1.1", "100.64.0.1",
		"169.254.169.254", "224.0.0.1", "255.255.255.255", "240.0.0.1", "0.0.0.0",
		// IPv6.
		"::", "::1", "fd12:3456::1", "fd00:ec2::254", "fe80::1", "fe80::1%eth0", "fec0::1", "ff02::1",
		"2001:db8::1", "2001:10::1", "2001:20::1", "100::1",
		// IPv4 embedded in IPv6, whatever the embedded address.
		"::ffff:127.0.0.1", "::ffff:7f00:1", "::ffff:8.8.8.8", "::127.0.0.1", "::8.8.8.8",
		"64:ff9b::a9fe:a9fe", "64:ff9b::808:808", "64:ff9b:1::1",
		"2002:a9fe:a9fe::", "2002:808:808::", "2001:0:4136:e378:8000:63bf:3fff:fdd2",
		// Outside global unicast.
		"4000::1", "e000::1",
	}
	for _, s := range blocked {
		if IsPublic(netip.MustParseAddr(s)) {
			t.Errorf("%s is public", s)
		}
	}
	public := []string{"8.8.8.8", "1.1.1.1", "93.184.216.34", "2606:4700::1111", "2001:4860:4860::8888"}
	for _, s := range public {
		if !IsPublic(netip.MustParseAddr(s)) {
			t.Errorf("%s is blocked", s)
		}
	}
	if IsPublic(netip.Addr{}) {
		t.Error("the zero address is public")
	}
}

func TestNormalizeUnmapsSocketAddresses(t *testing.T) {
	if got := Normalize(netip.MustParseAddr("::ffff:8.8.8.8")); got != netip.MustParseAddr("8.8.8.8") || !IsPublic(got) {
		t.Fatalf("Normalize = %s", got)
	}
	if IsPublic(Normalize(netip.MustParseAddr("::ffff:127.0.0.1"))) {
		t.Fatal("unmapped loopback is public")
	}
}
