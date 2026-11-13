package endpoints_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"relaylab/internal/endpoints"
	"relaylab/internal/endpoints/token"
	"relaylab/internal/platform/config"
	"relaylab/internal/platform/db/store"
	"relaylab/internal/testutil/fixture"
)

const ingest = "https://hooks.relaylab.test"

var urlPattern = regexp.MustCompile(`^https://hooks\.relaylab\.test/h/([0-9A-Za-z]{43})$`)

func testKeys(t *testing.T) *token.Keys {
	t.Helper()
	k, err := token.NewKeys(bytes.Repeat([]byte{0xA1}, 32), bytes.Repeat([]byte{0xB2}, 32), 1)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func setup(t *testing.T, limit int) (*fixture.Env, *endpoints.Service, uuid.UUID) {
	t.Helper()
	e := fixture.New(t, "alice", "bob")
	svc := endpoints.NewService(e.Pool, testKeys(t), ingest, limit)
	return e, svc, e.Project(t, "alice", "Alice's project")
}

func tokenOf(t *testing.T, ep endpoints.Endpoint) string {
	t.Helper()
	if ep.URL == nil {
		t.Fatal("endpoint has no URL")
	}
	m := urlPattern.FindStringSubmatch(*ep.URL)
	if m == nil {
		t.Fatalf("url %q does not match %s", *ep.URL, urlPattern)
	}
	return m[1]
}

func TestCreate(t *testing.T) {
	e, svc, p := setup(t, 25)
	ctx := context.Background()
	ep, err := svc.Create(ctx, "alice", p, "  Stripe staging ")
	if err != nil {
		t.Fatal(err)
	}
	if ep.Name != "Stripe staging" || !ep.Enabled || ep.ProjectID != p || ep.ID.Version() != 7 || ep.LastReceivedAt != nil {
		t.Fatalf("%+v", ep)
	}
	tok := tokenOf(t, ep)

	// The database holds only the hash and the ciphertext, never the token.
	var hash, sealed []byte
	if err := e.Admin.QueryRow(ctx, `SELECT token_hash, token_ciphertext FROM app.endpoints WHERE id = $1`, ep.ID).Scan(&hash, &sealed); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(hash, testKeys(t).Hash(tok)) {
		t.Fatal("stored hash is not HMAC(token)")
	}
	if n := e.Count(t, `SELECT count(*) FROM app.endpoints WHERE position($1::bytea IN token_ciphertext) > 0 OR position($1::bytea IN token_hash) > 0 OR name LIKE '%' || $2 || '%'`, []byte(tok), tok); n != 0 {
		t.Fatal("plaintext token found in the database")
	}
	// The ingestion lookup finds the endpoint by the presented token's hash.
	r, err := store.New(e.Pool).GetEndpointByTokenHash(ctx, testKeys(t).Hash(tok))
	if err != nil || r.ID.Bytes != ep.ID || !r.Enabled {
		t.Fatalf("lookup by hash: %+v %v", r, err)
	}
}

func TestDefaultIngestHostIsAppOrigin(t *testing.T) {
	e := fixture.New(t, "alice")
	origin, _ := config.Load(func(k string) string {
		return map[string]string{"DATABASE_URL": "postgres://u:p@h/d?sslmode=require", "APP_ENV": "production", "APP_ORIGIN": "https://relaylab-abc.a.run.app"}[k]
	})
	ep, err := config.LoadEndpoints(func(k string) string {
		return map[string]string{
			"ENDPOINT_TOKEN_HMAC_KEY": "oaGhoaGhoaGhoaGhoaGhoaGhoaGhoaGhoaGhoaGhoaE=",
			"ENDPOINT_TOKEN_ENC_KEY":  "srKysrKysrKysrKysrKysrKysrKysrKysrKysrKysrI=",
		}[k]
	}, origin.Env, origin.AppOrigin)
	if err != nil {
		t.Fatal(err)
	}
	keys, _ := token.NewKeys(ep.HMACKey(), ep.EncKey(), 1)
	svc := endpoints.NewService(e.Pool, keys, ep.IngestBaseURL.String(), 25)
	created, err := svc.Create(context.Background(), "alice", e.Project(t, "alice", "p"), "x")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(*created.URL, "https://relaylab-abc.a.run.app/h/") {
		t.Fatalf("url %s", *created.URL)
	}
}

func TestTokensAreUniqueAcrossEndpoints(t *testing.T) {
	_, svc, p := setup(t, 25)
	seen := map[string]bool{}
	for i := range 25 {
		ep, err := svc.Create(context.Background(), "alice", p, fmt.Sprintf("e%d", i))
		if err != nil {
			t.Fatal(err)
		}
		tok := tokenOf(t, ep)
		if seen[tok] || strings.Contains(tok, ep.ID.String()) || strings.Contains(tok, p.String()) {
			t.Fatalf("token %s duplicated or derived from ids", tok)
		}
		seen[tok] = true
	}
}

func TestNameValidation(t *testing.T) {
	e, svc, p := setup(t, 25)
	for _, name := range []string{"", "   ", strings.Repeat("n", 101), strings.Repeat("é", 101), "a\nb", "a\x00b"} {
		_, err := svc.Create(context.Background(), "alice", p, name)
		var ve *endpoints.ValidationError
		if !errors.As(err, &ve) || ve.Fields["name"] == "" {
			t.Errorf("%q: %v", name, err)
		}
	}
	if n := e.Count(t, `SELECT count(*) FROM app.endpoints`); n != 0 {
		t.Fatalf("%d endpoints created", n)
	}
	if _, err := svc.Create(context.Background(), "alice", p, strings.Repeat("é", 100)); err != nil {
		t.Fatalf("100 characters: %v", err)
	}
}

func TestLimitConfiguredToTwo(t *testing.T) {
	limits, err := config.LoadLimits(func(k string) string {
		return map[string]string{"LIMIT_ENDPOINTS_PER_PROJECT": "2"}[k]
	})
	if err != nil || limits.EndpointsPerProject != 2 || config.DefaultLimits().EndpointsPerProject != 25 {
		t.Fatalf("%+v %v", limits, err)
	}
	e, svc, p := setup(t, limits.EndpointsPerProject)
	ctx := context.Background()
	for _, n := range []string{"one", "two"} {
		if _, err := svc.Create(ctx, "alice", p, n); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.Create(ctx, "alice", p, "three"); !errors.Is(err, endpoints.ErrLimitReached) {
		t.Fatalf("third: %v", err)
	}
	// The limit is per project.
	other := e.Project(t, "alice", "Second project")
	if _, err := svc.Create(ctx, "alice", other, "elsewhere"); err != nil {
		t.Fatalf("other project: %v", err)
	}
}

func TestConcurrentCreatesRespectTheLimit(t *testing.T) {
	e, svc, p := setup(t, 3)
	var wg sync.WaitGroup
	errs := make([]error, 10)
	start := make(chan struct{})
	for i := range errs {
		wg.Go(func() {
			<-start
			_, errs[i] = svc.Create(context.Background(), "alice", p, fmt.Sprintf("r%d", i))
		})
	}
	close(start)
	wg.Wait()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
		} else if !errors.Is(err, endpoints.ErrLimitReached) {
			t.Errorf("unexpected: %v", err)
		}
	}
	if ok != 3 || e.Count(t, `SELECT count(*) FROM app.endpoints WHERE project_id = $1`, p) != 3 {
		t.Fatalf("ok=%d", ok)
	}
}

func TestListGetAndForeignProject(t *testing.T) {
	_, svc, p := setup(t, 25)
	ctx := context.Background()
	if list, err := svc.List(ctx, "alice", p); err != nil || list == nil || len(list) != 0 {
		t.Fatalf("empty list: %v %v", list, err)
	}
	a, _ := svc.Create(ctx, "alice", p, "first")
	b, _ := svc.Create(ctx, "alice", p, "second")
	list, err := svc.List(ctx, "alice", p)
	if err != nil || len(list) != 2 || list[0].ID != b.ID || list[1].ID != a.ID {
		t.Fatalf("newest first: %+v %v", list, err)
	}
	got, err := svc.Get(ctx, "alice", p, a.ID)
	if err != nil || got.Name != "first" || *got.URL != *a.URL {
		t.Fatalf("get: %+v %v", got, err)
	}
	if _, err := svc.List(ctx, "bob", p); !errors.Is(err, endpoints.ErrNotFound) {
		t.Fatalf("bob lists alice's project: %v", err)
	}
	if _, err := svc.Get(ctx, "alice", p, uuid.Must(uuid.NewV7())); !errors.Is(err, endpoints.ErrNotFound) {
		t.Fatalf("unknown endpoint: %v", err)
	}
}

func TestRenameAndEnableKeepTheURL(t *testing.T) {
	_, svc, p := setup(t, 25)
	ctx := context.Background()
	ep, _ := svc.Create(ctx, "alice", p, "old")
	renamed, err := svc.Update(ctx, "alice", p, ep.ID, ptr("GitHub prod"), nil)
	if err != nil || renamed.Name != "GitHub prod" || *renamed.URL != *ep.URL || !renamed.Enabled {
		t.Fatalf("rename: %+v %v", renamed, err)
	}
	disabled, err := svc.Update(ctx, "alice", p, ep.ID, nil, ptr(false))
	if err != nil || disabled.Enabled || *disabled.URL != *ep.URL {
		t.Fatalf("disable: %+v %v", disabled, err)
	}
	enabled, err := svc.Update(ctx, "alice", p, ep.ID, nil, ptr(true))
	if err != nil || !enabled.Enabled || *enabled.URL != *ep.URL {
		t.Fatalf("enable: %+v %v", enabled, err)
	}
	if _, err := svc.Update(ctx, "alice", p, ep.ID, nil, nil); err == nil {
		t.Fatal("empty update accepted")
	}
	if _, err := svc.Update(ctx, "alice", p, ep.ID, ptr(""), nil); err == nil {
		t.Fatal("empty name accepted")
	}
}

func TestRotateInvalidatesTheOldURL(t *testing.T) {
	e, svc, p := setup(t, 25)
	ctx := context.Background()
	ep, _ := svc.Create(ctx, "alice", p, "hooks")
	oldTok := tokenOf(t, ep)
	rotated, err := svc.Rotate(ctx, "alice", p, ep.ID)
	if err != nil {
		t.Fatal(err)
	}
	newTok := tokenOf(t, rotated)
	if newTok == oldTok || rotated.ID != ep.ID {
		t.Fatal("rotation did not change the token")
	}
	q := store.New(e.Pool)
	if _, err := q.GetEndpointByTokenHash(ctx, testKeys(t).Hash(oldTok)); err == nil {
		t.Fatal("old URL still resolves")
	}
	if r, err := q.GetEndpointByTokenHash(ctx, testKeys(t).Hash(newTok)); err != nil || r.ID.Bytes != ep.ID {
		t.Fatalf("new URL: %v", err)
	}
	if n := e.Count(t, `SELECT count(*) FROM app.endpoints WHERE id = $1 AND token_rotated_at IS NOT NULL`, ep.ID); n != 1 {
		t.Fatal("token_rotated_at not set")
	}
}

func TestDelete(t *testing.T) {
	e, svc, p := setup(t, 25)
	ctx := context.Background()
	ep, _ := svc.Create(ctx, "alice", p, "doomed")
	if err := svc.Delete(ctx, "alice", p, ep.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Get(ctx, "alice", p, ep.ID); !errors.Is(err, endpoints.ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}
	if err := svc.Delete(ctx, "alice", p, ep.ID); !errors.Is(err, endpoints.ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
	if n := e.Count(t, `SELECT count(*) FROM app.endpoints`); n != 0 {
		t.Fatal("row remains")
	}
}

func TestEveryOperationIsOwnerAndProjectScoped(t *testing.T) {
	e, svc, p := setup(t, 25)
	ctx := context.Background()
	ep, _ := svc.Create(ctx, "alice", p, "alice's")
	otherProject := e.Project(t, "alice", "Other")

	for name, err := range map[string]error{
		"create in foreign project": func() error { _, err := svc.Create(ctx, "bob", p, "x"); return err }(),
		"get as bob":                func() error { _, err := svc.Get(ctx, "bob", p, ep.ID); return err }(),
		"rename as bob":             func() error { _, err := svc.Update(ctx, "bob", p, ep.ID, ptr("pwned"), nil); return err }(),
		"rotate as bob":             func() error { _, err := svc.Rotate(ctx, "bob", p, ep.ID); return err }(),
		"delete as bob":             svc.Delete(ctx, "bob", p, ep.ID),
		"get via other project":     func() error { _, err := svc.Get(ctx, "alice", otherProject, ep.ID); return err }(),
		"rotate via other project":  func() error { _, err := svc.Rotate(ctx, "alice", otherProject, ep.ID); return err }(),
		"delete via other project":  svc.Delete(ctx, "alice", otherProject, ep.ID),
	} {
		if !errors.Is(err, endpoints.ErrNotFound) {
			t.Errorf("%s: %v", name, err)
		}
	}
	got, err := svc.Get(ctx, "alice", p, ep.ID)
	if err != nil || got.Name != "alice's" || *got.URL != *ep.URL {
		t.Fatalf("alice's endpoint changed: %+v %v", got, err)
	}
	if n := e.Count(t, `SELECT count(*) FROM app.endpoints WHERE project_id = $1`, p); n != 1 {
		t.Fatalf("bob created an endpoint in alice's project: %d", n)
	}
}

func TestAuditTrailHasNoTokenOrURL(t *testing.T) {
	e, svc, p := setup(t, 25)
	ctx := context.Background()
	ep, _ := svc.Create(ctx, "alice", p, "audited")
	_, _ = svc.Update(ctx, "alice", p, ep.ID, ptr("renamed"), nil)
	_, _ = svc.Update(ctx, "alice", p, ep.ID, nil, ptr(false))
	_, _ = svc.Update(ctx, "alice", p, ep.ID, nil, ptr(true))
	_, _ = svc.Update(ctx, "alice", p, ep.ID, ptr("renamed"), ptr(true)) // no change: no entries
	rotated, _ := svc.Rotate(ctx, "alice", p, ep.ID)
	_ = svc.Delete(ctx, "alice", p, ep.ID)

	rows, err := e.Admin.Query(ctx, `SELECT action, actor_user_id, resource_type, resource_id, metadata::text
		FROM app.audit_log WHERE project_id = $1 ORDER BY created_at, id`, p)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var actions []string
	for rows.Next() {
		var action, actor, rtype, meta string
		var rid uuid.UUID
		if err := rows.Scan(&action, &actor, &rtype, &rid, &meta); err != nil {
			t.Fatal(err)
		}
		actions = append(actions, action)
		if actor != "alice" || rtype != "endpoint" || rid != ep.ID {
			t.Errorf("%s: actor=%s type=%s id=%s", action, actor, rtype, rid)
		}
		for _, secret := range []string{tokenOf(t, ep), tokenOf(t, rotated), *ep.URL, *rotated.URL, "/h/"} {
			if strings.Contains(meta, secret) {
				t.Errorf("%s metadata contains a token or URL: %s", action, meta)
			}
		}
	}
	want := "endpoint.created,endpoint.renamed,endpoint.disabled,endpoint.enabled,endpoint.rotated,endpoint.deleted"
	if strings.Join(actions, ",") != want {
		t.Fatalf("actions = %v", actions)
	}
}

func TestLostEncryptionKeyLeavesURLUnavailableButLookupWorks(t *testing.T) {
	e, svc, p := setup(t, 25)
	ctx := context.Background()
	ep, _ := svc.Create(ctx, "alice", p, "hooks")
	tok := tokenOf(t, ep)
	otherEnc, _ := token.NewKeys(bytes.Repeat([]byte{0xA1}, 32), bytes.Repeat([]byte{0xC3}, 32), 2)
	svc2 := endpoints.NewService(e.Pool, otherEnc, ingest, 25)
	got, err := svc2.Get(ctx, "alice", p, ep.ID)
	if err != nil || got.URL != nil {
		t.Fatalf("expected no URL with the wrong key: %+v %v", got, err)
	}
	if _, err := store.New(e.Pool).GetEndpointByTokenHash(ctx, otherEnc.Hash(tok)); err != nil {
		t.Fatalf("lookup with the same HMAC key must still work: %v", err)
	}
	rotated, err := svc2.Rotate(ctx, "alice", p, ep.ID)
	if err != nil || rotated.URL == nil {
		t.Fatalf("rotation must issue a displayable URL: %+v %v", rotated, err)
	}
}

func ptr[T any](v T) *T { return &v }
