package events

import (
	"reflect"
	"testing"

	"relaylab/internal/platform/redact"
)

func TestMarkHeaders(t *testing.T) {
	red := redact.New([]string{"X-Partner-Key"}, nil)
	got := markHeaders(red, [][2]string{
		{"Authorization", "[REDACTED]"},
		{"Content-Type", "application/json"},
		{"Cookie", "[REDACTED]"},
		{"X-Hub-Signature-256", "[REDACTED]"}, // substring rule
		{"X-Note", "[REDACTED]"},              // literal marker, ordinary header
		{"X-Partner-Key", "[REDACTED]"},       // configured extra
		{"X-Trace", "a"},
		{"X-Trace", "b"}, // duplicates keep their order
	})
	want := []Header{
		{"Authorization", "[REDACTED]", true},
		{"Content-Type", "application/json", false},
		{"Cookie", "[REDACTED]", true},
		{"X-Hub-Signature-256", "[REDACTED]", true},
		{"X-Note", "[REDACTED]", false},
		{"X-Partner-Key", "[REDACTED]", true},
		{"X-Trace", "a", false},
		{"X-Trace", "b", false},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
	// A sensitive name whose stored value is not the marker is shown as is
	// (it cannot have been removed); this only arises for rows written before
	// a rule existed.
	if h := markHeaders(red, [][2]string{{"Authorization", ""}}); h[0].Redacted {
		t.Fatal("empty Authorization marked as redacted")
	}
}

func TestMarkQuery(t *testing.T) {
	red := redact.New(nil, nil)
	got, n := markQuery(red, map[string][]string{
		"token": {"[REDACTED]", "[REDACTED]"},
		"page":  {"2"},
		"note":  {"[REDACTED]"}, // literal marker on an ordinary param
		"a":     {"x", "y"},
	})
	want := []QueryParam{
		{"a", "x", false}, {"a", "y", false},
		{"note", "[REDACTED]", false},
		{"page", "2", false},
		{"token", "[REDACTED]", true}, {"token", "[REDACTED]", true},
	}
	if !reflect.DeepEqual(got, want) || n != 2 {
		t.Fatalf("got %+v (%d)\nwant %+v (2)", got, n, want)
	}
	if empty, n := markQuery(red, nil); empty == nil || len(empty) != 0 || n != 0 {
		t.Fatalf("no params: %#v, %d", empty, n)
	}
}

func TestRedactedCount(t *testing.T) {
	red := redact.New(nil, nil)
	m := markEvent(red,
		[][2]string{{"Authorization", "[REDACTED]"}, {"Cookie", "[REDACTED]"}, {"Accept", "*/*"}}, 2,
		map[string][]string{"api_key": {"[REDACTED]"}, "token": {"[REDACTED]"}, "q": {"x"}})
	if m.RedactedCount != 4 {
		t.Fatalf("redacted_count = %d, want 4 (2 headers + 2 query values)", m.RedactedCount)
	}
	// The header part comes from receipt, not from re-counting markers.
	if m := markEvent(red, [][2]string{{"X-Note", "[REDACTED]"}}, 0, nil); m.RedactedCount != 0 {
		t.Fatalf("literal marker counted: %d", m.RedactedCount)
	}
}
