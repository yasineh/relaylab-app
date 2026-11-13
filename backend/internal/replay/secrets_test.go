package replay

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// dbContains reports whether any column of any app or auth row contains s.
func dbContains(t *testing.T, e *apiEnv, s string) bool {
	t.Helper()
	var n int
	if err := e.Admin.QueryRow(context.Background(), `
		SELECT count(*) FROM (
		  SELECT row_to_json(r)::text AS j FROM app.replay_attempts r
		  UNION ALL SELECT row_to_json(j)::text FROM app.jobs j
		  UNION ALL SELECT row_to_json(a)::text FROM app.audit_log a
		  UNION ALL SELECT row_to_json(w)::text FROM app.webhook_events w
		) x WHERE j LIKE '%' || $1 || '%'`, s).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n > 0
}

func TestOverrideValuesNeverStoredInPlaintext(t *testing.T) {
	e := newAPIEnv(t, apiOpts{})
	in := urlDest("https://hooks.example.com/a")
	in["header_overrides"] = map[string]string{"Authorization": "Bearer s3cret", "X-Mode": "m0de-value"}
	rec := e.alice.Do(http.MethodPost, e.eventPath(e.event), body(in))
	if rec.Code != 202 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	for _, secret := range []string{"s3cret", "m0de-value"} {
		if dbContains(t, e, secret) {
			t.Fatalf("%q stored in plaintext", secret)
		}
		if strings.Contains(rec.Body.String(), secret) {
			t.Fatalf("%q in the API response", secret)
		}
	}
	r := decode[ReplayJSON](t, rec)
	if len(r.HeaderOverrides) != 2 || r.HeaderOverrides[0].Value != "[REDACTED]" || !r.HeaderOverrides[0].Redacted {
		t.Fatalf("overrides shown as %+v", r.HeaderOverrides)
	}
	if e.Count(t, `SELECT count(*) FROM app.replay_attempts WHERE header_overrides_ciphertext IS NOT NULL AND header_override_names = '{Authorization,X-Mode}'`) != 1 {
		t.Fatal("ciphertext or names not stored")
	}
	// The ciphertext is bound to its replay: it cannot be opened as another.
	var ct []byte
	var id uuid.UUID
	_ = e.Admin.QueryRow(context.Background(), `SELECT id, header_overrides_ciphertext FROM app.replay_attempts`).Scan(&id, &ct)
	s, _ := NewSealer(testKey)
	if got, err := s.Open(ct, id); err != nil || len(got) != 2 {
		t.Fatalf("open: %v %v", got, err)
	}
	if _, err := s.Open(ct, uuid.New()); err == nil {
		t.Fatal("ciphertext opened under another replay id")
	}
}

func TestOverridesErasedOnEveryTerminalPath(t *testing.T) {
	e := newExecEnv(t)
	e.build()
	api := &apiEnv{Env: e.Env, t: t}
	sink := api.endpoint(t, e.project, "Sink", true)
	ov := []Override{{"Authorization", "Bearer s3cret"}}

	succeeded := e.queue("", &sink, ov)
	e.runOne()
	blocked := e.queue("https://blocked.example.test/", nil, ov) // NXDOMAIN in the fake resolver
	e.runOne()
	lost := e.queue("https://example.com/", nil, ov)
	if _, err := e.Admin.Exec(context.Background(), `UPDATE app.jobs SET state = 'running', lease_owner = 'gone', attempts = 3,
		lease_expires_at = now() - interval '1 second' WHERE payload->>'replay_id' = $1`, lost.String()); err != nil {
		t.Fatal(err)
	}
	if err := e.runner.Reap(context.Background()); err != nil {
		t.Fatal(err)
	}
	for name, id := range map[string]uuid.UUID{"succeeded": succeeded, "failed": blocked, "worker_lost": lost} {
		if n := e.Count(t, `SELECT count(*) FROM app.replay_attempts WHERE id = $1 AND state IN ('succeeded','failed') AND header_overrides_ciphertext IS NULL`, id); n != 1 {
			t.Errorf("%s: ciphertext not erased or not terminal", name)
		}
	}
	if r := e.result(lost); *r.FailureCategory != CategoryWorkerLost {
		t.Errorf("lost: %+v", r)
	}
}
