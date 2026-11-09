package events

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ErrInvalidCursor means a cursor did not decode, or was issued for a
// different filter or sort than the request it came with (400 invalid_cursor).
var ErrInvalidCursor = errors.New("invalid cursor")

// maxCursorLen bounds the encoded cursor before any decoding work. A real
// cursor is about 110 characters.
const maxCursorLen = 256

// cursor is the position after the last returned event. It is opaque to
// clients but not signed (owner decision): every query is owner- and
// project-scoped, so an edited or foreign cursor can only move within events
// the caller may already read. The filter hash stops a cursor from one query
// being silently reused with different filters or sort.
type cursor struct {
	ID     uuid.UUID `json:"id"`
	Dir    string    `json:"dir"`
	Filter string    `json:"f"`
}

func direction(f Filter) string {
	if f.Asc {
		return "asc"
	}
	return "desc"
}

// filterHash identifies a filter (sort excluded; it is checked via Dir).
// Methods are order-insensitive and times compare by instant.
func filterHash(f Filter) string {
	var b strings.Builder
	if f.EndpointID != nil {
		b.WriteString(f.EndpointID.String())
	}
	b.WriteByte(0)
	methods := slices.Clone(f.Methods)
	slices.Sort(methods)
	b.WriteString(strings.Join(slices.Compact(methods), ","))
	b.WriteByte(0)
	for _, t := range []*time.Time{f.ReceivedAfter, f.ReceivedBefore} {
		if t != nil {
			b.WriteString(t.UTC().Format(time.RFC3339Nano))
		}
		b.WriteByte(0)
	}
	b.WriteString(f.ContentTypePrefix)
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:8])
}

// encodeCursor returns the cursor that continues f after event id.
func encodeCursor(f Filter, id uuid.UUID) string {
	raw, _ := json.Marshal(cursor{ID: id, Dir: direction(f), Filter: filterHash(f)}) //nolint:errchkjson // fixed struct
	return base64.RawURLEncoding.EncodeToString(raw)
}

// decodeCursor parses s and checks that it was issued for f. An empty string
// means "first page" and returns nil.
func decodeCursor(s string, f Filter) (*uuid.UUID, error) {
	if s == "" {
		return nil, nil
	}
	if len(s) > maxCursorLen {
		return nil, ErrInvalidCursor
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, ErrInvalidCursor
	}
	var c cursor
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil || dec.More() {
		return nil, ErrInvalidCursor
	}
	if c.ID == uuid.Nil || c.ID.Version() != 7 || c.Dir != direction(f) || c.Filter != filterHash(f) {
		return nil, ErrInvalidCursor
	}
	return &c.ID, nil
}
