package config

import (
	"net/url"
	"slices"
	"strconv"
	"strings"
)

// Outbound is the worker's policy for user-directed outbound requests
// (replay). Numeric limits (timeouts, response cap, concurrency) are in
// Limits as LIMIT_OUTBOUND_*.
type Outbound struct {
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
	return o, nil
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
