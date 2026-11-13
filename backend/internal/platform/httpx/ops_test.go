package httpx

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"relaylab/internal/platform/logging"
)

// A panic while handling a request that carries credentials: the client gets
// the 500 envelope without a stack trace; the log gets exactly one Cloud
// Error Reporting entry with the request id and no header value or body.
func TestPanicIsOneErrorReportingEntryWithoutSecrets(t *testing.T) {
	var logs bytes.Buffer
	r := chi.NewRouter()
	r.Use(RequestIDMiddleware(logging.New(&logs, "api", "production", slog.LevelInfo)), AccessLog, Recoverer)
	r.Post("/api/v1/boom", func(http.ResponseWriter, *http.Request) { panic("handler bug") })

	req := httptest.NewRequest(http.MethodPost, "/api/v1/boom", strings.NewReader(`{"card":"4242-body-secret"}`))
	req.Header.Set("Authorization", "Bearer sk_live_header_secret")
	req.Header.Set("Cookie", "relaylab.session_token=cookie-secret")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != 500 || !strings.Contains(rec.Body.String(), `"code":"internal_error"`) ||
		strings.Contains(rec.Body.String(), "goroutine") || strings.Contains(rec.Body.String(), "handler bug") {
		t.Fatalf("response: %d %s", rec.Code, rec.Body)
	}
	out := logs.String()
	for _, secret := range []string{"sk_live_header_secret", "cookie-secret", "4242-body-secret"} {
		if strings.Contains(out, secret) {
			t.Fatalf("log contains %q", secret)
		}
	}
	var errorEntries int
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		var e map[string]any
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("not JSON: %s", line)
		}
		if e["@type"] == logging.ErrorReportingType {
			errorEntries++
			if e["severity"] != "ERROR" || e["request_id"] != rec.Header().Get(RequestIDHeader) ||
				!strings.Contains(e["stack_trace"].(string), "panic: handler bug") || e["route"] != "/api/v1/boom" {
				t.Fatalf("error entry: %v", e)
			}
		}
	}
	if errorEntries != 1 {
		t.Fatalf("%d error-reporting entries: %s", errorEntries, out)
	}
}

func TestSeverityAndTrace(t *testing.T) {
	var logs bytes.Buffer
	l := logging.New(&logs, "api", "production", slog.LevelDebug)
	l.Debug("d")
	l.Info("i")
	l.Warn("w")
	l.Error("e")
	want := []string{"DEBUG", "INFO", "WARNING", "ERROR"}
	for i, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		var e map[string]any
		_ = json.Unmarshal([]byte(line), &e)
		if e["severity"] != want[i] {
			t.Errorf("line %d severity %v", i, e["severity"])
		}
	}

	old := traceProject
	defer func() { traceProject = old }()
	traceProject = ""
	if traceName("0123456789abcdef0123456789abcdef/1;o=1") != "" {
		t.Fatal("trace without a project")
	}
	traceProject = "example-project"
	if got := traceName("0123456789abcdef0123456789abcdef/1;o=1"); got != "projects/example-project/traces/0123456789abcdef0123456789abcdef" {
		t.Fatalf("trace %q", got)
	}
	for _, bad := range []string{"", "nothex/1", "0123456789abcdef0123456789abcdef0/1", `"injected"/1`} {
		if traceName(bad) != "" {
			t.Errorf("%q accepted", bad)
		}
	}
}
