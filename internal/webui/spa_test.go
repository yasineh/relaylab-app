package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

var testFS = fstest.MapFS{
	"index.html":         {Data: []byte("<!doctype html><title>RelayLab</title><script type=\"module\" src=\"/assets/app.js\"></script>")},
	"assets/app-abc.js":  {Data: []byte("console.log(1)")},
	"assets/app-abc.css": {Data: []byte("body{}")},
	"favicon.svg":        {Data: []byte("<svg/>")},
}

func get(path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	Handler(testFS).ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	return rec
}

func TestServesIndexAtRoot(t *testing.T) {
	rec := get("/")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "<!doctype html>") {
		t.Fatalf("got %d %q", rec.Code, rec.Body)
	}
	if rec.Header().Get("Cache-Control") != "no-cache" || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("headers: %v", rec.Header())
	}
}

func TestClientRouteFallsBackToIndex(t *testing.T) {
	rec := get("/projects/123/events")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "<title>RelayLab</title>") {
		t.Fatalf("got %d %q", rec.Code, rec.Body)
	}
}

func TestAssetsServedWithLongCache(t *testing.T) {
	rec := get("/assets/app-abc.js")
	if rec.Code != 200 || rec.Body.String() != "console.log(1)" {
		t.Fatalf("got %d %q", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("cache header: %q", rec.Header().Get("Cache-Control"))
	}
	if !strings.Contains(rec.Header().Get("Content-Type"), "javascript") {
		t.Fatalf("content-type: %q", rec.Header().Get("Content-Type"))
	}
}

func TestMissingAssetIs404(t *testing.T) {
	if rec := get("/assets/missing.js"); rec.Code != http.StatusNotFound {
		t.Fatalf("got %d", rec.Code)
	}
}

func TestPathTraversalStaysInFS(t *testing.T) {
	// net/http rejects ".." paths outright (400); either way nothing outside
	// the embedded FS can be served.
	for _, p := range []string{"/../../etc/passwd", "/assets/../../go.mod"} {
		rec := get(p)
		if rec.Code != http.StatusBadRequest && !strings.Contains(rec.Body.String(), "<title>RelayLab</title>") {
			t.Fatalf("%s: unexpected response %d %q", p, rec.Code, rec.Body)
		}
		if strings.Contains(rec.Body.String(), "root:") || strings.Contains(rec.Body.String(), "module relaylab") {
			t.Fatalf("%s: traversal escaped the FS", p)
		}
	}
}
