// Package webui serves the built single-page app. The production image builds
// web/, copies web/dist to internal/webui/dist, and compiles with
// `-tags embedweb`; development builds embed nothing and Vite serves the app.
package webui

import (
	"errors"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// Handler serves static files from fsys and falls back to index.html for
// client-side routes. Hashed assets are cached long-term; index.html is not.
func Handler(fsys fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name == "" {
			name = "index.html"
		}
		if !fs.ValidPath(name) {
			http.NotFound(w, r)
			return
		}
		// name is cleaned and validated; fs.FS cannot escape its root.
		if info, err := fs.Stat(fsys, name); err == nil && !info.IsDir() { //nolint:gosec // G703: see above
			if strings.HasPrefix(name, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			} else {
				w.Header().Set("Cache-Control", "no-cache")
			}
			http.ServeFileFS(w, r, fsys, name) //nolint:gosec // G703: name validated with fs.ValidPath above
			return
		} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		// Missing asset files are real 404s; other paths are SPA routes.
		if strings.HasPrefix(name, "assets/") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFileFS(w, r, fsys, "index.html")
	})
}
