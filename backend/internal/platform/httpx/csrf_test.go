package httpx

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestSameOrigin(t *testing.T) {
	origin, _ := url.Parse("https://relaylab.example.test")
	h := SameOrigin(origin)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	cases := []struct {
		method, origin, fetchSite string
		want                      int
	}{
		{http.MethodGet, "https://evil.example", "", http.StatusNoContent},
		{http.MethodHead, "", "cross-site", http.StatusNoContent},
		{http.MethodPost, "https://relaylab.example.test", "", http.StatusNoContent},
		{http.MethodDelete, "", "same-origin", http.StatusNoContent},
		{http.MethodPost, "https://evil.example", "", http.StatusForbidden},
		{http.MethodPut, "https://relaylab.example.test.evil.example", "", http.StatusForbidden},
		{http.MethodPatch, "http://relaylab.example.test", "", http.StatusForbidden},
		{http.MethodPost, "null", "", http.StatusForbidden},
		{http.MethodPost, "", "", http.StatusForbidden},
		{http.MethodPost, "", "same-site", http.StatusForbidden},
		{http.MethodPost, "", "cross-site", http.StatusForbidden},
		// An explicit, mismatching Origin wins over a same-origin Sec-Fetch-Site claim.
		{http.MethodPost, "https://evil.example", "same-origin", http.StatusForbidden},
	}
	for _, tc := range cases {
		r := httptest.NewRequest(tc.method, "/api/v1/projects", nil)
		if tc.origin != "" {
			r.Header.Set("Origin", tc.origin)
		}
		if tc.fetchSite != "" {
			r.Header.Set("Sec-Fetch-Site", tc.fetchSite)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if rec.Code != tc.want {
			t.Errorf("%s origin=%q site=%q: status %d, want %d", tc.method, tc.origin, tc.fetchSite, rec.Code, tc.want)
		}
		if rec.Code == http.StatusForbidden && !strings.Contains(rec.Body.String(), `"origin_rejected"`) {
			t.Errorf("body = %s", rec.Body)
		}
		for k := range rec.Header() {
			if strings.HasPrefix(strings.ToLower(k), "access-control-") {
				t.Errorf("CORS header emitted: %s", k)
			}
		}
	}
}

func TestRequireJSON(t *testing.T) {
	h := RequireJSON(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	cases := []struct {
		method, ctype, body string
		want                int
	}{
		{http.MethodPost, "application/json", `{}`, http.StatusNoContent},
		{http.MethodPost, "application/json; charset=utf-8", `{}`, http.StatusNoContent},
		{http.MethodPost, "text/plain", `{}`, http.StatusUnsupportedMediaType},
		{http.MethodPost, "application/x-www-form-urlencoded", `a=b`, http.StatusUnsupportedMediaType},
		{http.MethodPost, "multipart/form-data; boundary=x", `--x`, http.StatusUnsupportedMediaType},
		{http.MethodPost, "", `{}`, http.StatusUnsupportedMediaType},
		{http.MethodPost, "application/jsonx", `{}`, http.StatusUnsupportedMediaType},
		{http.MethodDelete, "", "", http.StatusNoContent}, // no body: no content type needed
		{http.MethodGet, "text/plain", "", http.StatusNoContent},
	}
	for _, tc := range cases {
		r := httptest.NewRequest(tc.method, "/api/v1/projects", strings.NewReader(tc.body))
		if tc.body == "" {
			r.ContentLength = 0
		}
		if tc.ctype != "" {
			r.Header.Set("Content-Type", tc.ctype)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if rec.Code != tc.want {
			t.Errorf("%s %q: status %d, want %d", tc.method, tc.ctype, rec.Code, tc.want)
		}
		if rec.Code == http.StatusUnsupportedMediaType && !strings.Contains(rec.Body.String(), `"unsupported_media_type"`) {
			t.Errorf("body = %s", rec.Body)
		}
	}
}
