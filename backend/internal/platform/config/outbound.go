package config

import (
	"net"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Outbound defaults (outbound-request-guard spec).
const (
	defaultOutboundMaxResponseBytes = 64 << 10
	maxOutboundResponseBytes        = 256 << 10 // hard cap
	defaultOutboundMaxConcurrency   = 8
)

// Outbound is the worker's policy and limits for user-directed outbound
// requests (replay). The keys are the OUTBOUND_* names the spec defines.
type Outbound struct {
	// ConnectTimeout bounds the TCP connect and each DNS lookup
	// (OUTBOUND_CONNECT_TIMEOUT, default 3s).
	ConnectTimeout time.Duration
	// TLSHandshakeTimeout (OUTBOUND_TLS_TIMEOUT, default 3s).
	TLSHandshakeTimeout time.Duration
	// ResponseHeaderTimeout (OUTBOUND_HEADER_TIMEOUT, default 5s).
	ResponseHeaderTimeout time.Duration
	// TotalTimeout covers the whole request including the body read
	// (OUTBOUND_TOTAL_TIMEOUT, default 10s).
	TotalTimeout time.Duration
	// MaxResponseBytes is the response body kept (OUTBOUND_MAX_RESPONSE_BYTES,
	// default 65536, at most 262144); longer bodies are truncated.
	MaxResponseBytes int
	// MaxConcurrency is the in-flight requests per worker process
	// (OUTBOUND_MAX_CONCURRENCY, default 8).
	MaxConcurrency int

	// AllowHTTP permits http:// destinations (and port 80). Local
	// development only: refused when APP_ENV=production.
	AllowHTTP bool
	// AllowedPorts are the destination ports allowed (OUTBOUND_ALLOWED_PORTS,
	// default 443 and 8443; 80 is added when AllowHTTP is set).
	AllowedPorts []int
	// BlockedHosts are host names that are never destinations, with their
	// subdomains: OUTBOUND_BLOCKED_HOSTS plus RelayLab's own hosts (APP_ORIGIN,
	// INGEST_BASE_URL) and the database host(s) from DATABASE_URL. Built-in
	// rules for internal names (localhost, *.internal, ...) apply in addition.
	BlockedHosts []string
	// TestAllowlist lists exact "host:port" destinations the end-to-end test
	// stack may reach although they are internal (REPLAY_TEST_ALLOWLIST, for
	// example "echo:8080"). Only DNS names, never IP literals or RelayLab's
	// own hosts. Plain HTTP is allowed to these entries only. Refused when
	// APP_ENV=production.
	TestAllowlist []string
}

// LoadOutbound reads OUTBOUND_ALLOW_HTTP, OUTBOUND_ALLOWED_PORTS, and
// OUTBOUND_BLOCKED_HOSTS, and adds the control-plane hosts of cfg.
func LoadOutbound(getenv Getenv, cfg *Config) (*Outbound, error) {
	o := &Outbound{}
	switch v := strings.ToLower(strings.TrimSpace(getenv("OUTBOUND_ALLOW_HTTP"))); v {
	case "", "false", "0":
	case "true", "1":
		if cfg.Env == EnvProduction {
			return nil, &FieldError{Var: "OUTBOUND_ALLOW_HTTP", Reason: "must not be enabled in production"}
		}
		o.AllowHTTP = true
	default:
		return nil, &FieldError{Var: "OUTBOUND_ALLOW_HTTP", Reason: "must be true or false"}
	}

	durations := []struct {
		env string
		def string
		dst *time.Duration
	}{
		{"OUTBOUND_CONNECT_TIMEOUT", "3s", &o.ConnectTimeout},
		{"OUTBOUND_TLS_TIMEOUT", "3s", &o.TLSHandshakeTimeout},
		{"OUTBOUND_HEADER_TIMEOUT", "5s", &o.ResponseHeaderTimeout},
		{"OUTBOUND_TOTAL_TIMEOUT", "10s", &o.TotalTimeout},
	}
	for _, d := range durations {
		v, err := time.ParseDuration(withDefault(getenv(d.env), d.def))
		if err != nil || v <= 0 || v > time.Minute {
			return nil, &FieldError{Var: d.env, Reason: "must be a positive duration of at most 1m, such as 3s"}
		}
		*d.dst = v
	}
	if o.TotalTimeout < o.ConnectTimeout || o.TotalTimeout < o.ResponseHeaderTimeout {
		return nil, &FieldError{Var: "OUTBOUND_TOTAL_TIMEOUT", Reason: "must not be shorter than the connect or header timeout"}
	}
	var err error
	if o.MaxResponseBytes, err = positiveInt(getenv, "OUTBOUND_MAX_RESPONSE_BYTES", defaultOutboundMaxResponseBytes, maxOutboundResponseBytes); err != nil {
		return nil, err
	}
	if o.MaxConcurrency, err = positiveInt(getenv, "OUTBOUND_MAX_CONCURRENCY", defaultOutboundMaxConcurrency, 0); err != nil {
		return nil, err
	}

	ports := splitList(withDefault(getenv("OUTBOUND_ALLOWED_PORTS"), "443,8443"))
	for _, p := range ports {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return nil, &FieldError{Var: "OUTBOUND_ALLOWED_PORTS", Reason: "must be a comma-separated list of ports from 1 to 65535"}
		}
		if !slices.Contains(o.AllowedPorts, n) {
			o.AllowedPorts = append(o.AllowedPorts, n)
		}
	}
	if o.AllowHTTP && !slices.Contains(o.AllowedPorts, 80) {
		o.AllowedPorts = append(o.AllowedPorts, 80)
	}

	add := func(h string) {
		h = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(h)), ".")
		if h != "" && !slices.Contains(o.BlockedHosts, h) {
			o.BlockedHosts = append(o.BlockedHosts, h)
		}
	}
	for _, h := range splitList(getenv("OUTBOUND_BLOCKED_HOSTS")) {
		if strings.ContainsAny(h, "/:@ ") {
			return nil, &FieldError{Var: "OUTBOUND_BLOCKED_HOSTS", Reason: "must be a comma-separated list of host names"}
		}
		add(h)
	}
	add("metadata.google.internal")
	add("metadata")
	if cfg.AppOrigin != nil {
		add(cfg.AppOrigin.Hostname())
	}
	if raw := strings.TrimSpace(getenv("INGEST_BASE_URL")); raw != "" {
		u, err := url.Parse(raw)
		if err != nil || u.Hostname() == "" {
			return nil, &FieldError{Var: "INGEST_BASE_URL", Reason: "must be an absolute URL"}
		}
		add(u.Hostname())
	}
	for _, h := range dsnHosts(cfg.DatabaseURL.Reveal()) {
		add(h)
	}
	if o.TestAllowlist, err = loadTestAllowlist(getenv, cfg.Env, o.BlockedHosts); err != nil {
		return nil, err
	}
	return o, nil
}

const testAllowlistEnv = "REPLAY_TEST_ALLOWLIST"

var allowlistHost = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$`)

// loadTestAllowlist reads REPLAY_TEST_ALLOWLIST: comma-separated host:port
// entries for the end-to-end test stack. It is an escape hatch through the
// outbound guard, so it is refused in production, and entries can never name
// an IP literal or a host the guard always blocks for RelayLab itself.
func loadTestAllowlist(getenv Getenv, env Env, blocked []string) ([]string, error) {
	raw := strings.TrimSpace(getenv(testAllowlistEnv))
	if raw == "" {
		return nil, nil
	}
	if env == EnvProduction {
		return nil, &FieldError{Var: testAllowlistEnv, Reason: "is for the end-to-end test stack only and must not be set in production"}
	}
	var out []string
	for _, e := range splitList(raw) {
		host, portStr, err := net.SplitHostPort(strings.ToLower(e))
		port, perr := strconv.Atoi(portStr)
		if err != nil || perr != nil || port < 1 || port > 65535 || !allowlistHost.MatchString(host) || isNumericHost(host) {
			return nil, &FieldError{Var: testAllowlistEnv, Reason: "must be a comma-separated list of host:port entries with DNS host names, such as echo:8080"}
		}
		for _, b := range blocked {
			if host == b || strings.HasSuffix(host, "."+b) {
				return nil, &FieldError{Var: testAllowlistEnv, Reason: "must not name RelayLab's own hosts, the database, or metadata hosts"}
			}
		}
		entry := host + ":" + strconv.Itoa(port)
		if !slices.Contains(out, entry) {
			out = append(out, entry)
		}
	}
	return out, nil
}

// isNumericHost reports whether every label is digits (an IPv4-like literal).
func isNumericHost(h string) bool {
	for _, l := range strings.Split(h, ".") {
		if strings.Trim(l, "0123456789") != "" {
			return false
		}
	}
	return true
}

// dsnHosts returns the host names in a postgres:// URL, including every host
// of a multi-host URL (postgres://u@h1:5432,h2:5432/db).
func dsnHosts(dsn string) []string {
	u, err := url.Parse(dsn)
	if err != nil {
		return nil
	}
	var out []string
	for _, hp := range strings.Split(u.Host, ",") {
		h := hp
		if strings.HasPrefix(h, "[") {
			if i := strings.Index(h, "]"); i > 0 {
				h = h[1:i]
			}
		} else if i := strings.LastIndex(h, ":"); i >= 0 {
			h = h[:i]
		}
		if h != "" {
			out = append(out, h)
		}
	}
	return out
}

// positiveInt reads a positive integer with a default and an optional upper
// bound (0 means none).
func positiveInt(getenv Getenv, env string, def, upper int) (int, error) {
	raw := strings.TrimSpace(getenv(env))
	if raw == "" {
		return def, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 0, &FieldError{Var: env, Reason: "must be a positive integer"}
	}
	if upper > 0 && n > upper {
		return 0, &FieldError{Var: env, Reason: "must be at most " + strconv.Itoa(upper)}
	}
	return n, nil
}
