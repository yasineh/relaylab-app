package replay

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestPreviewShowsSentAndStrippedHeaders(t *testing.T) {
	e := newAPIEnv(t, apiOpts{})
	in := urlDest("https://hooks.example.com/receive?x=1")
	in["header_overrides"] = map[string]string{"Authorization": "Bearer s3cret"}
	rec := e.alice.Do(http.MethodPost, e.eventPath(e.event)+"/preview", body(in))
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	p := decode[Preview](t, rec)
	if p.Method != http.MethodPost || p.Target != "https://hooks.example.com/receive?x=1" || p.BodySize != 20 ||
		p.ContentType == nil || *p.ContentType != "application/json" {
		t.Fatalf("preview %+v", p)
	}
	for _, name := range []string{"Authorization", "Cookie", "Stripe-Signature"} {
		if !slices.Contains(p.Stripped, name) {
			t.Errorf("%s not listed as not forwarded: %v", name, p.Stripped)
		}
	}
	sent := map[string]SentHeader{}
	for _, h := range p.Headers {
		sent[h.Name] = h
	}
	if sent["Content-Type"].Value != "application/json" || sent["X-Github-Event"].Value != "push" {
		t.Errorf("sent %v", p.Headers)
	}
	if a := sent["Authorization"]; a.Value != "[REDACTED]" || !a.Redacted {
		t.Errorf("override shown as %+v", a)
	}
	if _, ok := sent[ReplayIDHeader]; !ok {
		t.Error("replay id header not shown")
	}
	if strings.Contains(rec.Body.String(), "s3cret") {
		t.Fatal("override value in preview response")
	}
	// No side effects.
	for _, table := range []string{"app.replay_attempts", "app.jobs", "app.audit_log"} {
		if n := e.count(table); n != 0 {
			t.Errorf("%s has %d rows after a preview", table, n)
		}
	}
}

func TestPreviewSinkDestination(t *testing.T) {
	e := newAPIEnv(t, apiOpts{})
	rec := e.alice.Do(http.MethodPost, e.eventPath(e.event)+"/preview", body(sinkDest(e.sink)))
	p := decode[Preview](t, rec)
	if rec.Code != 200 || p.Target != "Test sink: Sink" || p.Destination.Type != "sink" || *p.Destination.EndpointID != e.sink.String() {
		t.Fatalf("%d %+v", rec.Code, p)
	}
}

func TestPreviewValidationMatchesCreation(t *testing.T) {
	e := newAPIEnv(t, apiOpts{})
	disabled := e.endpoint(t, e.project, "Off", false)
	for name, tc := range map[string]struct {
		in    map[string]any
		code  string
		field string
	}{
		"http scheme":       {urlDest("http://example.com/"), "invalid_destination", "destination.url"},
		"blocked port":      {urlDest("https://example.com:5432/"), "invalid_destination", "destination.url"},
		"metadata host":     {urlDest("https://metadata.google.internal/"), "invalid_destination", "destination.url"},
		"loopback":          {urlDest("https://127.0.0.1/"), "invalid_destination", "destination.url"},
		"control plane":     {urlDest("https://relaylab.test/h/x"), "invalid_destination", "destination.url"},
		"empty url":         {urlDest(""), "validation_failed", "destination.url"},
		"bad type":          {map[string]any{"destination": map[string]any{"type": "ftp"}}, "validation_failed", "destination.type"},
		"bad method":        {merge(urlDest("https://example.com/"), "method", "TRACE"), "validation_failed", "method"},
		"cookie override":   {merge(urlDest("https://example.com/"), "header_overrides", map[string]string{"Cookie": "sid=1"}), "validation_failed", "header_overrides"},
		"disabled sink":     {sinkDest(disabled), "invalid_destination", "destination.endpoint_id"},
		"malformed sink id": {map[string]any{"destination": map[string]any{"type": "sink", "endpoint_id": "x"}}, "validation_failed", "destination.endpoint_id"},
	} {
		for _, suffix := range []string{"/preview", ""} {
			rec := e.alice.Do(http.MethodPost, e.eventPath(e.event)+suffix, body(tc.in))
			env := expectError(t, rec, http.StatusUnprocessableEntity, tc.code)
			if env.Error.Fields[tc.field] == "" {
				t.Errorf("%s%s: no reason for %s: %v", name, suffix, tc.field, env.Error.Fields)
			}
		}
	}
	if n := e.count("app.replay_attempts"); n != 0 {
		t.Fatalf("%d replays created by invalid requests", n)
	}
}

func TestPreviewNotFoundAndUnavailable(t *testing.T) {
	e := newAPIEnv(t, apiOpts{})
	bobsProject := e.Project(t, "bob", "Bob's")
	bobsEndpoint := e.endpoint(t, bobsProject, "B", true)
	expectError(t, e.bob.Do(http.MethodPost, e.eventPath(e.event)+"/preview", body(urlDest("https://example.com/"))), 404, "not_found")
	expectError(t, e.alice.Do(http.MethodPost, e.eventPath(uuid.New())+"/preview", body(urlDest("https://example.com/"))), 404, "not_found")
	expectError(t, e.alice.Do(http.MethodPost, e.eventPath(e.event)+"/preview", body(sinkDest(bobsEndpoint))), 404, "not_found")

	e.status.setActive(false) // no recent active guard heartbeat
	expectError(t, e.alice.Do(http.MethodPost, e.eventPath(e.event)+"/preview", body(urlDest("https://example.com/"))), 503, "replay_unavailable")
	if rec := e.alice.Do(http.MethodPost, e.eventPath(e.event)+"/preview", body(sinkDest(e.sink))); rec.Code != 200 {
		t.Fatalf("sink preview without guard: %d %s", rec.Code, rec.Body)
	}
}

func merge(m map[string]any, k string, v any) map[string]any {
	m[k] = v
	return m
}
