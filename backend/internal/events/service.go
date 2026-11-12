package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"relaylab/internal/platform/db/store"
	"relaylab/internal/platform/redact"
)

// ErrNotFound covers missing events, events in other users' projects or in
// other projects, and malformed ids alike.
var ErrNotFound = errors.New("event not found")

// Page is one page of the event list.
type Page struct {
	Items      []Summary
	NextCursor *string
}

// Body is the stored body exactly as received.
type Body struct {
	Bytes       []byte
	Encoding    string // "utf8" or "binary"
	IsJSON      bool   // parsed as JSON at receipt (JSON content type, valid)
	JSONInvalid bool   // JSON content type but not valid JSON
}

// Detail is one event with everything stored about it.
type Detail struct {
	Summary
	RawQuery       string
	Query          []QueryParam
	Headers        []Header
	RedactedCount  int
	Body           Body
	SourceIPPrefix *string
	UserAgent      *string
	CorrelationID  string
}

// Service reads events for the acting user.
type Service struct {
	q   *store.Queries
	red *redact.Redactor
}

// NewService returns the event read service. red must be the same redactor
// configuration ingestion uses, so markers match what was removed.
func NewService(db store.DBTX, red *redact.Redactor) *Service {
	return &Service{q: newQuerier(db), red: red}
}

// ParseID parses an id from a URL. Anything that is not a UUID is reported as
// ErrNotFound, so malformed and foreign ids look identical.
func ParseID(raw string) (uuid.UUID, error) {
	id, err := uuid.Parse(raw)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, ErrNotFound
	}
	return id, nil
}

// List returns one page of the project's events. limit must already be
// validated against the page limits. A project the owner cannot see is
// ErrNotFound; an owned project with no matching events is an empty page.
func (s *Service) List(ctx context.Context, owner string, project uuid.UUID, f Filter, cursor string, limit int) (Page, error) {
	items, next, err := listPage(ctx, s.q, owner, project, f, cursor, limit)
	if err != nil {
		return Page{}, err
	}
	if len(items) == 0 {
		// Only an empty page needs the ownership check: any returned row
		// already proves it (every query joins the owner's projects).
		if _, err := s.q.GetProject(ctx, store.GetProjectParams{ID: pgUUID(project), OwnerID: owner}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return Page{}, ErrNotFound
			}
			return Page{}, fmt.Errorf("check project: %w", err)
		}
	}
	return Page{Items: items, NextCursor: next}, nil
}

// Get returns one event with redaction markers applied.
func (s *Service) Get(ctx context.Context, owner string, project, id uuid.UUID) (Detail, error) {
	r, err := s.q.GetEvent(ctx, store.GetEventParams{ID: pgUUID(id), ProjectID: pgUUID(project), OwnerID: owner})
	if errors.Is(err, pgx.ErrNoRows) {
		return Detail{}, ErrNotFound
	}
	if err != nil {
		return Detail{}, fmt.Errorf("get event: %w", err)
	}
	var headers [][2]string
	if err := json.Unmarshal(r.Headers, &headers); err != nil {
		return Detail{}, fmt.Errorf("decode headers: %w", err)
	}
	var query map[string][]string
	if err := json.Unmarshal(r.QueryParams, &query); err != nil {
		return Detail{}, fmt.Errorf("decode query: %w", err)
	}
	m := markEvent(s.red, headers, int(r.RedactedHeaderCount), query)
	d := Detail{
		Summary: Summary{
			ID: r.ID.Bytes, EndpointID: r.EndpointID.Bytes, EndpointName: r.EndpointName,
			ReceivedAt: r.ReceivedAt.Time.UTC(), Method: r.Method, Path: r.Path, BodySize: int(r.BodySize),
		},
		RawQuery: r.RawQuery, Query: m.Query, Headers: m.Headers, RedactedCount: m.RedactedCount,
		Body:          Body{Bytes: r.Body, Encoding: r.BodyEncoding, IsJSON: r.IsJson, JSONInvalid: r.JsonInvalid},
		CorrelationID: r.CorrelationID,
	}
	if r.ContentType.Valid {
		d.ContentType = &r.ContentType.String
	}
	if r.SourceIpPrefix != "" {
		d.SourceIPPrefix = &r.SourceIpPrefix
	}
	if r.UserAgent.Valid {
		d.UserAgent = &r.UserAgent.String
	}
	return d, nil
}

// RawBody returns the exact stored body bytes.
func (s *Service) RawBody(ctx context.Context, owner string, project, id uuid.UUID) ([]byte, error) {
	b, err := s.q.GetEventBody(ctx, store.GetEventBodyParams{ID: pgUUID(id), ProjectID: pgUUID(project), OwnerID: owner})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get event body: %w", err)
	}
	return b, nil
}

// Methods accepted by the method filter (the same set ingestion stores).
var filterMethods = map[string]bool{
	"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true, "HEAD": true, "OPTIONS": true,
}

// contentTypeFilterMaxChars bounds the content type filter. It is a field
// length, not a page limit, and is allowlisted in the limit-literal guard.
const contentTypeFilterMaxChars = 100

// ListParams are the raw list query parameters.
type ListParams struct {
	EndpointID, Methods, ReceivedAfter, ReceivedBefore, ContentType, Sort, Limit, Cursor []string
}

// ValidationError maps query parameters to user-facing problems.
type ValidationError struct{ Fields map[string]string }

func (e *ValidationError) Error() string { return fmt.Sprintf("invalid event filters: %v", e.Fields) }

// ParseList validates list parameters into a filter and page size. Each
// parameter may appear once, except method, which may repeat and also takes a
// comma-separated list. Unknown parameters are ignored by the caller.
func ParseList(p ListParams, pageDefault, pageMax int) (Filter, string, int, error) {
	var f Filter
	bad := map[string]string{}
	single := func(name string, vals []string) (string, bool) {
		switch len(vals) {
		case 0:
			return "", false
		case 1:
			return vals[0], true
		default:
			bad[name] = "Give this parameter once."
			return "", false
		}
	}
	if v, ok := single("endpoint_id", p.EndpointID); ok {
		id, err := uuid.Parse(v)
		if err != nil || id == uuid.Nil {
			bad["endpoint_id"] = "Must be an endpoint id."
		} else {
			f.EndpointID = &id
		}
	}
	for _, v := range p.Methods {
		for _, m := range strings.Split(v, ",") {
			um := strings.ToUpper(m)
			if !filterMethods[um] {
				bad["method"] = "Must be one or more of GET, POST, PUT, PATCH, DELETE, HEAD, OPTIONS."
				break
			}
			if !slices.Contains(f.Methods, um) {
				f.Methods = append(f.Methods, um)
			}
		}
	}
	parseTime := func(name string, vals []string) *time.Time {
		v, ok := single(name, vals)
		if !ok {
			return nil
		}
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			bad[name] = "Must be an RFC 3339 timestamp, such as 2026-11-01T12:00:00Z."
			return nil
		}
		t = t.UTC()
		return &t
	}
	f.ReceivedAfter = parseTime("received_after", p.ReceivedAfter)
	f.ReceivedBefore = parseTime("received_before", p.ReceivedBefore)
	if f.ReceivedAfter != nil && f.ReceivedBefore != nil && !f.ReceivedAfter.Before(*f.ReceivedBefore) {
		bad["received_before"] = "Must be later than received_after."
	}
	if v, ok := single("content_type", p.ContentType); ok {
		switch {
		case len([]rune(v)) > contentTypeFilterMaxChars:
			bad["content_type"] = fmt.Sprintf("Must be at most %d characters.", contentTypeFilterMaxChars)
		case hasControl(v):
			bad["content_type"] = "Must not contain control characters."
		default:
			f.ContentTypePrefix = v
		}
	}
	if v, ok := single("sort", p.Sort); ok {
		switch v {
		case "desc":
		case "asc":
			f.Asc = true
		default:
			bad["sort"] = "Must be asc or desc."
		}
	}
	limit := pageDefault
	if v, ok := single("limit", p.Limit); ok {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > pageMax || strings.TrimSpace(v) != v || strings.HasPrefix(v, "+") {
			bad["limit"] = fmt.Sprintf("Must be a whole number from 1 to %d.", pageMax)
		} else {
			limit = n
		}
	}
	cursor, _ := single("cursor", p.Cursor)
	if len(bad) > 0 {
		return Filter{}, "", 0, &ValidationError{Fields: bad}
	}
	return f, cursor, limit, nil
}

func hasControl(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}
