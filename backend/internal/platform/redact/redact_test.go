package redact

import (
	"reflect"
	"strings"
	"testing"
)

func TestBuiltinHeaderNamesAnyCase(t *testing.T) {
	r := New(nil, nil)
	for _, name := range builtinHeaderNames {
		for _, variant := range []string{name, strings.ToUpper(name), titleCase(name)} {
			if !r.SensitiveHeader(variant) {
				t.Errorf("%q not redacted", variant)
			}
		}
	}
}

func TestSubstringAndProviderHeaders(t *testing.T) {
	r := New(nil, nil)
	for _, name := range []string{
		// Spec scenarios.
		"X-Shopify-Hmac-Signature", "X-Session-Id", "Client-Secret",
		// Provider signature and credential headers.
		"Stripe-Signature",                       // Stripe
		"X-Hub-Signature", "X-Hub-Signature-256", // GitHub
		"X-Slack-Signature",     // Slack
		"X-Shopify-Hmac-Sha256", // Shopify
		"X-Twilio-Signature",    // Twilio
		"X-Gitlab-Token",        // GitLab
		"Svix-Signature",        // Svix / Clerk
		"X-Webhook-Secret", "X-Refresh-Token", "X-Password", "X-Api_Key", "X-Credential-Id",
	} {
		if !r.SensitiveHeader(name) {
			t.Errorf("%q not redacted", name)
		}
	}
}

func TestNonSensitiveHeadersPassThrough(t *testing.T) {
	r := New(nil, nil)
	for _, name := range []string{
		"Content-Type", "Content-Length", "User-Agent", "Accept", "Host",
		"X-GitHub-Event", "X-GitHub-Delivery", "Stripe-Version", "X-Request-Id", "X-Forwarded-For",
	} {
		if r.SensitiveHeader(name) {
			t.Errorf("%q redacted but is not sensitive", name)
		}
	}
}

func TestHeadersKeepsNamesOrderAndDuplicates(t *testing.T) {
	r := New(nil, nil)
	in := [][2]string{
		{"Authorization", "Bearer sk_live_abc123"},
		{"Content-Type", "application/json"},
		{"Cookie", "sid=xyz"},
		{"Cookie", "theme=dark"},
		{"X-Github-Event", "push"},
	}
	got, n := r.Headers(in)
	want := [][2]string{
		{"Authorization", Marker},
		{"Content-Type", "application/json"},
		{"Cookie", Marker},
		{"Cookie", Marker},
		{"X-Github-Event", "push"},
	}
	if !reflect.DeepEqual(got, want) || n != 3 {
		t.Fatalf("got %v (n=%d), want %v (n=3)", got, n, want)
	}
	if in[0][1] != "Bearer sk_live_abc123" {
		t.Fatal("input was modified")
	}
}

func TestExtraConfiguredNames(t *testing.T) {
	r := New([]string{" X-Partner-Id ", ""}, []string{"Tenant"})
	if got, n := r.Headers([][2]string{{"X-Partner-Id", "p-77"}}); got[0][1] != Marker || n != 1 {
		t.Fatalf("extra header not redacted: %v", got)
	}
	if _, params, n := r.Query("tenant=t1"); params[0].Value != Marker || n != 1 {
		t.Fatalf("extra param not redacted: %v", params)
	}
	// Extras never remove built-ins.
	if !r.SensitiveHeader("Authorization") {
		t.Fatal("built-in lost after adding extras")
	}
	// A default redactor is unaffected by another's extras.
	if New(nil, nil).SensitiveHeader("X-Partner-Id") {
		t.Fatal("extras leaked into another redactor")
	}
}

func TestQuery(t *testing.T) {
	r := New(nil, nil)
	tests := []struct {
		raw     string
		wantRaw string
		want    []Param
		wantN   int
	}{
		{"", "", nil, 0},
		{"api_key=k123&page=2", "api_key=%5BREDACTED%5D&page=2", []Param{{"api_key", Marker}, {"page", "2"}}, 1},
		{"x=1&x=2&y=", "x=1&x=2&y=", []Param{{"x", "1"}, {"x", "2"}, {"y", ""}}, 0},
		{"access_token=a&KEY=b&sig=c&code=d&auth=e&signature=f", "access_token=%5BREDACTED%5D&KEY=%5BREDACTED%5D&sig=%5BREDACTED%5D&code=%5BREDACTED%5D&auth=%5BREDACTED%5D&signature=%5BREDACTED%5D",
			[]Param{{"access_token", Marker}, {"KEY", Marker}, {"sig", Marker}, {"code", Marker}, {"auth", Marker}, {"signature", Marker}}, 6},
		// Encoded names are matched after decoding; other bytes stay as received.
		{"api%5Fkey=s&q=a%20b+c", "api%5Fkey=%5BREDACTED%5D&q=a%20b+c", []Param{{"api_key", Marker}, {"q", "a b c"}}, 1},
		// A bare sensitive name has no value to hide.
		{"token&a=1", "token&a=1", []Param{{"token", ""}, {"a", "1"}}, 0},
		// Invalid escapes decode to their literal text; empty pieces are kept but skipped.
		{"a=%zz&&b=%", "a=%zz&&b=%", []Param{{"a", "%zz"}, {"b", "%"}}, 0},
		// "keyword" contains no sensitive substring and is not the exact name "key".
		{"keyword=go&monkey=1", "keyword=go&monkey=1", []Param{{"keyword", "go"}, {"monkey", "1"}}, 0},
	}
	for _, tt := range tests {
		raw, params, n := r.Query(tt.raw)
		if raw != tt.wantRaw || !reflect.DeepEqual(params, tt.want) || n != tt.wantN {
			t.Errorf("Query(%q) = %q, %v, %d; want %q, %v, %d", tt.raw, raw, params, n, tt.wantRaw, tt.want, tt.wantN)
		}
		if strings.Contains(raw, "k123") {
			t.Errorf("secret survived in %q", raw)
		}
	}
}

func titleCase(s string) string {
	parts := strings.Split(s, "-")
	for i, p := range parts {
		if p != "" {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, "-")
}
