package outbound

import (
	"net/http"
	"sync/atomic"
	"testing"
)

func TestRedirectsNotFollowed(t *testing.T) {
	for _, tc := range []struct {
		status       int
		location     string
		wantLocation string
		followedHost string
	}{
		{http.StatusFound, "http://169.254.169.254/computeMetadata/v1/", "http://169.254.169.254/computeMetadata/v1/", ""},
		{http.StatusMovedPermanently, "https://other.example.com/x", "https://other.example.com/x", "other.example.com"},
		{http.StatusTemporaryRedirect, "https://user:pw@other.example.com/y", "https://other.example.com/y", "other.example.com"},
		{http.StatusPermanentRedirect, "/relative", "/relative", ""},
	} {
		e := newEnv(t)
		var hits atomic.Int32
		_, port := e.server("example.com", func(w http.ResponseWriter, _ *http.Request) {
			hits.Add(1)
			w.Header().Set("Location", tc.location)
			w.WriteHeader(tc.status)
		})
		e.resolver.set("other.example.com", []string{"93.184.216.34"})
		e.build()
		r := e.do(http.MethodPost, "https://example.com:"+port+"/", nil, []byte("{}"))
		expectResult(t, r, Succeeded)
		if r.Status != tc.status || r.Location != tc.wantLocation {
			t.Errorf("%d %s: status %d location %q", tc.status, tc.location, r.Status, r.Location)
		}
		if n := hits.Load(); n != 1 {
			t.Errorf("%d %s: %d requests sent, want exactly 1", tc.status, tc.location, n)
		}
		if tc.followedHost != "" && e.resolver.calls[tc.followedHost] != 0 {
			t.Errorf("%d: redirect target %s was resolved", tc.status, tc.followedHost)
		}
	}
}

func TestSanitizeLocation(t *testing.T) {
	for in, want := range map[string]string{
		"":                                  "",
		"https://a:b@x.example/p?q#f":       "https://x.example/p?q#f",
		"https://x.example/a\r\nSet-Cookie": "https://x.example/aSet-Cookie",
		"https://x.example/@not-userinfo":   "https://x.example/@not-userinfo",
	} {
		if got := sanitizeLocation(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}
