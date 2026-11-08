// Package identity establishes who is calling the RelayLab API. It proxies
// /api/auth/* to the Better Auth service on the same origin and validates
// Better Auth session cookies itself, read-only, for every /api/v1 request.
package identity

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"relaylab/internal/platform/config"
	"relaylab/internal/platform/httpx"
	"relaylab/internal/platform/logging"
)

// ProxyHeader carries the shared secret that the auth service requires on
// every request (defence in depth: the service is never meant to be reachable
// directly).
const ProxyHeader = "X-RelayLab-Proxy"

// ProxyOptions configures the /api/auth reverse proxy.
type ProxyOptions struct {
	Target      *url.URL      // internal auth service base URL
	Secret      config.Secret // AUTH_PROXY_SHARED_SECRET
	Timeout     time.Duration // end-to-end bound per request
	ForwardedTo string        // X-Forwarded-Proto value: the public scheme (from APP_ORIGIN)
}

// NewAuthProxy returns a handler that forwards requests unchanged in path,
// query, method, and body to the auth service, and returns its response
// (including Set-Cookie) on the application origin.
//
// It strips hop-by-hop headers and any client-supplied forwarding headers,
// sets X-Forwarded-For to the client IP derived from trusted proxies, adds the
// shared secret header, maps Better Auth's X-Retry-After to Retry-After on 429,
// removes any CORS headers, and never logs headers, cookies, or bodies.
func NewAuthProxy(o ProxyOptions) http.Handler {
	transport := &http.Transport{
		Proxy:                 nil, // never route auth traffic through an environment proxy
		DialContext:           (&net.Dialer{Timeout: 3 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		MaxIdleConns:          32,
		MaxIdleConnsPerHost:   32,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: o.Timeout,
		ForceAttemptHTTP2:     false,
	}
	rp := &httputil.ReverseProxy{
		Transport: transport,
		// Rewrite (not Director): the inbound Forwarded / X-Forwarded-* headers and
		// hop-by-hop headers are removed before Rewrite runs.
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(o.Target)
			pr.Out.Host = o.Target.Host
			h := pr.Out.Header
			h.Del(ProxyHeader)
			h.Set(ProxyHeader, o.Secret.Reveal())
			if ip, ok := httpx.ClientIP(pr.In.Context()); ok {
				h.Set("X-Forwarded-For", ip.String())
			}
			h.Set("X-Forwarded-Proto", o.ForwardedTo)
			if id := httpx.RequestID(pr.In.Context()); id != "" {
				h.Set(httpx.RequestIDHeader, id)
			}
		},
		ModifyResponse: func(res *http.Response) error {
			for k := range res.Header {
				if strings.HasPrefix(strings.ToLower(k), "access-control-") {
					res.Header.Del(k)
				}
			}
			if res.StatusCode == http.StatusTooManyRequests && res.Header.Get("Retry-After") == "" {
				if v := res.Header.Get("X-Retry-After"); v != "" {
					res.Header.Set("Retry-After", v)
				}
			}
			// The auth service's own request id must not replace ours.
			res.Header.Del(httpx.RequestIDHeader)
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			var tooLarge *http.MaxBytesError
			switch {
			case errors.As(err, &tooLarge):
				httpx.WriteError(w, r, http.StatusRequestEntityTooLarge, "body_too_large", "Request body is too large.")
				return
			case errors.Is(err, context.Canceled) && r.Context().Err() != nil && !errors.Is(r.Context().Err(), context.DeadlineExceeded):
				// Client went away; nothing useful to send.
				return
			}
			status, code := http.StatusBadGateway, "auth_unavailable"
			if errors.Is(err, context.DeadlineExceeded) || isTimeout(err) {
				status, code = http.StatusGatewayTimeout, "auth_timeout"
			}
			// Log the error class only: transport errors can contain the upstream URL.
			logging.FromContext(r.Context()).Warn("auth proxy error",
				slog.Int("status", status), slog.String("kind", errorKind(err)))
			httpx.WriteError(w, r, status, code, "Authentication is temporarily unavailable. Try again shortly.")
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), o.Timeout)
		defer cancel()
		rp.ServeHTTP(w, r.WithContext(ctx))
	})
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

func errorKind(err error) string {
	var opErr *net.OpError
	switch {
	case errors.Is(err, context.DeadlineExceeded), isTimeout(err):
		return "timeout"
	case errors.As(err, &opErr) && opErr.Op == "dial":
		return "connect"
	default:
		return "upstream"
	}
}
