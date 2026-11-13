package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

//nolint:gosec // G101: test-only values.
const (
	testSessionSecret = "test-only-better-auth-secret-0123456789abcdef"
	testProxySecret   = "test-only-proxy-shared-secret-0123456789abcdef"
)

func authEnv(extra map[string]string) Getenv {
	m := map[string]string{
		"BETTER_AUTH_SECRET":       testSessionSecret,
		"AUTH_PROXY_SHARED_SECRET": testProxySecret,
	}
	for k, v := range extra {
		m[k] = v
	}
	return envFrom(m)
}

func TestLoadAuthDevelopmentDefaults(t *testing.T) {
	a, err := LoadAuth(authEnv(nil), EnvDevelopment)
	if err != nil {
		t.Fatalf("LoadAuth: %v", err)
	}
	if a.ServiceURL.String() != "http://localhost:3001" {
		t.Errorf("ServiceURL = %s", a.ServiceURL)
	}
	if !a.SignupEnabled || !a.RequireEmailVerification {
		t.Errorf("signup=%v verify=%v, want true/true", a.SignupEnabled, a.RequireEmailVerification)
	}
	if a.CookieName != "relaylab.session_token" {
		t.Errorf("CookieName = %q", a.CookieName)
	}
	if a.SessionSecret.Reveal() != testSessionSecret || a.ProxySharedSecret.Reveal() != testProxySecret {
		t.Error("secrets not stored")
	}
}

func TestLoadAuthProductionDefaults(t *testing.T) {
	a, err := LoadAuth(authEnv(map[string]string{"AUTH_SERVICE_URL": "http://localhost:3001"}), EnvProduction)
	if err != nil {
		t.Fatalf("LoadAuth: %v", err)
	}
	if a.SignupEnabled {
		t.Error("sign-up must default to off in production")
	}
	if !a.RequireEmailVerification {
		t.Error("verification must default to on in production")
	}
	if a.CookieName != "__Secure-relaylab.session_token" {
		t.Errorf("CookieName = %q", a.CookieName)
	}
}

func TestLoadAuthExplicitSwitches(t *testing.T) {
	a, err := LoadAuth(authEnv(map[string]string{
		"AUTH_SERVICE_URL":                "http://auth:3001/",
		"AUTH_SIGNUP_ENABLED":             "true",
		"AUTH_REQUIRE_EMAIL_VERIFICATION": "false",
	}), EnvDevelopment)
	if err != nil {
		t.Fatalf("LoadAuth: %v", err)
	}
	if !a.SignupEnabled || a.RequireEmailVerification || a.ServiceURL.String() != "http://auth:3001" {
		t.Errorf("got %+v", a)
	}
}

func TestLoadAuthErrors(t *testing.T) {
	cases := []struct {
		name string
		env  Env
		vars map[string]string
		want string
	}{
		{"short session secret", EnvDevelopment, map[string]string{"BETTER_AUTH_SECRET": "short"}, "BETTER_AUTH_SECRET"},
		{"short proxy secret", EnvDevelopment, map[string]string{"AUTH_PROXY_SHARED_SECRET": "x"}, "AUTH_PROXY_SHARED_SECRET"},
		{"same secrets", EnvDevelopment, map[string]string{"AUTH_PROXY_SHARED_SECRET": testSessionSecret}, "AUTH_PROXY_SHARED_SECRET"},
		{"service URL required in production", EnvProduction, nil, "AUTH_SERVICE_URL"},
		{"service URL with path", EnvDevelopment, map[string]string{"AUTH_SERVICE_URL": "http://auth:3001/api"}, "AUTH_SERVICE_URL"},
		{"service URL with credentials", EnvDevelopment, map[string]string{"AUTH_SERVICE_URL": "http://u:p@auth:3001"}, "AUTH_SERVICE_URL"},
		{"bad boolean", EnvDevelopment, map[string]string{"AUTH_SIGNUP_ENABLED": "maybe"}, "AUTH_SIGNUP_ENABLED"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadAuth(authEnv(tc.vars), tc.env)
			assertFieldError(t, err, tc.want)
		})
	}
}

func TestLoadAuthErrorsNeverContainValues(t *testing.T) {
	_, err := LoadAuth(authEnv(map[string]string{
		"AUTH_PROXY_SHARED_SECRET": testSessionSecret,
		"AUTH_SERVICE_URL":         "http://user:hunter2@auth:3001",
	}), EnvDevelopment)
	if err == nil {
		t.Fatal("expected error")
	}
	for _, v := range []string{testSessionSecret, "hunter2"} {
		if strings.Contains(err.Error(), v) {
			t.Errorf("error leaks a value: %v", err)
		}
	}
}

func TestAuthSecretsNeverPrinted(t *testing.T) {
	a, err := LoadAuth(authEnv(nil), EnvDevelopment)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("cfg", slog.Any("auth", a))
	j, _ := json.Marshal(a)
	out := fmt.Sprintf("%v %+v %#v %s", a, a, a, j) + buf.String()
	for _, s := range []string{testSessionSecret, testProxySecret} {
		if strings.Contains(out, s) {
			t.Fatalf("secret printed: %s", out)
		}
	}
}

func TestPublicSignupInProductionNeedsAckAndVerification(t *testing.T) {
	load := func(kv map[string]string) error {
		kv["AUTH_SERVICE_URL"] = "http://localhost:3001"
		_, err := LoadAuth(authEnv(kv), EnvProduction)
		return err
	}
	assertFieldError(t, load(map[string]string{"AUTH_SIGNUP_ENABLED": "true"}), "AUTH_SIGNUP_ENABLED")
	assertFieldError(t, load(map[string]string{
		"AUTH_SIGNUP_ENABLED": "true", "AUTH_ALLOW_PUBLIC_SIGNUP_ACK": "true", "AUTH_REQUIRE_EMAIL_VERIFICATION": "false",
	}), "AUTH_REQUIRE_EMAIL_VERIFICATION")
	if err := load(map[string]string{"AUTH_SIGNUP_ENABLED": "true", "AUTH_ALLOW_PUBLIC_SIGNUP_ACK": "true"}); err != nil {
		t.Fatalf("acknowledged sign-up: %v", err)
	}
	if err := load(map[string]string{}); err != nil {
		t.Fatalf("default (sign-up off): %v", err)
	}
}
