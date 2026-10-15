package httpapi

import (
	"net/http"

	"jobassistant/backend/internal/application"
	"jobassistant/backend/internal/identity"
)

func (h *Handler) applicationList(w http.ResponseWriter, r *http.Request, p identity.Principal, _ string) {
	scope, ok := h.applicationScope(w, r, p)
	if !ok {
		return
	}
	limit, offset, ok := h.pagination(w, r)
	if !ok {
		return
	}
	page, err := h.applications.List(r.Context(), scope, limit, offset, h.service.Now())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.writeJSON(w, r, 200, page)
}
func (h *Handler) applicationCreate(w http.ResponseWriter, r *http.Request, p identity.Principal, _ string) {
	scope, ok := h.applicationScope(w, r, p)
	if !ok {
		return
	}
	var input application.Input
	if !h.decode(w, r, &input) {
		return
	}
	record, err := h.applications.Create(r.Context(), scope, input, requestID(r), h.service.Now())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.writeJSON(w, r, 201, map[string]any{"application": record})
}
func (h *Handler) applicationDetail(w http.ResponseWriter, r *http.Request, p identity.Principal, _ string) {
	scope, ok := h.applicationScope(w, r, p)
	if !ok || !h.validID(w, r) {
		return
	}
	record, err := h.applications.Get(r.Context(), scope, r.PathValue("id"), h.service.Now())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.writeJSON(w, r, 200, map[string]any{"application": record})
}
func (h *Handler) applicationUpdate(w http.ResponseWriter, r *http.Request, p identity.Principal, _ string) {
	scope, ok := h.applicationScope(w, r, p)
	if !ok || !h.validID(w, r) {
		return
	}
	var input application.Input
	if !h.decode(w, r, &input) {
		return
	}
	record, err := h.applications.Update(r.Context(), scope, r.PathValue("id"), input, requestID(r), h.service.Now())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.writeJSON(w, r, 200, map[string]any{"application": record})
}
func (h *Handler) applicationDelete(w http.ResponseWriter, r *http.Request, p identity.Principal, _ string) {
	scope, ok := h.applicationScope(w, r, p)
	if !ok || !h.validID(w, r) {
		return
	}
	var input struct {
		Version int64 `json:"version"`
	}
	if !h.decode(w, r, &input) {
		return
	}
	if err := h.applications.Delete(r.Context(), scope, r.PathValue("id"), input.Version, requestID(r), h.service.Now()); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(204)
}
func (h *Handler) applicationEvents(w http.ResponseWriter, r *http.Request, p identity.Principal, _ string) {
	scope, ok := h.applicationScope(w, r, p)
	if !ok || !h.validID(w, r) {
		return
	}
	items, err := h.applications.Events(r.Context(), scope, r.PathValue("id"), h.service.Now())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.writeJSON(w, r, 200, map[string]any{"items": items})
}
func (h *Handler) favorites(w http.ResponseWriter, r *http.Request, p identity.Principal, _ string) {
	scope, ok := h.applicationScope(w, r, p)
	if !ok {
		return
	}
	limit, offset, ok := h.pagination(w, r)
	if !ok {
		return
	}
	items, err := h.applications.Favorites(r.Context(), scope, limit, offset, h.service.Now())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.writeJSON(w, r, 200, map[string]any{"items": items, "limit": limit, "offset": offset})
}
func (h *Handler) favorite(w http.ResponseWriter, r *http.Request, p identity.Principal, _ string) {
	scope, ok := h.applicationScope(w, r, p)
	if !ok || !h.validID(w, r) {
		return
	}
	if err := h.applications.SetFavorite(r.Context(), scope, r.PathValue("id"), r.Method == http.MethodPut, requestID(r), h.service.Now()); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(204)
}
