package outbound

import (
	"net/netip"
	"testing"
)

func TestCanonicalHostRejectsAmbiguousNumbers(t *testing.T) {
	for _, h := range []string{
		"2130706433", "017700000001", "0", "2852039166", // single number: decimal, octal
		"0x7f000001", "0X7F000001", "0x7f.0.0.1", "0x", // hex
		"0177.0.0.1", "127.0.0.01", "010.0.0.1", // octal parts
		"127.1", "127.0.1", "10.1", // short forms
		"127.0.0x1", "1.2.3.0x4", "0x7f.1", // mixed
		"256.0.0.1", "1.2.3.4.5", "1.2.3.", // out of range, too many parts
		"example.123", "foo.0x10", // a numeric last label makes the host numeric
		"", ".", "..", "a..b", "-a.com", "a-.com", "ex ample.com", "exämple.com",
	} {
		if got, err := canonicalHost(h); err == nil || err.Category != InvalidURL {
			t.Errorf("%q: %+v, %v; want invalid_url", h, got, err)
		}
	}
}

func TestCanonicalHostAcceptsCanonicalForms(t *testing.T) {
	for in, want := range map[string]netip.Addr{
		"127.0.0.1":        netip.MustParseAddr("127.0.0.1"),
		"127.0.0.1.":       netip.MustParseAddr("127.0.0.1"), // trailing dot
		"8.8.8.8":          netip.MustParseAddr("8.8.8.8"),
		"0.0.0.0":          netip.MustParseAddr("0.0.0.0"),
		"::1":              netip.MustParseAddr("::1"),
		"0:0:0:0:0:0:0:1":  netip.MustParseAddr("::1"),
		"FE80::1%eth0":     netip.MustParseAddr("fe80::1%eth0"), // zone kept: never public
		"::ffff:127.0.0.1": netip.MustParseAddr("::ffff:127.0.0.1"),
		"2606:4700::1111":  netip.MustParseAddr("2606:4700::1111"),
	} {
		got, err := canonicalHost(in)
		if err != nil || got.ip != want {
			t.Errorf("%q: %+v, %v; want %s", in, got, err, want)
		}
	}
	for in, want := range map[string]string{
		"Hooks.Example.COM":         "hooks.example.com",
		"metadata.google.internal.": "metadata.google.internal",
		"a_b.example.com":           "a_b.example.com",
		"xn--exmple-cua.com":        "xn--exmple-cua.com",
		"cafe.babe":                 "cafe.babe", // hex letters without 0x are not numbers
		"123.example.com":           "123.example.com",
	} {
		got, err := canonicalHost(in)
		if err != nil || got.name != want || got.isIP() {
			t.Errorf("%q: %+v, %v; want name %s", in, got, err, want)
		}
	}
	if _, err := canonicalHost("1:2"); err == nil {
		t.Error("malformed IPv6 accepted")
	}
}
