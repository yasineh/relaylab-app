package endpoints

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"relaylab/internal/identity"
	"relaylab/internal/platform/httpx"
	"relaylab/internal/platform/logging"
)

// Handler serves /api/v1/projects/{projectId}/endpoints. It must be mounted
// behind identity.RequireSession and the same-origin and JSON checks.
type Handler struct{ svc *Service }

// NewHandler returns the HTTP handler for endpoints.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Routes registers the endpoint routes (mounted at /projects/{projectId}/endpoints).
func (h *Handler) Routes(r chi.Router) {
	r.Get("/", h.list)
	r.Post("/", h.create)
	r.Get("/{endpointId}", h.get)
	r.Patch("/{endpointId}", h.update)
	r.Post("/{endpointId}/rotate", h.rotate)
	r.Delete("/{endpointId}", h.delete)
}

// endpointJSON is the API representation. It never contains the token hash,
// ciphertext, or key material: only the receiving URL.
type endpointJSON struct {
	ID             string     `json:"id"`
	ProjectID      string     `json:"project_id"`
	Name           string     `json:"name"`
	Enabled        bool       `json:"enabled"`
	URL            *string    `json:"url"`
	LastReceivedAt *time.Time `json:"last_received_at"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

func toJSON(e Endpoint) endpointJSON {
	return endpointJSON{
		ID: e.ID.String(), ProjectID: e.ProjectID.String(), Name: e.Name, Enabled: e.Enabled,
		URL: e.URL, LastReceivedAt: e.LastReceivedAt, CreatedAt: e.CreatedAt, UpdatedAt: e.UpdatedAt,
	}
}

// scope returns the session user and the parsed path ids. The owner comes
// only from the validated session.
func (h *Handler) scope(w http.ResponseWriter, r *http.Request, withEndpoint bool) (string, uuid.UUID, uuid.UUID, bool) {
	id, ok := identity.FromContext(r.Context())
	if !ok {
		httpx.WriteError(w, r, http.StatusUnauthorized, "unauthenticated", "Sign in to continue.")
		return "", uuid.Nil, uuid.Nil, false
	}
	projectID, err := ParseID(chi.URLParam(r, "projectId"))
	if err != nil {
		h.fail(w, r, err)
		return "", uuid.Nil, uuid.Nil, false
	}
	var endpointID uuid.UUID
	if withEndpoint {
		if endpointID, err = ParseID(chi.URLParam(r, "endpointId")); err != nil {
			h.fail(w, r, err)
			return "", uuid.Nil, uuid.Nil, false
		}
	}
	return id.UserID, projectID, endpointID, true
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	owner, projectID, _, ok := h.scope(w, r, false)
	if !ok {
		return
	}
	list, err := h.svc.List(r.Context(), owner, projectID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	items := make([]endpointJSON, 0, len(list))
	for _, e := range list {
		items = append(items, toJSON(e))
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	owner, projectID, _, ok := h.scope(w, r, false)
	if !ok {
		return
	}
	// Authorize before reading the body: another user's project is a plain 404.
	if err := h.svc.AuthorizeProject(r.Context(), owner, projectID); err != nil {
		h.fail(w, r, err)
		return
	}
	var req struct {
		Name *string `json:"name"`
	}
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	name := ""
	if req.Name != nil {
		name = *req.Name
	}
	e, err := h.svc.Create(r.Context(), owner, projectID, name)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("Location", "/api/v1/projects/"+projectID.String()+"/endpoints/"+e.ID.String())
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusCreated, toJSON(e))
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	owner, projectID, endpointID, ok := h.scope(w, r, true)
	if !ok {
		return
	}
	e, err := h.svc.Get(r.Context(), owner, projectID, endpointID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusOK, toJSON(e))
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	owner, projectID, endpointID, ok := h.scope(w, r, true)
	if !ok {
		return
	}
	// Authorize before reading the body: an inaccessible endpoint is a plain 404.
	if _, err := h.svc.Get(r.Context(), owner, projectID, endpointID); err != nil {
		h.fail(w, r, err)
		return
	}
	var raw map[string]json.RawMessage
	if !httpx.DecodeJSON(w, r, &raw) {
		return
	}
	var name *string
	var enabled *bool
	for key, val := range raw {
		switch key {
		case "name":
			var s string
			if json.Unmarshal(val, &s) != nil {
				httpx.WriteValidationError(w, r, map[string]string{"name": "Name must be a string."})
				return
			}
			name = &s
		case "enabled":
			var b bool
			if json.Unmarshal(val, &b) != nil {
				httpx.WriteValidationError(w, r, map[string]string{"enabled": "Enabled must be true or false."})
				return
			}
			enabled = &b
		default:
			httpx.WriteError(w, r, http.StatusBadRequest, "invalid_json",
				"The request body must be a valid JSON object with known fields.")
			return
		}
	}
	e, err := h.svc.Update(r.Context(), owner, projectID, endpointID, name, enabled)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusOK, toJSON(e))
}

func (h *Handler) rotate(w http.ResponseWriter, r *http.Request) {
	owner, projectID, endpointID, ok := h.scope(w, r, true)
	if !ok {
		return
	}
	e, err := h.svc.Rotate(r.Context(), owner, projectID, endpointID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	logging.FromContext(r.Context()).Info("endpoint token rotated", slog.String("endpoint_id", e.ID.String()))
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusOK, toJSON(e))
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	owner, projectID, endpointID, ok := h.scope(w, r, true)
	if !ok {
		return
	}
	if err := h.svc.Delete(r.Context(), owner, projectID, endpointID); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// fail maps service errors to the envelope; inaccessible and missing resources
// share one 404 so ownership cannot be probed.
func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	var ve *ValidationError
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.NotFound(w, r)
	case errors.Is(err, ErrLimitReached):
		httpx.WriteError(w, r, http.StatusConflict, "endpoint_limit_reached",
			"This project has the maximum number of endpoints. Delete one to create another.")
	case errors.As(err, &ve):
		httpx.WriteValidationError(w, r, ve.Fields)
	default:
		logging.FromContext(r.Context()).Error("endpoint request failed", slog.String("error", err.Error()))
		httpx.WriteError(w, r, http.StatusInternalServerError, "internal_error", "An unexpected error occurred.")
	}
}
