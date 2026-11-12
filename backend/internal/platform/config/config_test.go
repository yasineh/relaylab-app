package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"
)

const testDSN = "postgres://app_runtime:sup3r-s3cret-pw@db:5432/relaylab"

func envFrom(m map[string]string) Getenv {
	return func(k string) string { return m[k] }
}

func validEnv() map[string]string {
	return map[string]string{"DATABASE_URL": testDSN}
}

func TestLoadDefaults(t *testing.T) {
	c, err := Load(envFrom(validEnv()))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Env != EnvDevelopment || c.HTTPAddr != ":8080" || c.WorkerHealthAddr != ":8081" {
		t.Errorf("unexpected defaults: %+v", c)
	}
	if c.AppOrigin.String() != "http://localhost:5173" {
		t.Errorf("AppOrigin = %s", c.AppOrigin)
	}
	if c.ShutdownGrace != 20*time.Second || c.LogLevel != slog.LevelInfo {
		t.Errorf("grace=%v level=%v", c.ShutdownGrace, c.LogLevel)
	}
	if c.DatabaseURL.Reveal() != testDSN {
		t.Errorf("DatabaseURL not stored")
	}
	if c.Limits != DefaultLimits() {
		t.Errorf("limits = %+v", c.Limits)
	}
}

func TestLoadMissingDatabaseURL(t *testing.T) {
	_, err := Load(envFrom(map[string]string{}))
	assertFieldError(t, err, "DATABASE_URL")
}

func TestLoadMalformedValues(t *testing.T) {
	cases := map[string]string{
		"APP_ENV":             "staging",
		"APP_ORIGIN":          "https://app.example.com/path",
		"DATABASE_URL":        "mysql://x@y/z",
		"LOG_LEVEL":           "verbose",
		"TRUSTED_PROXY_CIDRS": "10.0.0.0/8,not-a-cidr",
		"SHUTDOWN_GRACE":      "-5s",
	}
	for k, v := range cases {
		t.Run(k, func(t *testing.T) {
			env := validEnv()
			env[k] = v
			_, err := Load(envFrom(env))
			assertFieldError(t, err, k)
		})
	}
	// The stream must re-check sessions at least every minute (spec).
	for _, v := range []string{"61s", "2m", "500ms", "0s", "soon"} {
		t.Run("STREAM_REVALIDATE_INTERVAL="+v, func(t *testing.T) {
			env := validEnv()
			env["STREAM_REVALIDATE_INTERVAL"] = v
			_, err := Load(envFrom(env))
			assertFieldError(t, err, "STREAM_REVALIDATE_INTERVAL")
		})
	}
}

func TestStreamRevalidateDefault(t *testing.T) {
	c, err := Load(envFrom(validEnv()))
	if err != nil {
		t.Fatal(err)
	}
	if c.StreamRevalidate != time.Minute {
		t.Fatalf("default = %s, want 1m", c.StreamRevalidate)
	}
	env := validEnv()
	env["STREAM_REVALIDATE_INTERVAL"] = "15s"
	if c, err = Load(envFrom(env)); err != nil || c.StreamRevalidate != 15*time.Second {
		t.Fatalf("15s: %v %v", c, err)
	}
}

func TestProductionRequiresHTTPSOrigin(t *testing.T) {
	env := validEnv()
	env["APP_ENV"] = "production"
	_, err := Load(envFrom(env))
	assertFieldError(t, err, "APP_ORIGIN")

	env["APP_ORIGIN"] = "http://relaylab.example.com"
	_, err = Load(envFrom(env))
	assertFieldError(t, err, "APP_ORIGIN")

	env["APP_ORIGIN"] = "https://relaylab.example.com"
	if _, err := Load(envFrom(env)); err != nil {
		t.Fatalf("valid production config rejected: %v", err)
	}
}

func TestErrorsNeverContainValues(t *testing.T) {
	env := map[string]string{
		"DATABASE_URL":   "mysql://user:sup3r-s3cret-pw@host/db",
		"SHUTDOWN_GRACE": "sup3r-s3cret-pw",
	}
	_, err := Load(envFrom(env))
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "sup3r-s3cret-pw") {
		t.Fatalf("error leaks value: %q", err)
	}
}

func TestLoadMigrationURL(t *testing.T) {
	if _, err := LoadMigrationURL(envFrom(nil)); err == nil {
		t.Fatal("expected error for missing MIGRATION_DATABASE_URL")
	}
	s, err := LoadMigrationURL(envFrom(map[string]string{"MIGRATION_DATABASE_URL": testDSN}))
	if err != nil || s.Reveal() != testDSN {
		t.Fatalf("got %v", err)
	}
}

func TestSecretNeverFormats(t *testing.T) {
	s := NewSecret("sup3r-s3cret-pw")
	outs := []string{
		s.String(),
		fmt.Sprint(s), fmt.Sprintf("%v %s %q %+v %#v %x", s, s, s, s, s, s),
		fmt.Sprintf("%v", struct{ S Secret }{s}),
		fmt.Sprintf("%+v", &Config{DatabaseURL: s}),
	}
	b, _ := json.Marshal(map[string]Secret{"dsn": s})
	outs = append(outs, string(b))

	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("x", "dsn", s, "cfg", Config{DatabaseURL: s})
	outs = append(outs, buf.String())

	for _, o := range outs {
		if strings.Contains(o, "sup3r-s3cret-pw") {
			t.Fatalf("secret leaked in %q", o)
		}
	}
}

func assertFieldError(t *testing.T, err error, name string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error naming %s", name)
	}
	var fe *FieldError
	found := false
	for _, e := range flatten(err) {
		if errors.As(e, &fe) && fe.Var == name {
			found = true
		}
	}
	if !found {
		t.Fatalf("error %q does not name %s", err, name)
	}
}

func flatten(err error) []error {
	if u, ok := err.(interface{ Unwrap() []error }); ok {
		var out []error
		for _, e := range u.Unwrap() {
			out = append(out, flatten(e)...)
		}
		return out
	}
	return []error{err}
}
