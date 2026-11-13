// Command echo is the end-to-end suite's controlled replay destination. It
// answers every request with 200 and a JSON summary (method, path, body
// size), and logs one line per request so tests can prove that blocked
// replays never connected. Test-only: it is reachable from the worker only
// because REPLAY_TEST_ALLOWLIST names it, which production refuses.
package main

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"time"
)

func main() {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		n, _ := io.Copy(io.Discard, io.LimitReader(r.Body, 1<<20))
		log.Printf("echo request method=%s path=%q bytes=%d", r.Method, r.URL.Path, n)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Echo", "relaylab-e2e")
		_ = json.NewEncoder(w).Encode(map[string]any{"echo": true, "method": r.Method, "path": r.URL.Path, "bytes": n})
	})
	log.Print("echo listening on :8080")
	srv := &http.Server{Addr: ":8080", ReadHeaderTimeout: 5 * time.Second}
	log.Fatal(srv.ListenAndServe())
}
