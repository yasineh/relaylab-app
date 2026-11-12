package outbound

import (
	"bytes"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestHTTPErrorIsSuccessOutcome(t *testing.T) {
	e := newEnv(t)
	_, port := e.server("example.com", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	e.build()
	r := e.do(http.MethodGet, "https://example.com:"+port+"/", nil, nil)
	expectResult(t, r, Succeeded)
	if r.Status != 500 || !strings.Contains(string(r.Body), "boom") {
		t.Fatalf("%+v", r)
	}
}

func TestTLSFailures(t *testing.T) {
	// Self-signed: the client does not trust the test CA (no rootCAs).
	e := newEnv(t)
	_, port := e.server("example.com", func(http.ResponseWriter, *http.Request) {})
	e.opts.rootCAs = nil
	e.build()
	expectResult(t, e.do(http.MethodGet, "https://example.com:"+port+"/", nil, nil), TLSFailure)

	// Trusted CA, wrong name: the certificate is for example.com.
	e2 := newEnv(t)
	s, port2 := e2.server("", func(http.ResponseWriter, *http.Request) {})
	e2.resolver.set("wrong-name.example.org", []string{netip.MustParseAddrPort(s.Listener.Addr().String()).Addr().String()})
	e2.build()
	expectResult(t, e2.do(http.MethodGet, "https://wrong-name.example.org:"+port2+"/", nil, nil), TLSFailure)
}

func TestConnectFailure(t *testing.T) {
	e := newEnv(t)
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	ap := netip.MustParseAddrPort(ln.Addr().String())
	_ = ln.Close() // nothing listens there now
	e.reachable[ap] = true
	e.opts.AllowedPorts = append(e.opts.AllowedPorts, int(ap.Port()))
	e.resolver.set("closed.example.com", []string{"127.0.0.1"})
	e.build()
	expectResult(t, e.do(http.MethodGet, "https://closed.example.com:"+strconv.Itoa(int(ap.Port()))+"/", nil, nil), ConnectFailure)
}

func TestOversizedResponseHeaders(t *testing.T) {
	e := newEnv(t)
	_, port := e.server("example.com", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Huge", strings.Repeat("a", 40<<10))
	})
	e.build()
	expectResult(t, e.do(http.MethodGet, "https://example.com:"+port+"/", nil, nil), ResponseTooLarge)
}

var ipLike = regexp.MustCompile(`\d+\.\d+\.\d+\.\d+|[0-9a-f]*:[0-9a-f:]+`)

func TestEveryFailureHasASafeMessage(t *testing.T) {
	for _, c := range allCategories {
		m := messages[c]
		if c == Succeeded {
			if m != "" {
				t.Errorf("succeeded has a message")
			}
			continue
		}
		if m == "" || ipLike.MatchString(m) {
			t.Errorf("%s: unsafe or missing message %q", c, m)
		}
	}
}

func TestBlockedAddressMessageHidesResolvedIP(t *testing.T) {
	e := newEnv(t)
	e.resolver.set("internal-looking.example.com", []string{"10.1.2.3"})
	var logs bytes.Buffer
	e.opts.Logger = slog.New(slog.NewJSONHandler(&logs, nil))
	e.build()
	r := e.do(http.MethodGet, "https://internal-looking.example.com/", nil, nil)
	expectResult(t, r, BlockedAddress)
	if r.Message != "The destination is, or resolves to, a non-public address." {
		t.Fatalf("message %q", r.Message)
	}
	for _, s := range []string{r.Message, logs.String()} {
		if strings.Contains(s, "10.1.2.3") {
			t.Fatalf("resolved address leaked: %s", s)
		}
	}
}

func TestLogsOmitHeaderValuesAndBodies(t *testing.T) {
	e := newEnv(t)
	e.opts.ResponseHeaderTimeout = 100 * time.Millisecond
	_, port := e.server("example.com", func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body) // then the server notices when the client gives up
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	})
	var logs bytes.Buffer
	e.opts.Logger = slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	e.build()
	r := e.do(http.MethodPost, "https://example.com:"+port+"/hook",
		http.Header{"X-Api-Key": {"k123"}}, []byte(`{"card":"4242-secret-body"}`))
	expectResult(t, r, Timeout)
	out := logs.String()
	for _, want := range []string{`"category":"timeout"`, `"host":"example.com"`, `"duration_ms"`} {
		if !strings.Contains(out, want) {
			t.Errorf("log lacks %s: %s", want, out)
		}
	}
	for _, secret := range []string{"k123", "4242-secret-body", "X-Api-Key"} {
		if strings.Contains(out, secret) {
			t.Errorf("log contains %q: %s", secret, out)
		}
	}
}
