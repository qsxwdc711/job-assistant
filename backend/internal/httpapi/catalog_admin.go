package httpapi

import (
	"net/http"

	"jobassistant/backend/internal/catalog"
	"jobassistant/backend/internal/identity"
)

func (h *Handler) companies(w http.ResponseWriter, r *http.Request, p identity.Principal, _ string) {
	items, err := h.catalog.Companies(r.Context(), p.User.ID, h.service.Now())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.writeJSON(w, r, 200, map[string]any{"items": items})
}
func (h *Handler) companyUpdate(w http.ResponseWriter, r *http.Request, p identity.Principal, _ string) {
	var input catalog.Company
	if !h.decode(w, r, &input) {
		return
	}
	if err := h.catalog.ChangeCompany(r.Context(), p.User.ID, r.PathValue("code"), input, requestID(r), h.service.Now()); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(204)
}
func (h *Handler) sources(w http.ResponseWriter, r *http.Request, p identity.Principal, _ string) {
	items, err := h.catalog.Sources(r.Context(), p.User.ID, h.service.Now())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.writeJSON(w, r, 200, map[string]any{"items": items})
}
func (h *Handler) sourceUpdate(w http.ResponseWriter, r *http.Request, p identity.Principal, _ string) {
	var input struct {
		Enabled *bool `json:"enabled"`
	}
	if !h.decode(w, r, &input) {
		return
	}
	if input.Enabled == nil {
		h.fail(w, r, identity.ErrInvalidInput)
		return
	}
	if err := h.catalog.SetSourceEnabled(r.Context(), p.User.ID, r.PathValue("code"), *input.Enabled, requestID(r), h.service.Now()); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(204)
}
func (h *Handler) adminJobs(w http.ResponseWriter, r *http.Request, p identity.Principal, _ string) {
	f, err := catalog.ParseFilter(r.URL.Query())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	f.IncludeHidden = true
	if len(f.States) == 0 {
		f.States = []string{"all"}
	}
	page, err := h.catalog.Jobs(r.Context(), p.User.ID, f, h.service.Now())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.writeJSON(w, r, 200, page)
}
func (h *Handler) jobPublish(w http.ResponseWriter, r *http.Request, p identity.Principal, _ string) {
	if !h.validID(w, r) {
		return
	}
	var input struct {
		Published *bool  `json:"published"`
		Note      string `json:"note"`
	}
	if !h.decode(w, r, &input) {
		return
	}
	if input.Published == nil {
		h.fail(w, r, identity.ErrInvalidInput)
		return
	}
	if err := h.catalog.SetPublished(r.Context(), p.User.ID, r.PathValue("id"), *input.Published, input.Note, requestID(r), h.service.Now()); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(204)
}
