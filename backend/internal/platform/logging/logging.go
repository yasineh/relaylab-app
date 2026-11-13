// Package logging builds RelayLab's structured JSON logger and carries a
// request-scoped logger through context.
package logging

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"time"
)

// denylist holds attribute-key fragments that must never be logged. Matching is
// case-insensitive on the key after normalising '-' to '_'.
var denylist = []string{
	"authorization",
	"cookie",
	"password",
	"passwd",
	"token",
	"secret",
	"session",
	"api_key",
	"apikey",
	"credential",
	"dsn",
	"database_url",
}

// IsSensitiveKey reports whether an attribute key must be dropped.
func IsSensitiveKey(key string) bool {
	k := strings.ReplaceAll(strings.ToLower(key), "-", "_")
	for _, d := range denylist {
		if strings.Contains(k, d) {
			return true
		}
	}
	return false
}

// New returns a JSON logger that writes one line per record with UTC RFC 3339
// timestamps, the service and env attributes, a Cloud Logging "severity",
// and denylisted keys removed.
func New(w io.Writer, service, env string, level slog.Leveler) *slog.Logger {
	h := slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level:       level,
		ReplaceAttr: replaceAttr,
	})
	return slog.New(severityHandler{h}).With(slog.String("service", service), slog.String("env", env))
}

// severityHandler adds Cloud Logging's "severity" (DEBUG, INFO, WARNING,
// ERROR) to every record, next to slog's "level", so entries are classified
// correctly on Cloud Run.
type severityHandler struct{ slog.Handler }

func (h severityHandler) Handle(ctx context.Context, r slog.Record) error {
	r.AddAttrs(slog.String("severity", Severity(r.Level)))
	return h.Handler.Handle(ctx, r)
}

func (h severityHandler) WithAttrs(as []slog.Attr) slog.Handler {
	return severityHandler{h.Handler.WithAttrs(as)}
}

func (h severityHandler) WithGroup(name string) slog.Handler {
	return severityHandler{h.Handler.WithGroup(name)}
}

// Severity maps a slog level to a Cloud Logging severity.
func Severity(l slog.Level) string {
	switch {
	case l >= slog.LevelError:
		return "ERROR"
	case l >= slog.LevelWarn:
		return "WARNING"
	case l >= slog.LevelInfo:
		return "INFO"
	default:
		return "DEBUG"
	}
}

// ErrorReportingType marks a log entry as an error event for Cloud Error
// Reporting (the "@type" field of a structured log entry).
const ErrorReportingType = "type.googleapis.com/google.devtools.clouderrorreporting.v1beta1.ReportedErrorEvent"

// TraceKey is the field Cloud Logging uses to group one request's entries.
const TraceKey = "logging.googleapis.com/trace"

func replaceAttr(_ []string, a slog.Attr) slog.Attr {
	if a.Key == slog.TimeKey && a.Value.Kind() == slog.KindTime {
		return slog.String(slog.TimeKey, a.Value.Time().UTC().Format(time.RFC3339Nano))
	}
	if IsSensitiveKey(a.Key) {
		return slog.Attr{} // dropped
	}
	return a
}

type ctxKey struct{}

// WithLogger returns a context carrying l.
func WithLogger(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, l)
}

// FromContext returns the request-scoped logger, or slog.Default if none.
func FromContext(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(ctxKey{}).(*slog.Logger); ok && l != nil {
		return l
	}
	return slog.Default()
}
