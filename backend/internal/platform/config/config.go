// Package config loads and validates RelayLab process configuration from
// environment variables. Validation errors name the offending variable but
// never include its value.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

// Env is the deployment environment.
type Env string

// Supported environments.
const (
	EnvDevelopment Env = "development"
	EnvTest        Env = "test"
	EnvProduction  Env = "production"
)

// Config is the validated configuration shared by the api and worker processes.
type Config struct {
	Env              Env
	AppOrigin        *url.URL
	HTTPAddr         string
	WorkerHealthAddr string
	DatabaseURL      Secret
	LogLevel         slog.Level
	TrustedProxies   []netip.Prefix
	ShutdownGrace    time.Duration
	// StreamRevalidate is how often a live event stream re-checks its
	// session and project (at most a minute, per the live-event-stream spec).
	StreamRevalidate time.Duration
	Limits           Limits
}

// FieldError reports an invalid or missing environment variable.
// It intentionally carries only the variable name and a reason, never the value.
type FieldError struct {
	Var    string
	Reason string
}

func (e *FieldError) Error() string { return e.Var + ": " + e.Reason }

// Getenv looks up an environment variable; os.Getenv satisfies it.
type Getenv func(string) string

// Load reads and validates the process configuration. All problems are
// reported together as joined *FieldError values.
func Load(getenv Getenv) (*Config, error) {
	var errs []error
	fail := func(v, reason string) { errs = append(errs, &FieldError{Var: v, Reason: reason}) }

	c := &Config{}

	switch env := Env(strings.ToLower(strings.TrimSpace(getenv("APP_ENV")))); env {
	case "":
		c.Env = EnvDevelopment
	case EnvDevelopment, EnvTest, EnvProduction:
		c.Env = env
	default:
		fail("APP_ENV", "must be one of development, test, production")
	}

	origin := strings.TrimSpace(getenv("APP_ORIGIN"))
	if origin == "" && c.Env != EnvProduction {
		origin = "http://localhost:5173"
	}
	if origin == "" {
		fail("APP_ORIGIN", "is required in production")
	} else if u, err := parseOrigin(origin); err != nil {
		fail("APP_ORIGIN", err.Error())
	} else if c.Env == EnvProduction && u.Scheme != "https" {
		fail("APP_ORIGIN", "must use https in production")
	} else {
		c.AppOrigin = u
	}

	c.HTTPAddr = withDefault(getenv("HTTP_ADDR"), ":8080")
	c.WorkerHealthAddr = withDefault(getenv("WORKER_HEALTH_ADDR"), ":8081")

	if dsn := strings.TrimSpace(getenv("DATABASE_URL")); dsn == "" {
		fail("DATABASE_URL", "is required")
	} else if err := validateDSN(dsn); err != nil {
		fail("DATABASE_URL", err.Error())
	} else {
		c.DatabaseURL = NewSecret(dsn)
	}

	switch lvl := strings.ToLower(withDefault(getenv("LOG_LEVEL"), "info")); lvl {
	case "debug":
		c.LogLevel = slog.LevelDebug
	case "info":
		c.LogLevel = slog.LevelInfo
	case "warn":
		c.LogLevel = slog.LevelWarn
	case "error":
		c.LogLevel = slog.LevelError
	default:
		fail("LOG_LEVEL", "must be one of debug, info, warn, error")
	}

	for _, raw := range splitList(getenv("TRUSTED_PROXY_CIDRS")) {
		p, err := netip.ParsePrefix(raw)
		if err != nil {
			fail("TRUSTED_PROXY_CIDRS", "must be a comma-separated list of CIDRs")
			break
		}
		c.TrustedProxies = append(c.TrustedProxies, p.Masked())
	}

	grace, err := time.ParseDuration(withDefault(getenv("SHUTDOWN_GRACE"), "20s"))
	if err != nil || grace <= 0 {
		fail("SHUTDOWN_GRACE", "must be a positive duration such as 20s")
	} else {
		c.ShutdownGrace = grace
	}

	revalidate, err := time.ParseDuration(withDefault(getenv("STREAM_REVALIDATE_INTERVAL"), "60s"))
	if err != nil || revalidate < time.Second || revalidate > time.Minute {
		fail("STREAM_REVALIDATE_INTERVAL", "must be a duration from 1s to 60s")
	} else {
		c.StreamRevalidate = revalidate
	}

	limits, err := LoadLimits(getenv)
	if err != nil {
		errs = append(errs, err)
	} else {
		c.Limits = limits
	}

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return c, nil
}

// LoadMigrationURL reads MIGRATION_DATABASE_URL, used only by the migrate command.
func LoadMigrationURL(getenv Getenv) (Secret, error) {
	dsn := strings.TrimSpace(getenv("MIGRATION_DATABASE_URL"))
	if dsn == "" {
		return Secret{}, &FieldError{Var: "MIGRATION_DATABASE_URL", Reason: "is required"}
	}
	if err := validateDSN(dsn); err != nil {
		return Secret{}, &FieldError{Var: "MIGRATION_DATABASE_URL", Reason: err.Error()}
	}
	return NewSecret(dsn), nil
}

// IsProduction reports whether the process runs in production.
func (c *Config) IsProduction() bool { return c.Env == EnvProduction }

func parseOrigin(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, errors.New("must be an absolute http(s) origin such as https://app.example.com")
	}
	if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return nil, errors.New("must be an origin without path, query, fragment, or credentials")
	}
	u.Path = ""
	return u, nil
}

// validateDSN checks the URL shape without echoing it back in errors.
func validateDSN(dsn string) error {
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Host == "" {
		return fmt.Errorf("must be a postgres:// connection URL")
	}
	return nil
}

func withDefault(v, def string) string {
	if v = strings.TrimSpace(v); v == "" {
		return def
	}
	return v
}

func splitList(v string) []string {
	var out []string
	for _, part := range strings.Split(v, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}
