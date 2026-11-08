// Package isolation checks cross-user isolation at the HTTP layer: a user must
// get exactly the same 404 for another user's resource as for one that does
// not exist, on every route. Every change that adds owner-scoped routes runs
// its routes through AssertForeignNotFound.
package isolation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// Route is a request template; "{id}" in Path is replaced by a resource id.
type Route struct {
	Method string
	Path   string
	Body   string // JSON body for unsafe methods; "" for none
}

// Requester sends a request as one user (session cookie) through the full
// middleware stack.
type Requester struct {
	Handler http.Handler
	Origin  string // APP_ORIGIN, for the same-origin check
	Cookie  string // "relaylab.session_token=..."
}

// Do sends method path with body as this user.
func (u Requester) Do(method, path, body string) *httptest.ResponseRecorder {
	var req *http.Request
	if body != "" {
		req = httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequestWithContext(context.Background(), method, path, nil)
	}
	req.Header.Set("Origin", u.Origin)
	if u.Cookie != "" {
		req.Header.Set("Cookie", u.Cookie)
	}
	rec := httptest.NewRecorder()
	u.Handler.ServeHTTP(rec, req)
	return rec
}

type envelope struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// AssertForeignNotFound sends every route as intruder against foreignID (a
// resource owned by someone else) and against a random id that does not
// exist. Both must be 404 not_found with identical code and message (only the
// request id may differ), so ownership cannot be probed.
func AssertForeignNotFound(t *testing.T, intruder Requester, foreignID string, routes []Route) {
	t.Helper()
	missingID := uuid.NewString()
	for _, rt := range routes {
		foreign := intruder.Do(rt.Method, strings.ReplaceAll(rt.Path, "{id}", foreignID), rt.Body)
		missing := intruder.Do(rt.Method, strings.ReplaceAll(rt.Path, "{id}", missingID), rt.Body)
		var fe, me envelope
		_ = json.Unmarshal(foreign.Body.Bytes(), &fe)
		_ = json.Unmarshal(missing.Body.Bytes(), &me)
		if foreign.Code != http.StatusNotFound || fe.Error.Code != "not_found" {
			t.Errorf("%s %s as another user: %d %s, want 404 not_found", rt.Method, rt.Path, foreign.Code, foreign.Body)
			continue
		}
		if missing.Code != foreign.Code || me.Error != fe.Error {
			t.Errorf("%s %s: foreign (%d %+v) and nonexistent (%d %+v) responses differ",
				rt.Method, rt.Path, foreign.Code, fe.Error, missing.Code, me.Error)
		}
	}
}
