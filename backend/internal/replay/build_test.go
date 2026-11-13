package replay

import (
	"bytes"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"relaylab/internal/platform/redact"
)

var red = redact.New(nil, nil)

func TestBuildRequestFiltersHeaders(t *testing.T) {
	ev := StoredEvent{Method: "POST", Body: []byte(`{}`), Headers: [][2]string{
		{"Authorization", "[REDACTED]"}, // removed at receipt
		{"Cookie", "[REDACTED]"},
		{"Content-Type", "application/json"},
		{"Stripe-Signature", "t=1,v1=abc"}, // stored (e.g. older redaction rules): still stripped
		{"X-Hub-Signature", "sha1=x"},
		{"X-Hub-Signature-256", "sha256=x"},
		{"X-Signature", "x"},
		{"Svix-Signature", "v1,x"},
		{"Webhook-Signature", "x"},
		{"X-Shopify-Hmac-Sha256", "x"},
		{"X-Twilio-Signature", "x"},
		{"X-Slack-Signature", "x"},
		{"X-Custom-Hmac", "x"},
		{"X-Api-Key", "k"},
		{"X-Github-Event", "push"},
		{"X-Github-Delivery", "d1"},
		{"Connection", "keep-alive"},
		{"Content-Length", "2"},
		{"Host", "relaylab.example"},
		{"X-Forwarded-For", "203.0.113.9"},
		{"Forwarded", "for=x"},
		{"X-Real-Ip", "1.2.3.4"},
		{"Transfer-Encoding", "chunked"},
		{"User-Agent", "GitHub-Hookshot/1"},
		{"X-Relaylab-Replay-Id", "spoofed"},
		{"X-Github-Event", "ping"}, // repeated header keeps both values
		{"X-Weird", "[REDACTED]"},  // a literal marker is never forwarded
	}}
	id := uuid.Must(uuid.NewV7())
	b := BuildRequest(ev, "POST", nil, id, red)

	if got := b.Header.Values("X-Github-Event"); !slices.Equal(got, []string{"push", "ping"}) {
		t.Errorf("X-Github-Event %v", got)
	}
	for _, kept := range []string{"Content-Type", "X-Github-Delivery"} {
		if b.Header.Get(kept) == "" {
			t.Errorf("%s not forwarded", kept)
		}
	}
	if len(b.Header) != 3 {
		t.Errorf("forwarded %v", b.Header)
	}
	for _, name := range []string{
		"Authorization", "Cookie", "Stripe-Signature", "X-Hub-Signature", "X-Hub-Signature-256", "X-Signature",
		"Svix-Signature", "Webhook-Signature", "X-Shopify-Hmac-Sha256", "X-Twilio-Signature", "X-Slack-Signature",
		"X-Custom-Hmac", "X-Api-Key", "Connection", "Content-Length", "Host", "X-Forwarded-For", "Forwarded",
		"X-Real-Ip", "Transfer-Encoding", "User-Agent", "X-Weird",
	} {
		if !slices.Contains(b.Stripped, name) {
			t.Errorf("%s not listed as stripped: %v", name, b.Stripped)
		}
	}
	if b.Explicit.Get(ReplayIDHeader) != id.String() || len(b.Explicit) != 1 {
		t.Errorf("explicit %v", b.Explicit)
	}
	if last := b.Sent[len(b.Sent)-1]; last.Name != ReplayIDHeader || last.Value != id.String() {
		t.Errorf("replay id not shown: %+v", last)
	}
}

func TestBuildRequestBodyAndMethod(t *testing.T) {
	body := []byte{0xff, 0x00, 0xfe, 'x', 0x80}
	b := BuildRequest(StoredEvent{Method: "POST", Body: body, Headers: [][2]string{{"Content-Type", "application/octet-stream"}}}, http.MethodPut, nil, uuid.New(), red)
	if !bytes.Equal(b.Body, body) || b.Method != http.MethodPut || b.Header.Get("Content-Type") != "application/octet-stream" {
		t.Fatalf("%+v", b)
	}
}

func TestOverridesAppliedLastAndShownRedacted(t *testing.T) {
	ev := StoredEvent{Method: "POST", Headers: [][2]string{{"X-Mode", "live"}, {"Stripe-Signature", "old"}}}
	b := BuildRequest(ev, "POST", []Override{{"X-Mode", "test"}, {"Stripe-Signature", "t=1,v1=abc"}, {"Authorization", "Bearer s3cret"}}, uuid.New(), red)
	if b.Header.Get("X-Mode") != "" || b.Explicit.Get("X-Mode") != "test" || b.Explicit.Get("Stripe-Signature") != "t=1,v1=abc" ||
		b.Explicit.Get("Authorization") != "Bearer s3cret" {
		t.Fatalf("header %v explicit %v", b.Header, b.Explicit)
	}
	for _, s := range b.Sent {
		if strings.Contains(s.Value, "s3cret") || strings.Contains(s.Value, "v1=abc") || (s.Name == "X-Mode" && s.Value != redact.Marker) {
			t.Errorf("override value shown: %+v", s)
		}
	}
}

func TestValidateOverrides(t *testing.T) {
	ok := []Override{{"Authorization", "Bearer x"}, {"Stripe-Signature", "t=1"}, {"X-Custom", strings.Repeat("a", MaxOverrideValue)}}
	if msg := ValidateOverrides(ok); msg != "" {
		t.Fatalf("valid overrides rejected: %s", msg)
	}
	many := make([]Override, MaxOverrides+1)
	for i := range many {
		many[i] = Override{Name: "X-H" + string(rune('a'+i)), Value: "1"}
	}
	for name, o := range map[string][]Override{
		"21 entries":          many,
		"space in name":       {{"Bad Name", "1"}},
		"empty name":          {{"", "1"}},
		"CRLF in value":       {{"X-A", "a\r\nX-Injected: 1"}},
		"LF in value":         {{"X-A", "a\nb"}},
		"NUL in value":        {{"X-A", "a\x00"}},
		"value too long":      {{"X-A", strings.Repeat("a", MaxOverrideValue+1)}},
		"Cookie":              {{"Cookie", "sid=1"}},
		"Host":                {{"Host", "internal"}},
		"Proxy-Authorization": {{"Proxy-Authorization", "x"}},
		"Connection":          {{"Connection", "close"}},
		"X-Forwarded-For":     {{"X-Forwarded-For", "1.2.3.4"}},
		"Transfer-Encoding":   {{"Transfer-Encoding", "chunked"}},
		"replay id":           {{"X-RelayLab-Replay-Id", "x"}},
		"duplicate":           {{"X-A", "1"}, {"x-a", "2"}},
	} {
		if ValidateOverrides(o) == "" {
			t.Errorf("%s accepted", name)
		}
	}
}
