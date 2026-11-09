package events

import (
	"context"
	"encoding/base64"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestCursorRoundTrip(t *testing.T) {
	ep := uuid.Must(uuid.NewV7())
	at := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	id := uuid.Must(uuid.NewV7())
	for _, f := range []Filter{
		{},
		{Asc: true},
		{EndpointID: &ep, Methods: []string{"POST", "PUT"}, ReceivedAfter: &at, ContentTypePrefix: "application/json"},
	} {
		got, err := decodeCursor(encodeCursor(f, id), f)
		if err != nil || *got != id {
			t.Fatalf("%+v: got %v, %v", f, got, err)
		}
	}
	if got, err := decodeCursor("", Filter{}); got != nil || err != nil {
		t.Fatalf("empty cursor: %v, %v", got, err)
	}
}

func TestCursorIgnoresMethodOrderAndTimeZone(t *testing.T) {
	id := uuid.Must(uuid.NewV7())
	utc := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	cet := utc.In(time.FixedZone("CET", 3600))
	c := encodeCursor(Filter{Methods: []string{"PUT", "POST"}, ReceivedAfter: &utc}, id)
	if _, err := decodeCursor(c, Filter{Methods: []string{"POST", "PUT"}, ReceivedAfter: &cet}); err != nil {
		t.Fatalf("equivalent filter rejected: %v", err)
	}
}

func TestCursorRejected(t *testing.T) {
	ep := uuid.Must(uuid.NewV7())
	other := uuid.Must(uuid.NewV7())
	at := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	later := at.Add(time.Second)
	id := uuid.Must(uuid.NewV7())
	base := Filter{EndpointID: &ep, Methods: []string{"POST"}, ReceivedAfter: &at, ContentTypePrefix: "application/"}
	valid := encodeCursor(base, id)
	enc := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	hash := filterHash(base)

	cases := map[string]struct {
		cursor string
		f      Filter
	}{
		"not base64":           {"!!!", base},
		"padded base64":        {base64.URLEncoding.EncodeToString([]byte(`{}`)), base},
		"not json":             {enc("hello"), base},
		"trailing data":        {enc(`{"id":"` + id.String() + `","dir":"desc","f":"` + hash + `"}{}`), base},
		"unknown field":        {enc(`{"id":"` + id.String() + `","dir":"desc","f":"` + hash + `","x":1}`), base},
		"nil id":               {enc(`{"id":"` + uuid.Nil.String() + `","dir":"desc","f":"` + hash + `"}`), base},
		"not a v7 id":          {enc(`{"id":"` + uuid.New().String() + `","dir":"desc","f":"` + hash + `"}`), base},
		"edited filter hash":   {enc(`{"id":"` + id.String() + `","dir":"desc","f":"0000000000000000"}`), base},
		"too long":             {strings.Repeat("A", maxCursorLen+1), base},
		"sort changed":         {valid, Filter{EndpointID: &ep, Methods: []string{"POST"}, ReceivedAfter: &at, ContentTypePrefix: "application/", Asc: true}},
		"endpoint changed":     {valid, Filter{EndpointID: &other, Methods: []string{"POST"}, ReceivedAfter: &at, ContentTypePrefix: "application/"}},
		"endpoint dropped":     {valid, Filter{Methods: []string{"POST"}, ReceivedAfter: &at, ContentTypePrefix: "application/"}},
		"method changed":       {valid, Filter{EndpointID: &ep, Methods: []string{"PUT"}, ReceivedAfter: &at, ContentTypePrefix: "application/"}},
		"time changed":         {valid, Filter{EndpointID: &ep, Methods: []string{"POST"}, ReceivedAfter: &later, ContentTypePrefix: "application/"}},
		"after became before":  {valid, Filter{EndpointID: &ep, Methods: []string{"POST"}, ReceivedBefore: &at, ContentTypePrefix: "application/"}},
		"content type changed": {valid, Filter{EndpointID: &ep, Methods: []string{"POST"}, ReceivedAfter: &at, ContentTypePrefix: "text/"}},
	}
	for name, tc := range cases {
		if _, err := decodeCursor(tc.cursor, tc.f); !errors.Is(err, ErrInvalidCursor) {
			t.Errorf("%s: got %v, want ErrInvalidCursor", name, err)
		}
	}
}

func TestPagination(t *testing.T) {
	e := newSeedEnv(t)
	ep := e.endpoint(t, e.project, "Stripe")
	base := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	for i := range 120 {
		e.event(t, e.project, ep, base.Add(time.Duration(i)*time.Second), "POST", nil)
	}
	ctx := context.Background()

	for _, f := range []Filter{{}, {Asc: true}} {
		seen := map[uuid.UUID]bool{}
		var sizes []int
		cur := ""
		for {
			items, next, err := listPage(ctx, e.q, "alice", e.project, f, cur, 50)
			if err != nil {
				t.Fatal(err)
			}
			sizes = append(sizes, len(items))
			for _, it := range items {
				if seen[it.ID] {
					t.Fatalf("duplicate %v", it.ID)
				}
				seen[it.ID] = true
			}
			if next == nil {
				break
			}
			cur = *next
			// Events arriving mid-traversal must not shift or duplicate pages.
			e.event(t, e.project, ep, time.Now().UTC(), "POST", nil)
		}
		// Newest first: arrivals are ahead of the traversal and never appear.
		// Oldest first runs second, over 122 events (120 + the desc pass's two
		// arrivals); its own two arrivals land behind it and join the last page.
		want := []int{50, 50, 20}
		if f.Asc {
			want = []int{50, 50, 24}
		}
		if !slices.Equal(sizes, want) {
			t.Fatalf("%s pages = %v, want %v", direction(f), sizes, want)
		}
	}
}

func TestPaginationExactMultipleHasNoEmptyPage(t *testing.T) {
	e := newSeedEnv(t)
	ep := e.endpoint(t, e.project, "Stripe")
	for i := range 4 {
		e.event(t, e.project, ep, time.Now().UTC().Add(time.Duration(i)*time.Millisecond), "POST", nil)
	}
	items, next, err := listPage(context.Background(), e.q, "alice", e.project, Filter{}, "", 4)
	if err != nil || len(items) != 4 || next != nil {
		t.Fatalf("got %d items, next %v, err %v", len(items), next, err)
	}
}

func TestForeignProjectCursorStaysInRequestedProject(t *testing.T) {
	e := newSeedEnv(t)
	ctx := context.Background()
	base := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	epA := e.endpoint(t, e.project, "A")
	other := e.Project(t, "alice", "Beta")
	epB := e.endpoint(t, other, "B")
	for i := range 3 {
		e.event(t, e.project, epA, base.Add(time.Duration(i)*time.Second), "POST", nil)
		e.event(t, other, epB, base.Add(time.Duration(i)*time.Second+time.Millisecond), "POST", nil)
	}
	_, next, err := listPage(ctx, e.q, "alice", e.project, Filter{}, "", 1)
	if err != nil || next == nil {
		t.Fatalf("first page: %v, %v", next, err)
	}
	items, _, err := listPage(ctx, e.q, "alice", other, Filter{}, *next, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if it.EndpointID != epB {
			t.Fatalf("event %v from another project returned", it.ID)
		}
	}
	// Bob cannot use alice's cursor to read alice's project.
	items, _, err = listPage(ctx, e.q, "bob", e.project, Filter{}, *next, 10)
	if err != nil || len(items) != 0 {
		t.Fatalf("bob with alice's cursor: %d items, %v", len(items), err)
	}
}
