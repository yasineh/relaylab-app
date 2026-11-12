package events

import (
	"sort"

	"relaylab/internal/platform/redact"
)

// Header is one received header. Redacted means its value was removed at
// receipt; the original is not stored anywhere.
type Header struct {
	Name     string
	Value    string
	Redacted bool
}

// QueryParam is one received query parameter value.
type QueryParam struct {
	Name     string
	Value    string
	Redacted bool
}

// markHeaders flags stored headers whose value was removed at receipt. Both
// conditions are required: the stored value is the marker and the shared
// matcher classifies the name as sensitive. A sender that literally sent
// "[REDACTED]" in an ordinary header is not mislabelled.
func markHeaders(red *redact.Redactor, stored [][2]string) []Header {
	out := make([]Header, len(stored))
	for i, h := range stored {
		out[i] = Header{Name: h[0], Value: h[1], Redacted: h[1] == redact.Marker && red.SensitiveHeader(h[0])}
	}
	return out
}

// markQuery flattens stored query parameters ({name: [values]}) into name
// order, keeping each name's values in arrival order, and flags removed
// values the same way as headers. It returns how many were flagged.
func markQuery(red *redact.Redactor, stored map[string][]string) ([]QueryParam, int) {
	names := make([]string, 0, len(stored))
	for n := range stored {
		names = append(names, n)
	}
	sort.Strings(names)
	var out []QueryParam
	marked := 0
	for _, n := range names {
		sensitive := red.SensitiveParam(n)
		for _, v := range stored[n] {
			p := QueryParam{Name: n, Value: v, Redacted: sensitive && v == redact.Marker}
			if p.Redacted {
				marked++
			}
			out = append(out, p)
		}
	}
	if out == nil {
		out = []QueryParam{}
	}
	return out, marked
}

// marked is the header and query view of one event plus redacted_count: the
// header count recorded at receipt plus the query values flagged now.
// Ingestion does not store a query count, so a later change to the extra
// query rules affects only the query part (design: Redaction markers).
type marked struct {
	Headers       []Header
	Query         []QueryParam
	RedactedCount int
}

func markEvent(red *redact.Redactor, headers [][2]string, headerCount int, query map[string][]string) marked {
	q, n := markQuery(red, query)
	return marked{Headers: markHeaders(red, headers), Query: q, RedactedCount: headerCount + n}
}
