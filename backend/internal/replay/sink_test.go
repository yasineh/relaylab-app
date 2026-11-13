package replay

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestSinkDeliveryCreatesEvent(t *testing.T) {
	e := newExecEnv(t)
	e.build()
	api := &apiEnv{Env: e.Env, t: t}
	sink := api.endpoint(t, e.project, "Sink", true)
	id := e.queue("", &sink, []Override{{"Authorization", "Bearer s3cret"}, {"X-Mode", "test"}})
	e.runOne()

	r := e.result(id)
	if r.State != "succeeded" || *r.ResponseStatus != 200 || *r.Response.Body.Text != `{"received":true}` || r.Tries != 1 {
		t.Fatalf("%+v", r)
	}
	var body []byte
	var source, method string
	var replayRef uuid.UUID
	var headers string
	if err := e.Admin.QueryRow(context.Background(), `SELECT body, source, replay_attempt_id, method, headers::text
		FROM app.webhook_events WHERE endpoint_id = $1`, sink).Scan(&body, &source, &replayRef, &method, &headers); err != nil {
		t.Fatalf("sink event: %v", err)
	}
	if string(body) != `{"hello":"relaylab"}` || source != "replay" || replayRef != id || method != "POST" {
		t.Fatalf("sink event %q %s %s %s", body, source, replayRef, method)
	}
	// Overrides are stored redacted like any received credential.
	if strings.Contains(headers, "s3cret") || !strings.Contains(headers, `"Authorization", "[REDACTED]"`) || !strings.Contains(headers, `"X-Mode", "test"`) {
		t.Fatalf("sink event headers %s", headers)
	}
	// The original event is untouched and still the only ingest event.
	if e.Count(t, `SELECT count(*) FROM app.webhook_events WHERE source = 'ingest'`) != 1 {
		t.Fatal("original events changed")
	}
}

func TestSinkDisabledAtDelivery(t *testing.T) {
	e := newExecEnv(t)
	e.build()
	api := &apiEnv{Env: e.Env, t: t}
	sink := api.endpoint(t, e.project, "Sink", true)
	id := e.queue("", &sink, nil)
	if _, err := e.Admin.Exec(context.Background(), `UPDATE app.endpoints SET enabled = false WHERE id = $1`, sink); err != nil {
		t.Fatal(err)
	}
	e.runOne()
	if r := e.result(id); r.State != "failed" || *r.FailureCategory != CategorySinkDisabled {
		t.Fatalf("%+v", r)
	}
	// Deleted sink: the replay is kept (sink cleared) and fails the same way.
	other := api.endpoint(t, e.project, "Gone", true)
	id2 := e.queue("", &other, nil)
	if _, err := e.Admin.Exec(context.Background(), `DELETE FROM app.endpoints WHERE id = $1`, other); err != nil {
		t.Fatal(err)
	}
	e.runOne()
	if r := e.result(id2); r.State != "failed" || *r.FailureCategory != CategorySinkDisabled || r.Target != "Test sink (endpoint deleted)" {
		t.Fatalf("%+v", r)
	}
	if e.Count(t, `SELECT count(*) FROM app.webhook_events WHERE source = 'replay'`) != 0 {
		t.Fatal("event stored on a disabled sink")
	}
}
