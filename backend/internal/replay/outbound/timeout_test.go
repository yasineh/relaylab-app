package outbound

import (
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"testing"
	"time"
)

func TestSlowResponseHeadersTimeOut(t *testing.T) {
	e := newEnv(t)
	e.opts.ResponseHeaderTimeout = 200 * time.Millisecond
	_, port := e.server("example.com", func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done(): // the client gave up
		case <-time.After(5 * time.Second):
		}
	})
	e.build()
	start := time.Now()
	expectResult(t, e.do(http.MethodGet, "https://example.com:"+port+"/", nil, nil), Timeout)
	if d := time.Since(start); d > time.Second {
		t.Fatalf("aborted after %s", d)
	}
}

func TestSlowBodyDripTimesOut(t *testing.T) {
	e := newEnv(t)
	e.opts.TotalTimeout = 800 * time.Millisecond
	_, port := e.server("example.com", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		for {
			if _, err := w.Write([]byte("x")); err != nil {
				return
			}
			w.(http.Flusher).Flush()
			select {
			case <-r.Context().Done():
				return
			case <-time.After(100 * time.Millisecond):
			}
		}
	})
	e.build()
	start := time.Now()
	expectResult(t, e.do(http.MethodGet, "https://example.com:"+port+"/", nil, nil), Timeout)
	if d := time.Since(start); d > 1500*time.Millisecond {
		t.Fatalf("aborted after %s, want about the total timeout", d)
	}
}

// TestTLSHandshakeTimeout: a server that accepts TCP but never answers the
// TLS handshake.
func TestTLSHandshakeTimeout(t *testing.T) {
	e := newEnv(t)
	e.opts.TLSHandshakeTimeout = 200 * time.Millisecond
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { _ = c.Close() })
		}
	}()
	ap := netip.MustParseAddrPort(ln.Addr().String())
	e.reachable[ap] = true
	e.opts.AllowedPorts = append(e.opts.AllowedPorts, int(ap.Port()))
	e.resolver.set("silent.example.com", []string{"127.0.0.1"})
	e.build()
	start := time.Now()
	expectResult(t, e.do(http.MethodGet, "https://silent.example.com:"+strconv.Itoa(int(ap.Port()))+"/", nil, nil), Timeout)
	if d := time.Since(start); d > time.Second {
		t.Fatalf("aborted after %s", d)
	}
}
