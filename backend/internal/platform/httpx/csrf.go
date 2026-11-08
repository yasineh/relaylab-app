package httpx

import (
	"mime"
	"net/http"
	"net/url"
)

func isUnsafe(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	default:
		return true
	}
}

// SameOrigin protects state-changing requests from cross-site use (CSRF) for a
// cookie-authenticated, same-origin JSON API. For POST, PUT, PATCH, and DELETE:
//
//   - Origin must equal appOrigin exactly; or, when the browser sent no Origin,
//     Sec-Fetch-Site must be "same-origin". Otherwise 403 origin_rejected.
//
// Safe methods pass through. No CORS headers are ever emitted.
func SameOrigin(appOrigin *url.URL) func(http.Handler) http.Handler {
	want := appOrigin.Scheme + "://" + appOrigin.Host
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isUnsafe(r.Method) {
				origin := r.Header.Get("Origin")
				ok := origin == want || (origin == "" && r.Header.Get("Sec-Fetch-Site") == "same-origin")
				if !ok {
					WriteError(w, r, http.StatusForbidden, "origin_rejected",
						"This request must come from the RelayLab app.")
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireJSON rejects unsafe requests that carry a body with any content type
// other than application/json (415 unsupported_media_type). This also blocks
// simple cross-site form posts in browsers that omit Origin.
func RequireJSON(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isUnsafe(r.Method) && hasBody(r) {
			mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if err != nil || mt != "application/json" {
				WriteError(w, r, http.StatusUnsupportedMediaType, "unsupported_media_type",
					"Send the request body as application/json.")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func hasBody(r *http.Request) bool {
	return r.ContentLength > 0 || r.ContentLength == -1 || len(r.TransferEncoding) > 0
}
