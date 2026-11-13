package outbound

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"testing"
)

// REPLAY_TEST_ALLOWLIST (end-to-end stack only): exactly the listed
// host:port is reachable, over plain HTTP, at the addresses that name
// resolves to. Nothing else is loosened.
func TestTestAllowlistAdmitsOnlyTheListedDestination(t *testing.T) {
	e := newEnv(t)
	hits := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(s.Close)
	ap := netip.MustParseAddrPort(s.Listener.Addr().String())
	port := strconv.Itoa(int(ap.Port()))
	e.resolver.set("echo", []string{ap.Addr().String()})
	e.resolver.set("other.example", []string{ap.Addr().String()})
	e.opts.TestAllowlist = []string{"echo:" + port}
	e.opts.AllowedPorts = append(e.opts.AllowedPorts, int(ap.Port()))
	e.build()

	r := e.do("POST", "http://echo:"+port+"/hook", nil, []byte("x"))
	expectResult(t, r, Succeeded)
	if r.Status != http.StatusAccepted || hits != 1 {
		t.Fatalf("status %d hits %d", r.Status, hits)
	}

	for raw, want := range map[string]Category{
		"http://echo:" + strconv.Itoa(int(ap.Port())+1) + "/": BlockedScheme, // another port: normal rules
		"https://echo/":                       BlockedHost,    // single-label name, normal rules
		"http://other.example:" + port + "/":  BlockedScheme,  // same address, another name
		"https://other.example:" + port + "/": BlockedAddress, // resolves to loopback
		"https://127.0.0.1:" + port + "/":     BlockedAddress, // the address itself, as a literal
		"http://example.com/":                 BlockedScheme,
		"https://169.254.169.254/":            BlockedAddress,
	} {
		expectResult(t, e.do("GET", raw, nil, nil), want)
	}
	if hits != 1 {
		t.Fatalf("blocked destinations reached the server: %d hits", hits)
	}
	if !e.client.Active() {
		t.Fatal("self-check failed with an allowlist")
	}
}

func TestValidatorAcceptsAllowlistedTestDestination(t *testing.T) {
	v := NewValidator(Options{AllowedPorts: []int{443}, TestAllowlist: []string{"echo:8080"}})
	if err := v.Validate("http://echo:8080/hooks?x=1"); err != nil {
		t.Fatalf("allowlisted: %v", err.Category)
	}
	for _, raw := range []string{"http://echo:8081/", "https://echo/", "http://10.0.0.5:8080/", "http://user@echo:8080/"} {
		if err := v.Validate(raw); err == nil {
			t.Errorf("%s accepted", raw)
		}
	}
}
