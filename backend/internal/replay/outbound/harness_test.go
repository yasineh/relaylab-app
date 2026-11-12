package outbound

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"sync"
	"testing"
	"time"
)

// fakeResolver answers from a script: each host has a list of answers, used in
// order; the last one repeats. Hosts not in the script are NXDOMAIN.
type fakeResolver struct {
	mu      sync.Mutex
	answers map[string][][]netip.Addr
	delay   time.Duration
	calls   map[string]int
}

func newResolver() *fakeResolver {
	return &fakeResolver{answers: map[string][][]netip.Addr{}, calls: map[string]int{}}
}

func (r *fakeResolver) set(host string, answers ...[]string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.answers[host] = nil
	for _, a := range answers {
		var addrs []netip.Addr
		for _, s := range a {
			addrs = append(addrs, netip.MustParseAddr(s))
		}
		r.answers[host] = append(r.answers[host], addrs)
	}
}

func (r *fakeResolver) LookupNetIP(ctx context.Context, _, host string) ([]netip.Addr, error) {
	r.mu.Lock()
	n := r.calls[host]
	r.calls[host]++
	seq, ok := r.answers[host]
	delay := r.delay
	r.mu.Unlock()
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if !ok || len(seq) == 0 {
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	return seq[min(n, len(seq)-1)], nil
}

// testEnv is a client plus TLS test servers it may reach. Only the exact
// address:port of servers registered with reach() is allowed; every other
// loopback address stays blocked, as in production.
type testEnv struct {
	t         *testing.T
	resolver  *fakeResolver
	opts      Options
	reachable map[netip.AddrPort]bool
	client    *Client
	mu        sync.Mutex
}

func newEnv(t *testing.T) *testEnv {
	t.Helper()
	e := &testEnv{t: t, resolver: newResolver(), reachable: map[netip.AddrPort]bool{}}
	e.opts = Options{
		AllowedPorts:           []int{443, 8443},
		BlockedHosts:           []string{"metadata.google.internal", "metadata"},
		ConnectTimeout:         time.Second,
		TLSHandshakeTimeout:    time.Second,
		ResponseHeaderTimeout:  2 * time.Second,
		TotalTimeout:           3 * time.Second,
		MaxResponseBytes:       64 << 10,
		MaxResponseHeaderBytes: 32 << 10,
		MaxConcurrency:         8,
		UserAgent:              "RelayLab-Replay/test",
		resolver:               e.resolver,
		allow: func(ap netip.AddrPort) bool {
			e.mu.Lock()
			defer e.mu.Unlock()
			return e.reachable[ap]
		},
	}
	return e
}

// server starts a TLS server (certificate valid for example.com and
// 127.0.0.1), makes it reachable, and maps name to it in DNS.
func (e *testEnv) server(name string, h http.HandlerFunc) (*httptest.Server, string) {
	e.t.Helper()
	s := httptest.NewTLSServer(h)
	e.t.Cleanup(s.Close)
	ap := netip.MustParseAddrPort(s.Listener.Addr().String())
	e.mu.Lock()
	e.reachable[ap] = true
	e.mu.Unlock()
	e.opts.AllowedPorts = append(e.opts.AllowedPorts, int(ap.Port()))
	pool := s.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs
	e.opts.rootCAs = pool
	if name != "" {
		e.resolver.set(name, []string{ap.Addr().String()})
	}
	return s, strconv.Itoa(int(ap.Port()))
}

// build creates the client after servers and options are set.
func (e *testEnv) build() *Client {
	e.client = New(e.opts)
	if !e.client.Active() {
		e.t.Fatal("client inactive")
	}
	return e.client
}

func (e *testEnv) do(method, rawURL string, h http.Header, body []byte) Result {
	return e.client.Do(context.Background(), Request{Method: method, URL: rawURL, Header: h, Body: body})
}

func expectResult(t *testing.T, r Result, want Category) {
	t.Helper()
	if r.Category != want {
		t.Fatalf("category %s, want %s (result %+v)", r.Category, want, r)
	}
}

// listener counts connections on a loopback port that the client must never
// reach. It is not registered as reachable.
type trap struct {
	ln    net.Listener
	mu    sync.Mutex
	conns int
}

func newTrap(t *testing.T) *trap {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	tr := &trap{ln: ln}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			tr.mu.Lock()
			tr.conns++
			tr.mu.Unlock()
			_ = c.Close()
		}
	}()
	return tr
}

func (tr *trap) port() int { return int(netip.MustParseAddrPort(tr.ln.Addr().String()).Port()) }

func (tr *trap) count() int {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return tr.conns
}
