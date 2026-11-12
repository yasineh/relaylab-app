package outbound

import (
	"io"
	"net/http"
	"strconv"
	"testing"
)

func TestPublicDestinationSucceeds(t *testing.T) {
	e := newEnv(t)
	var got *http.Request
	var gotBody []byte
	_, port := e.server("example.com", func(w http.ResponseWriter, r *http.Request) {
		got = r.Clone(r.Context())
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("X-Reply", "1")
		_, _ = w.Write([]byte("hello"))
	})
	e.build()
	r := e.do(http.MethodPost, "https://example.com:"+port+"/receive?a=1", http.Header{"Content-Type": {"application/json"}}, []byte(`{"x":1}`))
	expectResult(t, r, Succeeded)
	if r.Status != 200 || string(r.Body) != "hello" || r.Truncated || r.Header.Get("X-Reply") != "1" || r.Message != "" {
		t.Fatalf("result %+v", r)
	}
	if got.URL.RequestURI() != "/receive?a=1" || string(gotBody) != `{"x":1}` || got.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("server saw %s %v %q", got.URL, got.Header, gotBody)
	}
	if ua := got.Header.Get("User-Agent"); ua != "RelayLab-Replay/test" {
		t.Fatalf("User-Agent %q", ua)
	}
	if got.ContentLength != int64(len(`{"x":1}`)) {
		t.Fatalf("Content-Length %d", got.ContentLength)
	}
}

func TestProxyEnvironmentIgnored(t *testing.T) {
	proxy := newTrap(t)
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:"+strconv.Itoa(proxy.port()))
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:"+strconv.Itoa(proxy.port()))
	t.Setenv("NO_PROXY", "")
	e := newEnv(t)
	hits := 0
	_, port := e.server("example.com", func(w http.ResponseWriter, _ *http.Request) { hits++ })
	e.build()
	expectResult(t, e.do(http.MethodGet, "https://example.com:"+port+"/", nil, nil), Succeeded)
	if hits != 1 || proxy.count() != 0 {
		t.Fatalf("server hits %d, proxy connections %d", hits, proxy.count())
	}
}

func TestMethodsLimitedToReplayable(t *testing.T) {
	e := newEnv(t)
	e.build()
	for _, m := range []string{"CONNECT", "TRACE", "", "get", "PO ST"} {
		expectResult(t, e.do(m, "https://example.com/", nil, nil), InvalidRequest)
	}
}
