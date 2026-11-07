package logging

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestDenylistedKeysDropped(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf, "api", "test", slog.LevelDebug)
	l.Info("hello",
		"Authorization", "Bearer abc123",
		"cookie", "sid=abc123",
		"set-cookie", "sid=abc123",
		"password", "abc123",
		"auth_token", "abc123",
		"client_secret", "abc123",
		"session_id", "abc123",
		"X-Api-Key", "abc123",
		slog.Group("req", "authorization", "abc123", "method", "GET"),
		"route", "/api/v1/projects",
	)
	out := buf.String()
	if strings.Contains(out, "abc123") {
		t.Fatalf("sensitive value logged: %s", out)
	}
	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if rec["route"] != "/api/v1/projects" || rec["service"] != "api" || rec["env"] != "test" {
		t.Fatalf("expected attributes missing: %v", rec)
	}
	if req, _ := rec["req"].(map[string]any); req["method"] != "GET" {
		t.Fatalf("non-sensitive grouped attr dropped: %v", rec)
	}
}

func TestOneJSONLinePerRecordWithInjection(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf, "api", "test", slog.LevelInfo)
	l.Info("first", "user_agent", "evil\n{\"level\":\"ERROR\",\"msg\":\"forged\"}")
	l.Warn("second")

	sc := bufio.NewScanner(&buf)
	lines := 0
	for sc.Scan() {
		lines++
		var rec map[string]any
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			t.Fatalf("line %d not valid JSON: %q", lines, sc.Text())
		}
		ts, _ := rec["time"].(string)
		parsed, err := time.Parse(time.RFC3339Nano, ts)
		if err != nil || parsed.Location() != time.UTC {
			t.Fatalf("time not UTC RFC3339: %q", ts)
		}
	}
	if lines != 2 {
		t.Fatalf("expected 2 lines, got %d", lines)
	}
}

func TestContextLogger(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf, "worker", "test", slog.LevelInfo).With("request_id", "abc-123")
	FromContext(WithLogger(context.Background(), l)).Info("x")
	if !strings.Contains(buf.String(), `"request_id":"abc-123"`) {
		t.Fatalf("context logger not used: %s", buf.String())
	}
	if FromContext(context.Background()) == nil {
		t.Fatal("expected default logger")
	}
}
