// Package httpx holds RelayLab's shared HTTP plumbing: the JSON error envelope,
// request IDs, access logging, panic recovery, security headers, size limits,
// and server lifecycle.
package httpx

import (
	"encoding/json"
	"errors"
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
	// Fields maps request fields to problems, for 422 validation_failed.
	Fields map[string]string `json:"fields,omitempty"`
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

// WriteValidationError writes 422 validation_failed with per-field messages.
func WriteValidationError(w http.ResponseWriter, r *http.Request, fields map[string]string) {
	WriteJSON(w, http.StatusUnprocessableEntity, ErrorBody{Error: ErrorDetail{
		Code:      "validation_failed",
		Message:   "Some fields are invalid.",
		RequestID: RequestID(r.Context()),
		Fields:    fields,
	}})
}

// DecodeJSON decodes a single JSON object from the request body into dst,
// rejecting unknown fields and trailing data. On failure it writes the error
// response (413 body_too_large or 400 invalid_json) and returns false.
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	err := dec.Decode(dst)
	if err == nil && dec.More() {
		err = errors.New("trailing data")
	}
	if err == nil {
		return true
	}
	if IsBodyTooLarge(err) {
		WriteError(w, r, http.StatusRequestEntityTooLarge, "body_too_large", "Request body is too large.")
		return false
	}
	WriteError(w, r, http.StatusBadRequest, "invalid_json", "The request body must be a valid JSON object with known fields.")
	return false
}

// IsBodyTooLarge reports whether err came from a BodyLimit-capped body.
func IsBodyTooLarge(err error) bool {
	var mbe *http.MaxBytesError
	return errors.As(err, &mbe)
}
