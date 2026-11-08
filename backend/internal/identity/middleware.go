package identity

import (
	"errors"
	"log/slog"
	"net/http"

	"relaylab/internal/platform/httpx"
	"relaylab/internal/platform/logging"
)

// RequireSession authenticates every request with v. On success the identity is
// in the request context (FromContext). Failures use the standard envelope:
//
//	401 unauthenticated     absent, malformed, tampered, unknown, or revoked session
//	401 session_expired     the session's expiry time has passed
//	403 email_not_verified  verification is required and the user has not verified
//	500 internal_error      the session store is unavailable
func RequireSession(v *Validator, requireVerification bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id, err := v.Validate(r.Context(), r)
			switch {
			case errors.Is(err, ErrUnauthenticated):
				httpx.WriteError(w, r, http.StatusUnauthorized, "unauthenticated", "Sign in to continue.")
				return
			case errors.Is(err, ErrSessionExpired):
				httpx.WriteError(w, r, http.StatusUnauthorized, "session_expired",
					"Your session has expired. Sign in again.")
				return
			case err != nil:
				logging.FromContext(r.Context()).Error("session validation failed",
					slog.String("error", err.Error()))
				httpx.WriteError(w, r, http.StatusInternalServerError, "internal_error",
					"An unexpected error occurred.")
				return
			}
			if requireVerification && !id.EmailVerified {
				httpx.WriteError(w, r, http.StatusForbidden, "email_not_verified",
					"Verify your email address to continue.")
				return
			}
			ctx := WithIdentity(r.Context(), id)
			ctx = logging.WithLogger(ctx, logging.FromContext(ctx).With(slog.String("user_id", id.UserID)))
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// meResponse is the body of GET /api/v1/me.
type meResponse struct {
	ID            string `json:"id"`
	Email         string `json:"email"`
	Name          string `json:"name"`
	EmailVerified bool   `json:"email_verified"`
}

// Me handles GET /api/v1/me: the signed-in user, from the validated session only.
func Me(w http.ResponseWriter, r *http.Request) {
	id, ok := FromContext(r.Context())
	if !ok {
		httpx.WriteError(w, r, http.StatusUnauthorized, "unauthenticated", "Sign in to continue.")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusOK, meResponse{
		ID:            id.UserID,
		Email:         id.Email,
		Name:          id.Name,
		EmailVerified: id.EmailVerified,
	})
}
