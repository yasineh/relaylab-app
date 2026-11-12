package config

import (
	"slices"
	"testing"
	"time"
)

func loadOutbound(t *testing.T, env map[string]string) (*Outbound, error) {
	t.Helper()
	base := validEnv()
	for k, v := range env {
		base[k] = v
	}
	c, err := Load(envFrom(base))
	if err != nil {
		t.Fatal(err)
	}
	return LoadOutbound(envFrom(base), c)
}

func TestOutboundBlocksControlPlaneAndDatabaseHosts(t *testing.T) {
	o, err := loadOutbound(t, map[string]string{
		"APP_ORIGIN":             "https://relaylab.example.com",
		"INGEST_BASE_URL":        "https://in.relaylab.example.net",
		"DATABASE_URL":           "postgres://app_runtime:pw@ep-cool-1.eu.aws.neon.tech:5432,Replica.DB.example:5433/relaylab",
		"OUTBOUND_BLOCKED_HOSTS": "Corp.Example., other.test",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{
		"relaylab.example.com", "in.relaylab.example.net", "ep-cool-1.eu.aws.neon.tech", "replica.db.example",
		"corp.example", "other.test", "metadata.google.internal", "metadata",
	} {
		if !slices.Contains(o.BlockedHosts, h) {
			t.Errorf("%s not in %v", h, o.BlockedHosts)
		}
	}
}

func TestOutboundDefaults(t *testing.T) {
	o, err := loadOutbound(t, nil)
	if err != nil {
		t.Fatal(err)
	}
	if o.AllowHTTP || !slices.Equal(o.AllowedPorts, []int{443, 8443}) {
		t.Fatalf("defaults: %+v", o)
	}
}

func TestOutboundRejectsBadValues(t *testing.T) {
	for k, v := range map[string]string{
		"OUTBOUND_ALLOW_HTTP":    "maybe",
		"OUTBOUND_ALLOWED_PORTS": "443,0",
		"OUTBOUND_BLOCKED_HOSTS": "https://x.example",
	} {
		if _, err := loadOutbound(t, map[string]string{k: v}); err == nil {
			t.Errorf("%s=%s accepted", k, v)
		} else {
			assertFieldError(t, err, k)
		}
	}
}

func TestOutboundHTTPRefusedInProduction(t *testing.T) {
	prod := map[string]string{"APP_ENV": "production", "APP_ORIGIN": "https://relaylab.example.com"}
	if _, err := loadOutbound(t, prod); err != nil {
		t.Fatalf("production without the flag: %v", err)
	}
	prod["OUTBOUND_ALLOW_HTTP"] = "true"
	_, err := loadOutbound(t, prod)
	assertFieldError(t, err, "OUTBOUND_ALLOW_HTTP")

	dev, err := loadOutbound(t, map[string]string{"OUTBOUND_ALLOW_HTTP": "true"})
	if err != nil || !dev.AllowHTTP || !slices.Contains(dev.AllowedPorts, 80) {
		t.Fatalf("development with the flag: %+v %v", dev, err)
	}
}

func TestOutboundLimitsDefaultsAndBounds(t *testing.T) {
	o, err := loadOutbound(t, nil)
	if err != nil {
		t.Fatal(err)
	}
	if o.ConnectTimeout != 3*time.Second || o.TLSHandshakeTimeout != 3*time.Second ||
		o.ResponseHeaderTimeout != 5*time.Second || o.TotalTimeout != 10*time.Second ||
		o.MaxResponseBytes != 64<<10 || o.MaxConcurrency != 8 {
		t.Fatalf("defaults %+v", o)
	}
	for k, v := range map[string]string{
		"OUTBOUND_MAX_RESPONSE_BYTES": "262145", // above the hard cap
		"OUTBOUND_MAX_CONCURRENCY":    "0",
		"OUTBOUND_CONNECT_TIMEOUT":    "0s",
		"OUTBOUND_HEADER_TIMEOUT":     "2m",
		"OUTBOUND_TOTAL_TIMEOUT":      "1s", // shorter than the header timeout
	} {
		_, err := loadOutbound(t, map[string]string{k: v})
		assertFieldError(t, err, k)
	}
	o, err = loadOutbound(t, map[string]string{"OUTBOUND_MAX_RESPONSE_BYTES": "262144", "OUTBOUND_TOTAL_TIMEOUT": "20s"})
	if err != nil || o.MaxResponseBytes != 256<<10 || o.TotalTimeout != 20*time.Second {
		t.Fatalf("%+v %v", o, err)
	}
}
