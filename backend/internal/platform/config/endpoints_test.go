package config

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"
	"testing"
)

var (
	testHMACKey = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0xA1}, 32))
	testEncKey  = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0xB2}, 32))
)

func endpointsEnv(extra map[string]string) Getenv {
	m := map[string]string{"ENDPOINT_TOKEN_HMAC_KEY": testHMACKey, "ENDPOINT_TOKEN_ENC_KEY": testEncKey}
	for k, v := range extra {
		m[k] = v
	}
	return envFrom(m)
}

func TestLoadEndpointsDefaultsToAppOrigin(t *testing.T) {
	origin, _ := url.Parse("https://relaylab.example.test")
	e, err := LoadEndpoints(endpointsEnv(nil), EnvProduction, origin)
	if err != nil {
		t.Fatal(err)
	}
	if e.IngestBaseURL.String() != "https://relaylab.example.test" {
		t.Errorf("IngestBaseURL = %s", e.IngestBaseURL)
	}
	if len(e.HMACKey()) != 32 || len(e.EncKey()) != 32 {
		t.Errorf("key lengths %d %d", len(e.HMACKey()), len(e.EncKey()))
	}
}

func TestLoadEndpointsIngestOverride(t *testing.T) {
	origin, _ := url.Parse("http://localhost:5173")
	e, err := LoadEndpoints(endpointsEnv(map[string]string{"INGEST_BASE_URL": "https://hooks.example.test/"}), EnvDevelopment, origin)
	if err != nil || e.IngestBaseURL.String() != "https://hooks.example.test" {
		t.Fatalf("%v %v", e, err)
	}
}

func TestLoadEndpointsAcceptsURLSafeBase64WithoutPadding(t *testing.T) {
	raw := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0xFB}, 32))
	if _, err := LoadEndpoints(endpointsEnv(map[string]string{"ENDPOINT_TOKEN_ENC_KEY": raw}), EnvDevelopment, nil); err != nil {
		t.Fatal(err)
	}
}

func TestLoadEndpointsRefusesMissingOrShortKeys(t *testing.T) {
	short := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 16))
	cases := []struct {
		name string
		env  Env
		vars map[string]string
		want string
	}{
		{"missing HMAC key", EnvDevelopment, map[string]string{"ENDPOINT_TOKEN_HMAC_KEY": ""}, "ENDPOINT_TOKEN_HMAC_KEY"},
		{"missing encryption key", EnvDevelopment, map[string]string{"ENDPOINT_TOKEN_ENC_KEY": ""}, "ENDPOINT_TOKEN_ENC_KEY"},
		{"short HMAC key", EnvDevelopment, map[string]string{"ENDPOINT_TOKEN_HMAC_KEY": short}, "ENDPOINT_TOKEN_HMAC_KEY"},
		{"short encryption key", EnvDevelopment, map[string]string{"ENDPOINT_TOKEN_ENC_KEY": short}, "ENDPOINT_TOKEN_ENC_KEY"},
		{"not base64", EnvDevelopment, map[string]string{"ENDPOINT_TOKEN_ENC_KEY": "not base64 !!"}, "ENDPOINT_TOKEN_ENC_KEY"},
		{"same key twice", EnvDevelopment, map[string]string{"ENDPOINT_TOKEN_ENC_KEY": testHMACKey}, "ENDPOINT_TOKEN_ENC_KEY"},
		{"ingest URL with path", EnvDevelopment, map[string]string{"INGEST_BASE_URL": "https://x.test/hooks"}, "INGEST_BASE_URL"},
		{"http ingest URL in production", EnvProduction, map[string]string{"INGEST_BASE_URL": "http://x.test"}, "INGEST_BASE_URL"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadEndpoints(endpointsEnv(tc.vars), tc.env, nil)
			assertFieldError(t, err, tc.want)
		})
	}
}

func TestEndpointKeysNeverPrinted(t *testing.T) {
	e, err := LoadEndpoints(endpointsEnv(nil), EnvDevelopment, nil)
	if err != nil {
		t.Fatal(err)
	}
	out := fmt.Sprintf("%v %+v %#v", e, e, e)
	for _, k := range [][]byte{e.HMACKey(), e.EncKey()} {
		if strings.Contains(out, string(k)) || strings.Contains(out, base64.StdEncoding.EncodeToString(k)) {
			t.Fatalf("key printed: %s", out)
		}
	}
}
