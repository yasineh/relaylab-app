package config

import (
	"testing"
	"time"
)

func TestRetentionDefaults(t *testing.T) {
	r, err := LoadRetention(replayEnv(nil), EnvProduction)
	if err != nil {
		t.Fatal(err)
	}
	want := Retention{
		EventTTL: 7 * 24 * time.Hour, MaxEventsPerProject: 10000, AuditTTL: 90 * 24 * time.Hour,
		JobTTL: 7 * 24 * time.Hour, Interval: 10 * time.Minute,
	}
	if *r != want {
		t.Fatalf("defaults %+v", *r)
	}
}

func TestRetentionOverrides(t *testing.T) {
	// A demo deployment keeps events for 24 hours.
	r, err := LoadRetention(replayEnv(map[string]string{
		"RETENTION_EVENT_TTL": "24h", "RETENTION_MAX_EVENTS_PER_PROJECT": "200",
		"RETENTION_AUDIT_TTL": "720h", "RETENTION_JOB_TTL": "48h", "RETENTION_INTERVAL": "5m",
	}), EnvProduction)
	if err != nil {
		t.Fatal(err)
	}
	if r.EventTTL != 24*time.Hour || r.MaxEventsPerProject != 200 || r.AuditTTL != 720*time.Hour ||
		r.JobTTL != 48*time.Hour || r.Interval != 5*time.Minute {
		t.Fatalf("%+v", *r)
	}
}

func TestRetentionZeroTTLOnlyOutsideProduction(t *testing.T) {
	dev, err := LoadRetention(replayEnv(map[string]string{"RETENTION_EVENT_TTL": "0"}), EnvDevelopment)
	if err != nil || dev.EventTTL != 0 {
		t.Fatalf("development 0: %+v %v", dev, err)
	}
	_, err = LoadRetention(replayEnv(map[string]string{"RETENTION_EVENT_TTL": "0"}), EnvProduction)
	assertFieldError(t, err, "RETENTION_EVENT_TTL")
}

func TestRetentionRejectsInvalidValues(t *testing.T) {
	for env, val := range map[string]string{
		"RETENTION_EVENT_TTL":              "7d", // Go durations have no days
		"RETENTION_AUDIT_TTL":              "1h",
		"RETENTION_JOB_TTL":                "-1h",
		"RETENTION_INTERVAL":               "10s",
		"RETENTION_MAX_EVENTS_PER_PROJECT": "0",
	} {
		_, err := LoadRetention(replayEnv(map[string]string{env: val}), EnvDevelopment)
		assertFieldError(t, err, env)
	}
}
