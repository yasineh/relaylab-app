package config

import (
	"slices"
	"testing"
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
