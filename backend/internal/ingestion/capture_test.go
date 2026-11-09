package ingestion

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"reflect"
	"strings"
	"testing"

	"relaylab/internal/platform/redact"
)

func newReq(method, target string, headers [][2]string) *http.Request {
	r := httptest.NewRequest(method, target, nil)
	for _, h := range headers {
		r.Header.Add(h[0], h[1])
	}
	return r
}

func TestCaptureHeadersSortedWithDuplicatesInArrivalOrder(t *testing.T) {
	r := newReq("POST", "http://relaylab.test/h/tok", [][2]string{
		{"X-B", "1"}, {"X-A", "2"}, {"X-B", "3"}, {"Authorization", "Bearer sk_live_abc123"},
	})
	c, ok := capture(r, "", "", redact.New(nil, nil), netip.Addr{}, false)
	if !ok {
		t.Fatal("capture rejected")
	}
	want := [][2]string{
		{"Authorization", redact.Marker},
		{"Host", "relaylab.test"},
		{"X-A", "2"},
		{"X-B", "1"},
		{"X-B", "3"},
	}
	if !reflect.DeepEqual(c.Headers, want) || c.RedactedHeaderCount != 1 {
		t.Fatalf("headers = %v (redacted %d)", c.Headers, c.RedactedHeaderCount)
	}
}

func TestCaptureSubpathAndQuery(t *testing.T) {
	r := newReq("GET", "/h/tok/orders/42?x=1&x=2&y=&api_key=k123", nil)
	c, ok := capture(r, "/orders/42", "/orders/42", redact.New(nil, nil), netip.Addr{}, false)
	if !ok {
		t.Fatal("capture rejected")
	}
	if c.Path != "/orders/42" || c.RawQuery != "x=1&x=2&y=&api_key=%5BREDACTED%5D" {
		t.Fatalf("path %q raw query %q", c.Path, c.RawQuery)
	}
	want := map[string][]string{"x": {"1", "2"}, "y": {""}, "api_key": {redact.Marker}}
	if !reflect.DeepEqual(c.Params, want) {
		t.Fatalf("params = %v", c.Params)
	}
}

func TestCapturePathThatTextCannotStoreKeepsEscapedForm(t *testing.T) {
	r := newReq("GET", "/h/tok/a%00b%FF", nil)
	c, _ := capture(r, "/a\x00b\xff", "/a%00b%FF", redact.New(nil, nil), netip.Addr{}, false)
	if c.Path != "/a%00b%FF" {
		t.Fatalf("path = %q", c.Path)
	}
}

func TestCaptureRejectsOverlongPathOrQuery(t *testing.T) {
	long := "/" + strings.Repeat("a", maxPathChars)
	if _, ok := capture(newReq("GET", "/h/tok"+long, nil), long, long, redact.New(nil, nil), netip.Addr{}, false); ok {
		t.Fatal("overlong path accepted")
	}
	q := "/h/tok?q=" + strings.Repeat("b", maxRawQueryChars)
	if _, ok := capture(newReq("GET", q, nil), "", "", redact.New(nil, nil), netip.Addr{}, false); ok {
		t.Fatal("overlong query accepted")
	}
	exact := "/" + strings.Repeat("a", maxPathChars-1)
	if _, ok := capture(newReq("GET", "/h/tok"+exact, nil), exact, exact, redact.New(nil, nil), netip.Addr{}, false); !ok {
		t.Fatal("path at the limit rejected")
	}
}

func TestCaptureMetadataTruncationAndSanitizing(t *testing.T) {
	r := newReq("POST", "/h/tok?n%00=v%FF", [][2]string{
		{"User-Agent", strings.Repeat("u", maxUserAgentChars+10)},
		{"Content-Type", "application/x-unknown; charset=bogus" + strings.Repeat("x", 300)},
	})
	c, _ := capture(r, "", "", redact.New(nil, nil), netip.Addr{}, false)
	if len([]rune(*c.UserAgent)) != maxUserAgentChars || len([]rune(*c.ContentType)) != maxContentTypeChars {
		t.Fatalf("not truncated: ua=%d ct=%d", len(*c.UserAgent), len(*c.ContentType))
	}
	// NUL and invalid UTF-8 cannot be stored in jsonb; the raw query keeps the original encoding.
	if !reflect.DeepEqual(c.Params, map[string][]string{"n�": {"v�"}}) || c.RawQuery != "n%00=v%FF" {
		t.Fatalf("params %v raw %q", c.Params, c.RawQuery)
	}
}

func TestTruncateIP(t *testing.T) {
	tests := map[string]string{
		"203.0.113.77":              "203.0.113.0",
		"::ffff:198.51.100.9":       "198.51.100.0",
		"2001:db8:abcd:12:3456::1":  "2001:db8:abcd::",
		"2001:db8:abcd:ffff:ffff::": "2001:db8:abcd::",
	}
	for in, want := range tests {
		if got := truncateIP(netip.MustParseAddr(in)).String(); got != want {
			t.Errorf("truncateIP(%s) = %s, want %s", in, got, want)
		}
	}
}

func TestHeaderFieldCount(t *testing.T) {
	r := newReq("GET", "http://relaylab.test/h/tok", [][2]string{{"A", "1"}, {"A", "2"}, {"B", "3"}})
	if n := headerFieldCount(r); n != 4 { // A, A, B, Host
		t.Fatalf("count = %d", n)
	}
}

func TestClassify(t *testing.T) {
	deep := strings.Repeat("[", maxJSONDepth+1) + strings.Repeat("]", maxJSONDepth+1)
	atLimit := strings.Repeat("[", maxJSONDepth) + strings.Repeat("]", maxJSONDepth)
	tests := []struct {
		name, ct string
		body     []byte
		enc      string
		json     string // "" means no parsed copy
		invalid  bool
	}{
		{"json", "application/json", []byte(`{"a":1}`), "utf8", `{"a":1}`, false},
		{"json charset and +json", "application/vnd.api+json; charset=utf-8", []byte(`[1, 2]`), "utf8", `[1,2]`, false},
		{"big number kept exact", "application/json", []byte(`{"n":12345678901234567890}`), "utf8", `{"n":12345678901234567890}`, false},
		{"form", "application/x-www-form-urlencoded", []byte("a=1&b=two"), "utf8", "", false},
		{"text", "text/plain", []byte("hello"), "utf8", "", false},
		{"binary", "application/octet-stream", []byte{0xff, 0xfe, 0x00}, "binary", "", false},
		{"empty", "", []byte{}, "utf8", "", false},
		{"invalid json", "application/json", []byte(`{"a":`), "utf8", "", true},
		{"binary json", "application/json", []byte{0xff, '{', '}'}, "binary", "", true},
		{"too deep", "application/json", []byte(deep), "utf8", "", false},
		{"at depth limit", "application/json", []byte(atLimit), "utf8", atLimit, false},
		{"brackets in strings ignored", "application/json", []byte(`{"s":"` + deep + `"}`), "utf8", `{"s":"` + deep + `"}`, false},
		{"nul escape skipped", "application/json", []byte(`{"a":"\u0000"}`), "utf8", "", false},
		{"lone surrogate normalized", "application/json", []byte(`{"a":"\ud800"}`), "utf8", `{"a":"` + "�" + `"}`, false},
		{"unknown type", "application/x-unknown; charset=bogus", []byte(`{"a":1}`), "utf8", "", false},
		{"malformed type", "application/json;;=", []byte(`{"a":1}`), "utf8", `{"a":1}`, false},
	}
	for _, tt := range tests {
		c := classify(tt.ct, tt.body)
		if c.Encoding != tt.enc || string(c.JSON) != tt.json || c.JSONInvalid != tt.invalid {
			t.Errorf("%s: got enc=%s json=%q invalid=%v", tt.name, c.Encoding, c.JSON, c.JSONInvalid)
		}
	}
}
