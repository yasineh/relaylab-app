package outbound

import (
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
