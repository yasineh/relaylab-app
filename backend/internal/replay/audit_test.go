package replay

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

type auditPage struct {
	Items      []AuditJSON `json:"items"`
	NextCursor *string     `json:"next_cursor"`
}

func (e *apiEnv) auditPath() string { return "/api/v1/projects/" + e.project.String() + "/audit-log" }

func TestAuditLogListsReplayCreations(t *testing.T) {
	e := newAPIEnv(t, apiOpts{})
	if decode[auditPage](t, e.alice.Do(http.MethodGet, e.auditPath(), "")).Items == nil {
		t.Fatal("empty audit log is not an empty list")
	}
	r := decode[ReplayJSON](t, e.alice.Do(http.MethodPost, e.eventPath(e.event), body(sinkDest(e.sink))))
	p := decode[auditPage](t, e.alice.Do(http.MethodGet, e.auditPath(), ""))
	if len(p.Items) != 1 {
		t.Fatalf("%d entries", len(p.Items))
	}
	a := p.Items[0]
	if a.Action != "replay.created" || a.ResourceType != "replay" || *a.ResourceID != r.ID || *a.ActorUserID != "alice" ||
		*a.ActorName != "alice" || a.Metadata["destination_type"] != "sink" || a.Metadata["sink_endpoint_id"] != e.sink.String() ||
		a.Metadata["event_id"] != e.event.String() || a.Metadata["method"] != "POST" {
		t.Fatalf("entry %+v", a)
	}
	expectError(t, e.bob.Do(http.MethodGet, e.auditPath(), ""), 404, "not_found")
	expectError(t, e.alice.Do(http.MethodGet, e.auditPath()+"?limit=101", ""), 422, "validation_failed")
}

func TestAuditLogHasNoMutationRoutes(t *testing.T) {
	e := newAPIEnv(t, apiOpts{})
	e.alice.Do(http.MethodPost, e.eventPath(e.event), body(sinkDest(e.sink)))
	entry := decode[auditPage](t, e.alice.Do(http.MethodGet, e.auditPath(), "")).Items[0]
	for _, m := range []string{http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodPost} {
		for _, path := range []string{e.auditPath() + "/" + entry.ID, e.auditPath()} {
			rec := e.alice.Do(m, path, "{}")
			if rec.Code != http.StatusMethodNotAllowed && rec.Code != http.StatusNotFound {
				t.Errorf("%s %s: %d", m, path, rec.Code)
			}
		}
	}
	if after := decode[auditPage](t, e.alice.Do(http.MethodGet, e.auditPath(), "")).Items; len(after) != 1 || after[0].ID != entry.ID {
		t.Fatal("audit entry changed")
	}
}

// TestAuditMetadataIsSafe: only the host of a URL destination is recorded.
func TestAuditMetadataIsSafe(t *testing.T) {
	e := newAPIEnv(t, apiOpts{})
	in := urlDest("https://hooks.example.com:8443/a/b?key=secret&token=t0k")
	in["header_overrides"] = map[string]string{"Authorization": "Bearer s3cret"}
	if rec := e.alice.Do(http.MethodPost, e.eventPath(e.event), body(in)); rec.Code != 202 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	a := decode[auditPage](t, e.alice.Do(http.MethodGet, e.auditPath(), "")).Items[0]
	if a.Metadata["destination_host"] != "hooks.example.com" {
		t.Fatalf("host %q", a.Metadata["destination_host"])
	}
	raw, _ := json.Marshal(a.Metadata)
	for _, leak := range []string{"/a/b", "secret", "t0k", "8443", "s3cret", "Authorization", "relaylab", "203.0.113"} {
		if strings.Contains(string(raw), leak) {
			t.Errorf("audit metadata contains %q: %s", leak, raw)
		}
	}
	if e.Count(t, `SELECT count(*) FROM app.audit_log WHERE metadata::text LIKE '%secret%' OR metadata::text LIKE '%/a/b%'`) != 0 {
		t.Fatal("stored metadata leaks the URL")
	}
}
