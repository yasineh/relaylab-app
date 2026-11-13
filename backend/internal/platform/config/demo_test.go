package config

import (
	"testing"
	"time"
)

func TestDemoDefaults(t *testing.T) {
	dev, err := LoadDemo(replayEnv(nil), EnvDevelopment)
	if err != nil {
		t.Fatal(err)
	}
	want := Demo{Enabled: true, TTL: 24 * time.Hour, MaxProjects: 1, MaxEndpoints: 2, MaxEvents: 200, MaxStreams: 2, IngestPerMinute: 10, IngestBurst: 5}
	if *dev != want {
		t.Fatalf("defaults %+v", *dev)
	}
	prod, err := LoadDemo(replayEnv(nil), EnvProduction)
	if err != nil || prod.Enabled {
		t.Fatalf("production: %+v %v", prod, err)
	}
	on, err := LoadDemo(replayEnv(map[string]string{"DEMO_ENABLED": "true", "DEMO_TTL": "90m", "DEMO_MAX_EVENTS": "50"}), EnvProduction)
	if err != nil || !on.Enabled || on.TTL != 90*time.Minute || on.MaxEvents != 50 {
		t.Fatalf("explicit: %+v %v", on, err)
	}
	for env, val := range map[string]string{
		"DEMO_ENABLED": "maybe", "DEMO_TTL": "1d", "DEMO_MAX_ENDPOINTS": "1", "DEMO_MAX_PROJECTS": "0",
	} {
		_, err := LoadDemo(replayEnv(map[string]string{env: val}), EnvDevelopment)
		assertFieldError(t, err, env)
	}
}
