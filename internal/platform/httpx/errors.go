// Package httpx holds RelayLab's shared HTTP plumbing: the JSON error envelope,
// request IDs, access logging, panic recovery, security headers, size limits,
// and server lifecycle.
package httpx

import (
	"encoding/json"
	"net/http"
)

// ErrorBody is the JSON error envelope returned by every API error.
type ErrorBody struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail carries a stable snake_case code, a human-readable message, and
// the request ID. It never contains stack traces, SQL, paths, or config values.
type ErrorDetail struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
}

// WriteJSON writes v as JSON with the given status.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// WriteError writes the standard error envelope.
func WriteError(w http.ResponseWriter, r *http.Request, status int, code, msg string) {
	WriteJSON(w, status, ErrorBody{Error: ErrorDetail{
		Code:      code,
		Message:   msg,
		RequestID: RequestID(r.Context()),
	}})
}

// NotFound is the router's 404 handler.
func NotFound(w http.ResponseWriter, r *http.Request) {
	WriteError(w, r, http.StatusNotFound, "not_found", "The requested resource was not found.")
}

// MethodNotAllowed is the router's 405 handler.
func MethodNotAllowed(w http.ResponseWriter, r *http.Request) {
	WriteError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "This method is not allowed for this resource.")
}
