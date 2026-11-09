package ingestion

import (
	"bytes"
	"encoding/json"
	"mime"
	"strings"
	"unicode/utf8"
)

// maxJSONDepth bounds nesting for the parsed copy; deeper bodies are stored raw only.
const maxJSONDepth = 64

// classification describes how a body is stored alongside its raw bytes.
type classification struct {
	Encoding    string // "utf8" or "binary"
	JSON        []byte // normalized JSON for the body_json column, nil when not stored
	JSONInvalid bool   // JSON content type, but the body did not parse
}

// classify inspects a body without modifying it. Only a bounded JSON check runs,
// and only for JSON content types; anything else is classified by UTF-8 validity.
func classify(contentType string, body []byte) classification {
	c := classification{Encoding: "binary"}
	isUTF8 := utf8.Valid(body)
	if isUTF8 {
		c.Encoding = "utf8"
	}
	if !isJSONType(contentType) {
		return c
	}
	if !isUTF8 || !json.Valid(body) {
		c.JSONInvalid = true
		return c
	}
	if depthExceeds(body, maxJSONDepth) {
		return c // valid, but too deep to parse into jsonb
	}
	c.JSON = normalizeJSON(body)
	return c
}

// isJSONType reports application/json or any +json media type. Malformed
// content types fall back to the part before ';'.
func isJSONType(contentType string) bool {
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		mt, _, _ = strings.Cut(contentType, ";")
	}
	mt = strings.ToLower(strings.TrimSpace(mt))
	return mt == "application/json" || strings.HasSuffix(mt, "+json")
}

// depthExceeds reports whether arrays/objects nest deeper than limit. It
// tracks string state so brackets inside strings do not count.
func depthExceeds(body []byte, limit int) bool {
	depth, inString, escaped := 0, false, false
	for _, b := range body {
		switch {
		case inString:
			switch {
			case escaped:
				escaped = false
			case b == '\\':
				escaped = true
			case b == '"':
				inString = false
			}
		case b == '"':
			inString = true
		case b == '{' || b == '[':
			depth++
			if depth > limit {
				return true
			}
		case b == '}' || b == ']':
			depth--
		}
	}
	return false
}

// normalizeJSON re-encodes valid JSON so PostgreSQL jsonb accepts it: Go
// replaces lone surrogate escapes with U+FFFD and keeps numbers exact. Bodies
// containing NUL (\u0000), which jsonb cannot store, get no parsed copy.
func normalizeJSON(body []byte) []byte {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil
	}
	out := bytes.TrimRight(buf.Bytes(), "\n")
	if bytes.Contains(out, []byte(`\u0000`)) {
		return nil
	}
	return out
}
