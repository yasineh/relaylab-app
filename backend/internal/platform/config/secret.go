package config

import (
	"fmt"
	"log/slog"
)

const redacted = "[redacted]"

// Secret holds a sensitive configuration value. It never reveals its value
// through fmt, %v/%s/%q/%#v formatting, JSON encoding, or slog; callers must use
// Reveal explicitly at the point of use.
type Secret struct {
	value string
}

// NewSecret wraps a sensitive value.
func NewSecret(v string) Secret { return Secret{value: v} }

// Reveal returns the underlying value. Use only where the raw value is required,
// such as opening a database connection.
func (s Secret) Reveal() string { return s.value }

// IsZero reports whether the secret is empty.
func (s Secret) IsZero() bool { return s.value == "" }

// String implements fmt.Stringer.
func (s Secret) String() string { return redacted }

// GoString implements fmt.GoStringer so %#v is redacted too.
func (s Secret) GoString() string { return redacted }

// Format implements fmt.Formatter so every verb is redacted.
func (s Secret) Format(f fmt.State, _ rune) { _, _ = f.Write([]byte(redacted)) }

// LogValue implements slog.LogValuer.
func (s Secret) LogValue() slog.Value { return slog.StringValue(redacted) }

// MarshalText implements encoding.TextMarshaler (covers JSON and similar encoders).
func (s Secret) MarshalText() ([]byte, error) { return []byte(redacted), nil }
