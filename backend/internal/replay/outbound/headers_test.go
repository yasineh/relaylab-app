package outbound

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"
)

func TestOutboundHeadersStripped(t *testing.T) {
	e := newEnv(t)
	var got http.Header
	var host string
	_, port := e.server("example.com", func(w http.ResponseWriter, r *http.Request) {
		got, host = r.Header.Clone(), r.Host
	})
	e.build()
	in := http.Header{
		"Authorization":       {"Bearer abc"},
		"Proxy-Authorization": {"Basic xyz"},
		"Cookie":              {"sid=1"},
		"Set-Cookie":          {"a=b"},
		"Host":                {"internal.example"},
		"Connection":          {"keep-alive, X-Secret"},
		"Keep-Alive":          {"timeout=5"},
		"Transfer-Encoding":   {"chunked"},
		"Te":                  {"trailers"},
		"Upgrade":             {"websocket"},
		"Proxy-Connection":    {"keep-alive"},
		"Forwarded":           {"for=10.0.0.1"},
		"X-Forwarded-For":     {"10.0.0.1"},
		"X-Forwarded-Host":    {"internal"},
		"X-Real-Ip":           {"10.0.0.1"},
		"Content-Length":      {"999"},
		"User-Agent":          {"evil/1.0"},
		"Accept-Encoding":     {"br"},
		// Kept:
		"Content-Type":      {"application/json"},
		"X-Github-Event":    {"push"},
		"X-Custom-Multi":    {"a", "b"},
		"Stripe-Signature2": {"t=1"},
	}
	r := e.do(http.MethodPost, "https://example.com:"+port+"/", in, []byte("{}"))
	expectResult(t, r, Succeeded)
	for _, name := range []string{
		"Authorization", "Proxy-Authorization", "Cookie", "Set-Cookie", "Keep-Alive", "Te", "Upgrade",
		"Proxy-Connection", "Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Real-Ip", "X-Secret",
	} {
		if v := got.Values(name); len(v) > 0 {
			t.Errorf("%s forwarded: %q", name, v)
		}
	}
	for _, v := range got.Values("Connection") {
		if strings.Contains(v, "X-Secret") {
			t.Errorf("caller Connection forwarded: %q", v)
		}
	}
	if host != "example.com:"+port {
		t.Errorf("Host %q, want the computed one", host)
	}
	if got.Get("User-Agent") != "RelayLab-Replay/test" {
		t.Errorf("User-Agent %q", got.Get("User-Agent"))
	}
	if got.Get("Accept-Encoding") == "br" {
		t.Error("caller Accept-Encoding forwarded")
	}
	if got.Get("Content-Type") != "application/json" || got.Get("X-Github-Event") != "push" ||
		!slices.Equal(got.Values("X-Custom-Multi"), []string{"a", "b"}) || got.Get("Stripe-Signature2") != "t=1" {
		t.Errorf("ordinary headers not forwarded: %v", got)
	}
}

func TestHeaderInjectionRejected(t *testing.T) {
	e := newEnv(t)
	hits := 0
	_, port := e.server("example.com", func(http.ResponseWriter, *http.Request) { hits++ })
	e.build()
	for _, h := range []http.Header{
		{"X-A": {"ok\r\nX-Injected: 1"}},
		{"X-A": {"ok\nX-Injected: 1"}},
		{"X-A": {"ok\rx"}},
		{"X-A": {"nul\x00"}},
		{"X-A\r\nX-Injected": {"1"}},
		{"Bad Name": {"1"}},
		{"": {"1"}},
		{"Authorization": {"Bearer a\r\nX-Injected: 1"}}, // rejected even though it would be stripped
	} {
		expectResult(t, e.do(http.MethodPost, "https://example.com:"+port+"/", h, nil), InvalidRequest)
	}
	if hits != 0 {
		t.Fatalf("%d requests were sent", hits)
	}
}

func TestExplicitHeaders(t *testing.T) {
	e := newEnv(t)
	var got http.Header
	hits := 0
	_, port := e.server("example.com", func(_ http.ResponseWriter, r *http.Request) { got, hits = r.Header.Clone(), hits+1 })
	e.build()
	url := "https://example.com:" + port + "/"

	// A user-added Authorization is sent; a derived one never is.
	r := e.do2(url, http.Header{"Authorization": {"Bearer from-event"}, "X-Kept": {"a"}},
		http.Header{"Authorization": {"Bearer from-user"}, "X-Kept": {"b"}, "Stripe-Signature": {"t=1"}})
	expectResult(t, r, Succeeded)
	if got.Get("Authorization") != "Bearer from-user" || !slices.Equal(got.Values("X-Kept"), []string{"b"}) ||
		got.Get("Stripe-Signature") != "t=1" {
		t.Fatalf("server saw %v", got)
	}
	if r := e.do2(url, http.Header{"Authorization": {"Bearer from-event"}}, nil); r.Category != Succeeded {
		t.Fatal(r.Category)
	} else if got.Get("Authorization") != "" {
		t.Fatalf("derived Authorization forwarded: %q", got.Get("Authorization"))
	}

	sent := hits
	for _, name := range []string{
		"Cookie", "Set-Cookie", "Proxy-Authorization", "Host", "Connection", "Transfer-Encoding",
		"Upgrade", "Forwarded", "X-Forwarded-For", "X-Real-Ip", "Content-Length", "Proxy-Connection",
	} {
		expectResult(t, e.do2(url, nil, http.Header{name: {"x"}}), InvalidRequest)
	}
	expectResult(t, e.do2(url, nil, http.Header{"Authorization": {"Bearer a\r\nX-Injected: 1"}}), InvalidRequest)
	if hits != sent {
		t.Fatal("a request with a forbidden explicit header was sent")
	}
	for name, want := range map[string]bool{"authorization": false, "Cookie": true, "X-Forwarded-Host": true, "X-Custom": false} {
		if ExplicitForbidden(name) != want {
			t.Errorf("ExplicitForbidden(%s) = %v", name, !want)
		}
	}
}

func (e *testEnv) do2(url string, derived, explicit http.Header) Result {
	return e.client.Do(context.Background(), Request{Method: http.MethodPost, URL: url, Header: derived, Explicit: explicit})
}
