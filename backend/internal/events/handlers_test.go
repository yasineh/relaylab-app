package events

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"relaylab/internal/platform/redact"
	"relaylab/internal/testutil/isolation"
)

type api struct {
	*seedEnv
	h          chi.Router
	alice, bob isolation.Requester
	ep         uuid.UUID
}

func newAPI(t *testing.T) *api {
	t.Helper()
	e := newSeedEnv(t)
	svc := NewService(e.Pool, redact.New(nil, nil))
	h := e.Router(func(v1 chi.Router) {
		v1.Route("/projects/{projectId}/events", NewHandler(svc, 50, 100).Routes)
	})
	return &api{seedEnv: e, h: h, alice: e.As(t, h, "alice"), bob: e.As(t, h, "bob"), ep: e.endpoint(t, e.project, "Stripe")}
}

func (a *api) base(p uuid.UUID) string { return "/api/v1/projects/" + p.String() + "/events" }

// full inserts an event with the given stored headers, query, and body.
func (a *api) full(t *testing.T, headers [][2]string, headerCount int, rawQuery string, query map[string][]string,
	contentType *string, body []byte, encoding string, isJSON, jsonInvalid bool,
) uuid.UUID {
	t.Helper()
	id := idAt(t, time.Now().UTC())
	hj, _ := json.Marshal(headers)
	qj, _ := json.Marshal(query)
	var bodyJSON any
	if isJSON {
		bodyJSON = string(body)
	}
	if _, err := a.Admin.Exec(context.Background(), `
		INSERT INTO app.webhook_events (id, endpoint_id, project_id, received_at, method, path, raw_query, query_params,
		  headers, redacted_header_count, content_type, body, body_size, body_encoding, body_json, json_invalid,
		  source_ip_prefix, user_agent, correlation_id)
		VALUES ($1, $2, $3, now(), 'POST', '/hooks/stripe', $4, $5, $6, $7, $8, $9, $10, $11, $12::jsonb, $13,
		  '203.0.113.0', 'Stripe/1.0', 'req-123') -- the prefix as ingestion stores it: truncated, no mask
		  `,
		id, a.ep, a.project, rawQuery, qj, hj, headerCount, contentType, body, len(body), encoding, bodyJSON, jsonInvalid); err != nil {
		t.Fatal(err)
	}
	return id
}

type errBody struct {
	Error struct {
		Code   string            `json:"code"`
		Fields map[string]string `json:"fields"`
	} `json:"error"`
}

func decodeJSON[T any](t *testing.T, raw []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return v
}

type listBody struct {
	Items      []summaryJSON `json:"items"`
	NextCursor *string       `json:"next_cursor"`
}

func TestListEndpoint(t *testing.T) {
	a := newAPI(t)
	base := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	for i := range 3 {
		a.event(t, a.project, a.ep, base.Add(time.Duration(i)*time.Second), "POST", strp("application/json"))
	}

	rec := a.alice.Do(http.MethodGet, a.base(a.project)+"?limit=2", "")
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	page := decodeJSON[listBody](t, rec.Body.Bytes())
	if len(page.Items) != 2 || page.NextCursor == nil || page.Items[0].EndpointName != "Stripe" ||
		!page.Items[0].ReceivedAt.After(page.Items[1].ReceivedAt) {
		t.Fatalf("first page: %+v", page)
	}
	rec = a.alice.Do(http.MethodGet, a.base(a.project)+"?limit=2&cursor="+*page.NextCursor, "")
	if p := decodeJSON[listBody](t, rec.Body.Bytes()); rec.Code != 200 || len(p.Items) != 1 || p.NextCursor != nil {
		t.Fatalf("second page: %d %s", rec.Code, rec.Body)
	}

	// Exact empty shape for an owned project with nothing (or nothing matching).
	empty := a.Project(t, "alice", "Empty")
	for _, path := range []string{a.base(empty), a.base(a.project) + "?method=DELETE"} {
		rec = a.alice.Do(http.MethodGet, path, "")
		if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != `{"items":[],"next_cursor":null}` {
			t.Fatalf("%s: %d %s", path, rec.Code, rec.Body)
		}
	}
	// Changed filters with an old cursor.
	rec = a.alice.Do(http.MethodGet, a.base(a.project)+"?limit=2&sort=asc&cursor="+*page.NextCursor, "")
	if rec.Code != 400 || decodeJSON[errBody](t, rec.Body.Bytes()).Error.Code != "invalid_cursor" {
		t.Fatalf("reused cursor: %d %s", rec.Code, rec.Body)
	}
	rec = a.alice.Do(http.MethodGet, a.base(a.project)+"?cursor=garbage", "")
	if rec.Code != 400 || decodeJSON[errBody](t, rec.Body.Bytes()).Error.Code != "invalid_cursor" {
		t.Fatalf("garbage cursor: %d %s", rec.Code, rec.Body)
	}
}

func TestListValidation(t *testing.T) {
	a := newAPI(t)
	cases := map[string]string{
		"method=FOO":                 "method",
		"method=POST,FOO":            "method",
		"received_after=yesterday":   "received_after",
		"received_before=2026-11-01": "received_before",
		"received_after=2026-11-02T00:00:00Z&received_before=2026-11-01T00:00:00Z": "received_before",
		"endpoint_id=nope": "endpoint_id",
		"endpoint_id=" + uuid.NewString() + "&endpoint_id=" + uuid.NewString(): "endpoint_id",
		"sort=sideways": "sort",
		"limit=0":       "limit",
		"limit=1000":    "limit",
		"limit=-5":      "limit",
		"limit=10abc":   "limit",
		"limit=+5":      "limit",
		"content_type=" + strings.Repeat("a", 101): "content_type",
		"content_type=text%0Aplain":                "content_type",
	}
	for q, field := range cases {
		rec := a.alice.Do(http.MethodGet, a.base(a.project)+"?"+q, "")
		body := decodeJSON[errBody](t, rec.Body.Bytes())
		if rec.Code != http.StatusUnprocessableEntity || body.Error.Code != "validation_failed" || body.Error.Fields[field] == "" {
			t.Errorf("%s: %d %s, want 422 naming %s", q, rec.Code, rec.Body, field)
		}
	}
	for _, q := range []string{"method=post,get", "method=POST&method=PUT", "limit=100", "sort=asc", "unknown=1",
		"content_type=" + strings.Repeat("a", 100), "received_after=2026-11-01T12:00:00%2B01:00"} {
		if rec := a.alice.Do(http.MethodGet, a.base(a.project)+"?"+q, ""); rec.Code != 200 {
			t.Errorf("%s: %d %s, want 200", q, rec.Code, rec.Body)
		}
	}
}

func TestDetail(t *testing.T) {
	a := newAPI(t)
	id := a.full(t,
		[][2]string{{"Authorization", "[REDACTED]"}, {"Content-Type", "application/json"}, {"X-Note", "[REDACTED]"}}, 1,
		"token=%5BREDACTED%5D&page=2", map[string][]string{"token": {"[REDACTED]"}, "page": {"2"}},
		strp("application/json"), []byte(`{"b":1,"a":[true,null]}`), "utf8", true, false)

	plain := a.alice.Do(http.MethodGet, a.base(a.project)+"/"+id.String(), "")
	if plain.Code != 200 || plain.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("detail: %d %s", plain.Code, plain.Body)
	}
	d := decodeJSON[detailJSON](t, plain.Body.Bytes())
	wantHeaders := []entryJSON{{"Authorization", "[REDACTED]", true}, {"Content-Type", "application/json", false}, {"X-Note", "[REDACTED]", false}}
	if len(d.Headers) != 3 || d.Headers[0] != wantHeaders[0] || d.Headers[1] != wantHeaders[1] || d.Headers[2] != wantHeaders[2] {
		t.Fatalf("headers: %+v", d.Headers)
	}
	if d.RedactedCount != 2 || d.RawQuery != "token=%5BREDACTED%5D&page=2" ||
		len(d.Query) != 2 || d.Query[0] != (entryJSON{"page", "2", false}) || d.Query[1] != (entryJSON{"token", "[REDACTED]", true}) {
		t.Fatalf("query/count: %+v", d)
	}
	if d.Body.Encoding != "utf8" || d.Body.Text == nil || *d.Body.Text != `{"b":1,"a":[true,null]}` || d.Body.Base64 != nil ||
		!d.Body.IsJSON || d.Body.JSONInvalid || d.Body.Size != 23 {
		t.Fatalf("body: %+v", d.Body)
	}
	if d.Method != http.MethodPost || d.Path != "/hooks/stripe" || d.EndpointName != "Stripe" || d.CorrelationID != "req-123" ||
		d.SourceIPPrefix == nil || *d.SourceIPPrefix != "203.0.113.0/24" || d.UserAgent == nil || *d.UserAgent != "Stripe/1.0" {
		t.Fatalf("summary fields: %+v", d)
	}
	// No raw `"redacted": true` text in the JSON beyond the two flagged entries.
	if n := strings.Count(plain.Body.String(), `"redacted":true`); n != 2 {
		t.Fatalf("%d redacted flags", n)
	}

	// ?reveal= and other parameters are ignored: identical response.
	for _, q := range []string{"?reveal=headers", "?reveal=true&x=1"} {
		rec := a.alice.Do(http.MethodGet, a.base(a.project)+"/"+id.String()+q, "")
		if rec.Code != 200 || rec.Body.String() != plain.Body.String() {
			t.Fatalf("%s changed the response: %s", q, rec.Body)
		}
	}

	for name, path := range map[string]string{
		"unknown event":     a.base(a.project) + "/" + uuid.NewString(),
		"malformed id":      a.base(a.project) + "/not-a-uuid",
		"wrong project":     a.base(a.Project(t, "alice", "Other")) + "/" + id.String(),
		"malformed project": "/api/v1/projects/nope/events/" + id.String(),
	} {
		rec := a.alice.Do(http.MethodGet, path, "")
		if rec.Code != 404 || decodeJSON[errBody](t, rec.Body.Bytes()).Error.Code != "not_found" {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
}

func TestBinaryBodyAndDownload(t *testing.T) {
	a := newAPI(t)
	raw := []byte{0xff, 0xfe, 0x00}
	id := a.full(t, [][2]string{}, 0, "", map[string][]string{}, strp("application/octet-stream"), raw, "binary", false, false)

	d := decodeJSON[detailJSON](t, a.alice.Do(http.MethodGet, a.base(a.project)+"/"+id.String(), "").Body.Bytes())
	if d.Body.Encoding != "binary" || d.Body.Text != nil || d.Body.Base64 == nil || d.Body.Size != 3 {
		t.Fatalf("binary body: %+v", d.Body)
	}
	if got, _ := base64.StdEncoding.DecodeString(*d.Body.Base64); string(got) != string(raw) {
		t.Fatalf("base64 decodes to %x", got)
	}
	if d.Headers == nil || d.Query == nil {
		t.Fatal("headers and query must be [] not null")
	}

	rec := a.alice.Do(http.MethodGet, a.base(a.project)+"/"+id.String()+"/body", "")
	hd := rec.Header()
	if rec.Code != 200 || rec.Body.String() != string(raw) ||
		hd.Get("Content-Type") != "application/octet-stream" ||
		hd.Get("Content-Disposition") != `attachment; filename="event-`+id.String()+`.bin"` ||
		hd.Get("X-Content-Type-Options") != "nosniff" || hd.Get("Content-Security-Policy") != "sandbox" ||
		hd.Get("Cache-Control") != "no-store" || hd.Get("Content-Length") != "3" {
		t.Fatalf("download: %d %q %v", rec.Code, rec.Body.Bytes(), hd)
	}
	// An HTML body is still served as an inert download.
	html := a.full(t, [][2]string{}, 0, "", map[string][]string{}, strp("text/html"), []byte(`<script>alert(1)</script>`), "utf8", false, false)
	rec = a.alice.Do(http.MethodGet, a.base(a.project)+"/"+html.String()+"/body", "")
	if rec.Header().Get("Content-Type") != "application/octet-stream" || !strings.HasPrefix(rec.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("html download headers: %v", rec.Header())
	}
}

func TestSessionRequired(t *testing.T) {
	a := newAPI(t)
	id := a.event(t, a.project, a.ep, time.Now().UTC(), "POST", nil)
	ctx := context.Background()
	expired := a.As(t, a.h, "alice")
	revoked := a.As(t, a.h, "alice")
	tokenOf := func(c string) string { return strings.SplitN(strings.SplitN(c, "=", 2)[1], ".", 2)[0] }
	if _, err := a.Admin.Exec(ctx, `UPDATE auth."session" SET "expiresAt" = now() - interval '1 second' WHERE token = $1`, tokenOf(expired.Cookie)); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Admin.Exec(ctx, `DELETE FROM auth."session" WHERE token = $1`, tokenOf(revoked.Cookie)); err != nil {
		t.Fatal(err)
	}
	missing := isolation.Requester{Handler: a.h, Origin: a.alice.Origin}
	for name, tc := range map[string]struct {
		u    isolation.Requester
		code string
	}{"missing": {missing, "unauthenticated"}, "expired": {expired, "session_expired"}, "revoked": {revoked, "unauthenticated"}} {
		for _, path := range []string{a.base(a.project), a.base(a.project) + "/" + id.String(), a.base(a.project) + "/" + id.String() + "/body"} {
			rec := tc.u.Do(http.MethodGet, path, "")
			if rec.Code != 401 || decodeJSON[errBody](t, rec.Body.Bytes()).Error.Code != tc.code {
				t.Errorf("%s %s: %d %s", name, path, rec.Code, rec.Body)
			}
		}
	}
}

func TestSourcePrefixShowsTruncation(t *testing.T) {
	a := newAPI(t)
	for stored, want := range map[string]string{"203.0.113.0": "203.0.113.0/24", "2001:db8:abcd::": "2001:db8:abcd::/48"} {
		id := a.event(t, a.project, a.ep, time.Now().UTC(), "POST", nil)
		if _, err := a.Admin.Exec(context.Background(), `UPDATE app.webhook_events SET source_ip_prefix = $1 WHERE id = $2`, stored, id); err != nil {
			t.Fatal(err)
		}
		d := decodeJSON[detailJSON](t, a.alice.Do(http.MethodGet, a.base(a.project)+"/"+id.String(), "").Body.Bytes())
		if d.SourceIPPrefix == nil || *d.SourceIPPrefix != want {
			t.Errorf("%s: got %v, want %s", stored, d.SourceIPPrefix, want)
		}
	}
}
