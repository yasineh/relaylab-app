package main

import (
	"net/http"

	"relaylab/internal/webui"
)

// webHandler returns the embedded single-page app, or nil in development
// builds where Vite serves the app and proxies API calls.
func webHandler() http.Handler {
	fsys, ok := webui.FS()
	if !ok {
		return nil
	}
	return webui.Handler(fsys)
}
