package outbound

import (
	"net/http"
	"strconv"
	"testing"
	"time"
)

func TestEveryResolvedAddressIsValidated(t *testing.T) {
	e := newEnv(t)
	e.resolver.set("mixed.example.test", []string{"93.184.216.34", "10.0.0.1"})
	e.resolver.set("mixed6.example.test", []string{"2606:4700::1111", "fd00:ec2::254"})
	e.resolver.set("localtest.example.test", []string{"127.0.0.1"})
	e.resolver.set("localtest6.example.test", []string{"::1"})
	e.resolver.set("mapped.example.test", []string{"::ffff:169.254.169.254"})
	e.resolver.set("empty.example.test", []string{})
	e.build()
	for host, want := range map[string]Category{
		"mixed.example.test":      BlockedAddress,
		"mixed6.example.test":     BlockedAddress,
		"localtest.example.test":  BlockedAddress,
		"localtest6.example.test": BlockedAddress,
		"mapped.example.test":     BlockedAddress,
		"nxdomain.example.test":   DNSFailure,
		"empty.example.test":      DNSFailure,
	} {
		r := e.do(http.MethodGet, "https://"+host+"/", nil, nil)
		if r.Category != want {
			t.Errorf("%s: %s, want %s", host, r.Category, want)
		}
	}
}

func TestDNSLookupTimeout(t *testing.T) {
	e := newEnv(t)
	e.opts.ConnectTimeout = 100 * time.Millisecond
	e.resolver.set("slow.example.test", []string{"93.184.216.34"})
	e.resolver.delay = time.Second
	e.build()
	start := time.Now()
	expectResult(t, e.do(http.MethodGet, "https://slow.example.test/", nil, nil), DNSFailure)
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Fatalf("lookup timeout took %s", d)
	}
}

// TestDNSRebindingDefeated: the first answer is public, every later one is
// loopback. The connection-time lookup is checked like the first, so nothing
// reaches the loopback listener.
func TestDNSRebindingDefeated(t *testing.T) {
	e := newEnv(t)
	tr := newTrap(t)
	e.opts.AllowedPorts = append(e.opts.AllowedPorts, tr.port())
	e.resolver.set("rebind.example.test", []string{"93.184.216.34"}, []string{"127.0.0.1"})
	e.build()
	r := e.do(http.MethodGet, "https://rebind.example.test:"+strconv.Itoa(tr.port())+"/", nil, nil)
	expectResult(t, r, BlockedAddress)
	if n := e.resolver.calls["rebind.example.test"]; n < 2 {
		t.Fatalf("resolved %d times; the dial must resolve again", n)
	}
	time.Sleep(50 * time.Millisecond)
	if tr.count() != 0 {
		t.Fatalf("%d connections reached the loopback listener", tr.count())
	}
}

// TestControlBlocksSocketAddresses: the dial-time check on the exact socket
// address is the last line; whatever produced an address, it is refused.
func TestControlBlocksSocketAddresses(t *testing.T) {
	c := newEnv(t).build()
	for _, a := range []string{
		"127.0.0.1:443", "[::1]:443", "169.254.169.254:443", "10.1.2.3:8443", "[::ffff:127.0.0.1]:443",
		"[fd00:ec2::254]:443", "0.0.0.0:443", "not-an-address",
	} {
		if err := c.control("tcp", a, nil); err == nil {
			t.Errorf("%s allowed", a)
		}
	}
	for _, a := range []string{"93.184.216.34:443", "[2606:4700::1111]:443", "[::ffff:8.8.8.8]:443"} {
		if err := c.control("tcp", a, nil); err != nil {
			t.Errorf("%s refused: %v", a, err)
		}
	}
}
