package httpx

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"regexp"
	"runtime/debug"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"relaylab/internal/platform/logging"
)

// RequestIDHeader is the request/response header carrying the request ID.
const RequestIDHeader = "X-Request-ID"

var safeRequestID = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

type requestIDKey struct{}
type clientIPKey struct{}

// RequestID returns the request ID stored in ctx, or "" if none.
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// ClientIP returns the client IP determined by RealIP, if any.
func ClientIP(ctx context.Context) (netip.Addr, bool) {
	ip, ok := ctx.Value(clientIPKey{}).(netip.Addr)
	return ip, ok
}

// RequestIDMiddleware reuses a safe inbound X-Request-ID or generates one, sets
// it on the response, and attaches a request-scoped logger to the context.
func RequestIDMiddleware(base *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get(RequestIDHeader)
			if !safeRequestID.MatchString(id) {
				id = uuid.NewString()
			}
			w.Header().Set(RequestIDHeader, id)
			ctx := context.WithValue(r.Context(), requestIDKey{}, id)
			ctx = logging.WithLogger(ctx, base.With(slog.String("request_id", id)))
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RealIP records the client IP. X-Forwarded-For is honoured only when the
// direct peer is inside a trusted proxy CIDR; the rightmost untrusted address
// wins so clients cannot spoof their identity.
func RealIP(trusted []netip.Prefix) func(http.Handler) http.Handler {
	isTrusted := func(a netip.Addr) bool {
		for _, p := range trusted {
			if p.Contains(a) {
				return true
			}
		}
		return false
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			host, _, err := net.SplitHostPort(r.RemoteAddr)
			if err != nil {
				host = r.RemoteAddr
			}
			ip, err := netip.ParseAddr(host)
			if err == nil {
				ip = ip.Unmap()
				if isTrusted(ip) {
					parts := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
					for i := len(parts) - 1; i >= 0; i-- {
						cand, perr := netip.ParseAddr(strings.TrimSpace(parts[i]))
						if perr != nil {
							break
						}
						ip = cand.Unmap()
						if !isTrusted(ip) {
							break
						}
					}
				}
				r = r.WithContext(context.WithValue(r.Context(), clientIPKey{}, ip))
			}
			next.ServeHTTP(w, r)
		})
	}
}

// statusRecorder captures status and bytes written for access logs.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += n
	return n, err
}

// Unwrap lets http.ResponseController reach the underlying writer (flush, deadlines).
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// AccessLog emits one structured line per request with the route pattern
// (never the raw path or query string) and no headers or bodies.
func AccessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)

		route := "unmatched"
		if rc := chi.RouteContext(r.Context()); rc != nil && rc.RoutePattern() != "" {
			route = rc.RoutePattern()
		}
		status := rec.status
		if status == 0 {
			status = http.StatusOK
		}
		level := slog.LevelInfo
		if status >= 500 {
			level = slog.LevelError
		}
		logging.FromContext(r.Context()).LogAttrs(r.Context(), level, "http_request",
			slog.String("method", r.Method),
			slog.String("route", route),
			slog.Int("status", status),
			slog.Int64("duration_ms", time.Since(start).Milliseconds()),
			slog.Int("bytes", rec.bytes),
		)
	})
}

// Recoverer converts handler panics into a 500 envelope and logs the stack.
func Recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				logging.FromContext(r.Context()).Error("panic recovered",
					slog.String("panic", fmt.Sprint(v)),
					slog.String("stack", string(debug.Stack())),
				)
				WriteError(w, r, http.StatusInternalServerError, "internal_error", "An unexpected error occurred.")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// ContentSecurityPolicy is applied to every response. It forbids inline scripts,
// plugins, and framing; inline style attributes are allowed for UI libraries.
const ContentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data:; font-src 'self'; connect-src 'self'; object-src 'none'; " +
	"base-uri 'self'; form-action 'self'; frame-ancestors 'none'"

// SecurityHeaders sets the baseline security headers on every response.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Content-Security-Policy", ContentSecurityPolicy)
		next.ServeHTTP(w, r)
	})
}

// HeaderLimit rejects requests whose headers exceed maxBytes with 431.
// http.Server.MaxHeaderBytes remains a hard backstop at the connection level.
func HeaderLimit(maxBytes int) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			size := len(r.Method) + len(r.RequestURI) + len(r.Proto) + 4
			for k, vs := range r.Header {
				for _, v := range vs {
					size += len(k) + len(v) + 4
				}
			}
			if size > maxBytes {
				WriteError(w, r, http.StatusRequestHeaderFieldsTooLarge, "headers_too_large",
					"Request headers are too large.")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// BodyLimit caps request bodies; handlers see an error after maxBytes and
// should respond 413 using IsBodyTooLarge.
func BodyLimit(maxBytes int) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ContentLength > int64(maxBytes) {
				WriteError(w, r, http.StatusRequestEntityTooLarge, "body_too_large", "Request body is too large.")
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, int64(maxBytes))
			next.ServeHTTP(w, r)
		})
	}
}
