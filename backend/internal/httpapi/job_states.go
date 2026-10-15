package httpapi

import (
	"net/http"

	"jobassistant/backend/internal/identity"
)

func (h *Handler) jobStates(w http.ResponseWriter, r *http.Request, p identity.Principal, _ string) {
	ids := r.URL.Query()["job_id"]
	if len(ids) == 0 || len(ids) > 100 {
		h.fail(w, r, identity.ErrInvalidInput)
		return
	}
	for _, id := range ids {
		if !uuidPattern.MatchString(id) {
			h.fail(w, r, identity.ErrInvalidInput)
			return
		}
	}
	items, err := h.applications.JobStates(r.Context(), p.User.ID, ids, h.service.Now())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.writeJSON(w, r, http.StatusOK, map[string]any{"items": items})
}
