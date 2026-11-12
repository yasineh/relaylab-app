package outbound

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
)

func TestSelfCheckPassesWithValidPolicy(t *testing.T) {
	if !New(newEnv(t).opts).Active() {
		t.Fatal("inactive with a valid policy")
	}
}

// TestSelfCheckFailsClosed: when the policy is broken so that a canary would
// pass, the client is inactive and sends nothing, not even to destinations
// the broken policy would allow.
func TestSelfCheckFailsClosed(t *testing.T) {
	for name, sabotage := range map[string]func(*Options){
		"every address public": func(o *Options) { o.isPublic = func(netip.Addr) bool { return true } },
		"every socket allowed": func(o *Options) { o.allow = func(netip.AddrPort) bool { return true } },
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			var hits atomic.Int32
			_, port := e.server("example.com", func(http.ResponseWriter, *http.Request) { hits.Add(1) })
			sabotage(&e.opts)
			var logs bytes.Buffer
			e.opts.Logger = slog.New(slog.NewJSONHandler(&logs, nil))
			c := New(e.opts)
			if c.Active() {
				t.Fatal("active with a broken policy")
			}
			e.client = c
			r := e.do(http.MethodGet, "https://example.com:"+port+"/", nil, nil)
			expectResult(t, r, GuardInactive)
			if hits.Load() != 0 {
				t.Fatal("a request was sent while inactive")
			}
			if !strings.Contains(logs.String(), "self-check failed") {
				t.Fatalf("no error logged: %s", logs.String())
			}
		})
	}
}
