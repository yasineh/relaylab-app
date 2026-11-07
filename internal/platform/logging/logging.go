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
// timestamps, the service and env attributes, and denylisted keys removed.
func New(w io.Writer, service, env string, level slog.Leveler) *slog.Logger {
	h := slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level:       level,
		ReplaceAttr: replaceAttr,
	})
	return slog.New(h).With(slog.String("service", service), slog.String("env", env))
}

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
