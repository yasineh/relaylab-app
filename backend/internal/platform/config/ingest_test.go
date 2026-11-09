package config

import (
	"reflect"
	"testing"
)

func TestLoadIngest(t *testing.T) {
	in, err := LoadIngest(envFrom(nil))
	if err != nil || in.MetricsAddr != "" || in.RedactExtraHeaders != nil || in.RedactExtraParams != nil {
		t.Fatalf("defaults = %+v, %v", in, err)
	}
	in, err = LoadIngest(envFrom(map[string]string{
		"INGEST_REDACT_EXTRA_HEADERS": " X-Partner-Id , X-Tenant ",
		"INGEST_REDACT_EXTRA_PARAMS":  "tenant",
		"METRICS_ADDR":                "127.0.0.1:9090",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in.RedactExtraHeaders, []string{"X-Partner-Id", "X-Tenant"}) ||
		!reflect.DeepEqual(in.RedactExtraParams, []string{"tenant"}) || in.MetricsAddr != "127.0.0.1:9090" {
		t.Fatalf("got %+v", in)
	}
}

func TestLoadIngestInvalid(t *testing.T) {
	_, err := LoadIngest(envFrom(map[string]string{"METRICS_ADDR": "9090"}))
	assertFieldError(t, err, "METRICS_ADDR")
	_, err = LoadIngest(envFrom(map[string]string{"INGEST_REDACT_EXTRA_HEADERS": "X-Bad: value"}))
	assertFieldError(t, err, "INGEST_REDACT_EXTRA_HEADERS")
}
