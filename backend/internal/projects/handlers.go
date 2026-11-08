package projects

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"relaylab/internal/identity"
	"relaylab/internal/platform/httpx"
	"relaylab/internal/platform/logging"
)

// Handler serves /api/v1/projects. It must be mounted behind
// identity.RequireSession and the same-origin and JSON checks.
type Handler struct{ svc *Service }

// NewHandler returns the HTTP handler for projects.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Routes registers the project routes on r (mounted at /api/v1/projects).
func (h *Handler) Routes(r chi.Router) {
	r.Get("/", h.list)
	r.Post("/", h.create)
	r.Get("/{projectId}", h.get)
	r.Patch("/{projectId}", h.update)
	r.Delete("/{projectId}", h.delete)
}

// projectJSON is the API representation of a project.
type projectJSON struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description *string   `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func toJSON(p Project) projectJSON {
	return projectJSON{ID: p.ID.String(), Name: p.Name, Description: p.Description, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt}
}

// owner returns the acting user from the validated session; never from the request.
func owner(w http.ResponseWriter, r *http.Request) (string, bool) {
	id, ok := identity.FromContext(r.Context())
	if !ok {
		httpx.WriteError(w, r, http.StatusUnauthorized, "unauthenticated", "Sign in to continue.")
		return "", false
	}
	return id.UserID, true
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	userID, ok := owner(w, r)
	if !ok {
		return
	}
	list, err := h.svc.List(r.Context(), userID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	items := make([]projectJSON, 0, len(list))
	for _, p := range list {
		items = append(items, toJSON(p))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

type createRequest struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	userID, ok := owner(w, r)
	if !ok {
		return
	}
	var req createRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	name := ""
	if req.Name != nil {
		name = *req.Name
	}
	p, err := h.svc.Create(r.Context(), userID, name, req.Description)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("Location", "/api/v1/projects/"+p.ID.String())
	httpx.WriteJSON(w, http.StatusCreated, toJSON(p))
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	userID, ok := owner(w, r)
	if !ok {
		return
	}
	id, err := ParseID(chi.URLParam(r, "projectId"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	p, err := h.svc.Authorize(r.Context(), userID, id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toJSON(p))
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	userID, ok := owner(w, r)
	if !ok {
		return
	}
	id, err := ParseID(chi.URLParam(r, "projectId"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	// Decode to raw fields so `"description": null` (clear) differs from an absent key.
	var raw map[string]json.RawMessage
	if !httpx.DecodeJSON(w, r, &raw) {
		return
	}
	var u Update
	for key, val := range raw {
		switch key {
		case "name":
			var s string
			if json.Unmarshal(val, &s) != nil {
				httpx.WriteValidationError(w, r, map[string]string{"name": "Name must be a string."})
				return
			}
			u.Name = Optional[string]{Set: true, Value: s}
		case "description":
			var s *string
			if json.Unmarshal(val, &s) != nil {
				httpx.WriteValidationError(w, r, map[string]string{"description": "Description must be a string or null."})
				return
			}
			u.Description = Optional[*string]{Set: true, Value: s}
		default:
			httpx.WriteError(w, r, http.StatusBadRequest, "invalid_json",
				"The request body must be a valid JSON object with known fields.")
			return
		}
	}
	p, err := h.svc.Update(r.Context(), userID, id, u)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toJSON(p))
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	userID, ok := owner(w, r)
	if !ok {
		return
	}
	id, err := ParseID(chi.URLParam(r, "projectId"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if err := h.svc.Delete(r.Context(), userID, id); err != nil {
		h.fail(w, r, err)
		return
	}
	logging.FromContext(r.Context()).Info("project deleted", slog.String("project_id", id.String()))
	w.WriteHeader(http.StatusNoContent)
}

// fail maps service errors to the error envelope. Missing, foreign, and
// malformed projects share one response so ownership is not observable.
func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	var ve *ValidationError
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.NotFound(w, r)
	case errors.Is(err, ErrLimitReached):
		httpx.WriteError(w, r, http.StatusConflict, "project_limit_reached",
			"You have reached the maximum number of projects. Delete one to create another.")
	case errors.As(err, &ve):
		httpx.WriteValidationError(w, r, ve.Fields)
	default:
		logging.FromContext(r.Context()).Error("project request failed", slog.String("error", err.Error()))
		httpx.WriteError(w, r, http.StatusInternalServerError, "internal_error", "An unexpected error occurred.")
	}
}
