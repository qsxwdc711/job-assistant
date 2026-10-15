package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"jobassistant/backend/internal/application"
	"jobassistant/backend/internal/catalog"
	"jobassistant/backend/internal/identity"
)

// Option installs a complete HTTP feature before the handler is exposed to requests.
type Option func(*Handler) error

// WithRecruitment connects real catalog and private progress storage to the identity middleware.
func WithRecruitment(jobs *catalog.Store, applications *application.Store) Option {
	return func(h *Handler) error {
		if jobs == nil || applications == nil {
			return errors.New("recruitment stores are required")
		}
		h.catalog = jobs
		h.applications = applications
		return nil
	}
}

func (h *Handler) recruitmentRoutes() {
	h.mux.HandleFunc("GET /api/v1/jobs", h.protect(h.jobs, false))
	h.mux.HandleFunc("GET /api/v1/jobs/filters", h.protect(h.jobFilters, false))
	h.mux.HandleFunc("GET /api/v1/jobs/{id}", h.protect(h.jobDetail, false))
	h.mux.HandleFunc("GET /api/v1/me/job-states", h.protect(h.jobStates, false))
	for _, prefix := range []string{"/api/v1/me", "/api/v1/admin"} {
		h.mux.HandleFunc("GET "+prefix+"/favorites", h.protect(h.favorites, false))
		h.mux.HandleFunc("PUT "+prefix+"/favorites/{id}", h.protect(h.favorite, true))
		h.mux.HandleFunc("DELETE "+prefix+"/favorites/{id}", h.protect(h.favorite, true))
		h.mux.HandleFunc("GET "+prefix+"/applications", h.protect(h.applicationList, false))
		h.mux.HandleFunc("POST "+prefix+"/applications", h.protect(h.applicationCreate, true))
		h.mux.HandleFunc("GET "+prefix+"/applications/{id}", h.protect(h.applicationDetail, false))
		h.mux.HandleFunc("PATCH "+prefix+"/applications/{id}", h.protect(h.applicationUpdate, true))
		h.mux.HandleFunc("DELETE "+prefix+"/applications/{id}", h.protect(h.applicationDelete, true))
		h.mux.HandleFunc("GET "+prefix+"/applications/{id}/events", h.protect(h.applicationEvents, false))
	}
	h.mux.HandleFunc("GET /api/v1/admin/companies", h.protect(h.companies, false))
	h.mux.HandleFunc("PATCH /api/v1/admin/companies/{code}", h.protect(h.companyUpdate, true))
	h.mux.HandleFunc("GET /api/v1/admin/sources", h.protect(h.sources, false))
	h.mux.HandleFunc("PATCH /api/v1/admin/sources/{code}", h.protect(h.sourceUpdate, true))
	h.mux.HandleFunc("GET /api/v1/admin/jobs", h.protect(h.adminJobs, false))
	h.mux.HandleFunc("PATCH /api/v1/admin/jobs/{id}", h.protect(h.jobPublish, true))
}

func (h *Handler) jobs(w http.ResponseWriter, r *http.Request, p identity.Principal, _ string) {
	f, err := catalog.ParseFilter(r.URL.Query())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	page, err := h.catalog.Jobs(r.Context(), p.User.ID, f, h.service.Now())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.writeJSON(w, r, 200, page)
}
func (h *Handler) jobDetail(w http.ResponseWriter, r *http.Request, p identity.Principal, _ string) {
	if !h.validID(w, r) {
		return
	}
	job, err := h.catalog.Job(r.Context(), p.User.ID, r.PathValue("id"), h.service.Now())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.writeJSON(w, r, 200, map[string]any{"job": job})
}
func (h *Handler) jobFilters(w http.ResponseWriter, r *http.Request, p identity.Principal, _ string) {
	choices, err := h.catalog.Filters(r.Context(), p.User.ID, h.service.Now())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.writeJSON(w, r, 200, choices)
}

func (h *Handler) applicationScope(w http.ResponseWriter, r *http.Request, p identity.Principal) (application.Scope, bool) {
	scope := application.Scope{ActorID: p.User.ID, RequireAdmin: strings.HasPrefix(r.URL.Path, "/api/v1/admin/")}
	if scope.RequireAdmin {
		if err := identity.Authorize(p.User, identity.ManageUsers, "", h.service.Now()); err != nil {
			h.fail(w, r, err)
			return scope, false
		}
		scope.OwnerID = r.URL.Query().Get("owner_user_id")
		if scope.OwnerID != "" && !uuidPattern.MatchString(scope.OwnerID) {
			h.fail(w, r, identity.ErrInvalidInput)
			return scope, false
		}
	}
	return scope, true
}
