package httpapi

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strings"

	"jobassistant/backend/internal/identity"
	"jobassistant/backend/internal/matching"
	"jobassistant/backend/internal/profile"
	"jobassistant/backend/internal/task"
)

// WithResumes installs private storage and queue endpoints; no request calls the model.
func WithResumes(profiles *profile.Store, matches *matching.Store, tasks *task.Store, configured bool, version string) Option {
	return func(h *Handler) error {
		if profiles == nil || matches == nil || tasks == nil {
			return errors.New("private stores are required")
		}
		h.profiles = profiles
		h.matches = matches
		h.tasks = tasks
		h.modelConfigured = configured
		h.modelVersion = version
		return nil
	}
}
func (h *Handler) resumeRoutes() {
	for _, prefix := range []string{"/api/v1/me", "/api/v1/admin"} {
		h.mux.HandleFunc("GET "+prefix+"/resumes", h.protect(h.resumeList, false))
		h.mux.HandleFunc("POST "+prefix+"/resumes", h.protect(h.resumeUpload, true))
		h.mux.HandleFunc("GET "+prefix+"/resumes/{id}", h.protect(h.resumeDetail, false))
		h.mux.HandleFunc("GET "+prefix+"/resumes/{id}/download", h.protect(h.resumeDownload, false))
		h.mux.HandleFunc("DELETE "+prefix+"/resumes/{id}", h.protect(h.resumeDelete, true))
		h.mux.HandleFunc("PATCH "+prefix+"/resumes/{id}/preview", h.protect(h.resumePreview, true))
		h.mux.HandleFunc("POST "+prefix+"/resumes/{id}/parse", h.protect(h.resumeParse, true))
		h.mux.HandleFunc("POST "+prefix+"/resumes/{id}/confirm", h.protect(h.resumeConfirm, true))
		h.mux.HandleFunc("POST "+prefix+"/resumes/{id}/versions/{versionID}/activate", h.protect(h.profileActivate, true))
		h.mux.HandleFunc("GET "+prefix+"/profiles", h.protect(h.profileList, false))
		h.mux.HandleFunc("GET "+prefix+"/tasks/{id}", h.protect(h.privateTask, false))
		h.mux.HandleFunc("POST "+prefix+"/recommendations", h.protect(h.recommendationCreate, true))
		h.mux.HandleFunc("GET "+prefix+"/recommendations", h.protect(h.recommendationList, false))
		h.mux.HandleFunc("GET "+prefix+"/recommendations/{id}", h.protect(h.recommendationList, false))
	}
}
func (h *Handler) profileScope(w http.ResponseWriter, r *http.Request, p identity.Principal) (profile.Scope, bool) {
	scope := profile.Scope{ActorID: p.User.ID, Admin: strings.HasPrefix(r.URL.Path, "/api/v1/admin/")}
	if scope.Admin {
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
func (h *Handler) resumeList(w http.ResponseWriter, r *http.Request, p identity.Principal, _ string) {
	scope, ok := h.profileScope(w, r, p)
	if !ok {
		return
	}
	limit, offset, ok := h.pagination(w, r)
	if !ok {
		return
	}
	items, err := h.profiles.List(r.Context(), scope, limit, offset, requestID(r), h.service.Now())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.writeJSON(w, r, 200, map[string]any{"items": items, "model_configured": h.modelConfigured, "model_version": h.modelVersion, "max_file_bytes": profile.MaxFileBytes})
}
func (h *Handler) resumeUpload(w http.ResponseWriter, r *http.Request, p identity.Principal, _ string) {
	scope, ok := h.profileScope(w, r, p)
	if !ok {
		return
	}
	if err := identity.Authorize(p.User, identity.UploadResume, scope.OwnerID, h.service.Now()); err != nil {
		h.fail(w, r, err)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, profile.MaxFileBytes+32*1024)
	reader, err := r.MultipartReader()
	if err != nil {
		h.writeError(w, r, 415, "unsupported_media_type", "multipart/form-data required")
		return
	}
	var data []byte
	var filename string
	names := []string{}
	parts := 0
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			h.uploadError(w, r, err)
			return
		}
		parts++
		if parts > 12 {
			part.Close()
			h.fail(w, r, identity.ErrInvalidInput)
			return
		}
		switch part.FormName() {
		case "file":
			if filename != "" || part.FileName() == "" {
				part.Close()
				h.fail(w, r, identity.ErrInvalidInput)
				return
			}
			filename = part.FileName()
			data, err = io.ReadAll(io.LimitReader(part, profile.MaxFileBytes+1))
			if len(data) > profile.MaxFileBytes {
				part.Close()
				h.writeError(w, r, 413, "body_too_large", "resume exceeds 10 MiB")
				return
			}
		case "sensitive_name":
			var value []byte
			value, err = io.ReadAll(io.LimitReader(part, 121))
			if len(value) > 120 {
				part.Close()
				h.fail(w, r, identity.ErrInvalidInput)
				return
			}
			if strings.TrimSpace(string(value)) != "" {
				names = append(names, strings.TrimSpace(string(value)))
			}
		default:
			part.Close()
			h.fail(w, r, identity.ErrInvalidInput)
			return
		}
		closeErr := part.Close()
		if err != nil {
			h.uploadError(w, r, err)
			return
		}
		if closeErr != nil {
			h.uploadError(w, r, closeErr)
			return
		}
	}
	if filename == "" {
		h.fail(w, r, identity.ErrInvalidInput)
		return
	}
	item, err := h.profiles.Upload(r.Context(), scope, filename, data, names, requestID(r), h.service.Now())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.writeJSON(w, r, 202, map[string]any{"resume": item})
}
func (h *Handler) uploadError(w http.ResponseWriter, r *http.Request, err error) {
	var sizeErr *http.MaxBytesError
	if errors.As(err, &sizeErr) {
		h.writeError(w, r, 413, "body_too_large", "resume upload exceeds limit")
	} else {
		h.fail(w, r, profile.ErrInvalidFile)
	}
}
func (h *Handler) resumeDetail(w http.ResponseWriter, r *http.Request, p identity.Principal, _ string) {
	scope, ok := h.profileScope(w, r, p)
	if !ok || !h.validID(w, r) {
		return
	}
	detail, err := h.profiles.Detail(r.Context(), scope, r.PathValue("id"), requestID(r), h.service.Now())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.writeJSON(w, r, 200, detail)
}
func (h *Handler) resumeDownload(w http.ResponseWriter, r *http.Request, p identity.Principal, _ string) {
	scope, ok := h.profileScope(w, r, p)
	if !ok || !h.validID(w, r) {
		return
	}
	meta, data, err := h.profiles.Download(r.Context(), scope, r.PathValue("id"), requestID(r), h.service.Now())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	contentType := "application/pdf"
	if strings.EqualFold(filepath.Ext(meta.Filename), ".docx") {
		contentType = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": meta.Filename}))
	w.WriteHeader(200)
	if _, err = w.Write(data); err != nil {
		return
	}
}
func (h *Handler) resumeDelete(w http.ResponseWriter, r *http.Request, p identity.Principal, _ string) {
	scope, ok := h.profileScope(w, r, p)
	if !ok || !h.validID(w, r) {
		return
	}
	if err := h.profiles.Delete(r.Context(), scope, r.PathValue("id"), requestID(r), h.service.Now()); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(204)
}
func (h *Handler) resumePreview(w http.ResponseWriter, r *http.Request, p identity.Principal, _ string) {
	scope, ok := h.profileScope(w, r, p)
	if !ok || !h.validID(w, r) {
		return
	}
	var input struct {
		Revision int64  `json:"revision"`
		Text     string `json:"text"`
	}
	// Preview editing uses a larger but still bounded JSON body than ordinary metadata writes.
	if !h.decodePreview(w, r, &input) {
		return
	}
	if err := h.profiles.UpdatePreview(r.Context(), scope, r.PathValue("id"), input.Revision, input.Text, requestID(r), h.service.Now()); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(204)
}
func (h *Handler) resumeParse(w http.ResponseWriter, r *http.Request, p identity.Principal, _ string) {
	scope, ok := h.profileScope(w, r, p)
	if !ok || !h.validID(w, r) {
		return
	}
	var input struct {
		Revision int64 `json:"revision"`
		Consent  bool  `json:"consent"`
	}
	if !h.decode(w, r, &input) {
		return
	}
	j, err := h.profiles.StartParse(r.Context(), scope, r.PathValue("id"), input.Revision, input.Consent, h.modelConfigured, requestID(r), h.service.Now())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.writeJSON(w, r, 202, map[string]any{"task": j})
}
func (h *Handler) resumeConfirm(w http.ResponseWriter, r *http.Request, p identity.Principal, _ string) {
	scope, ok := h.profileScope(w, r, p)
	if !ok || !h.validID(w, r) {
		return
	}
	var input struct {
		Revision      int64             `json:"revision"`
		Candidate     profile.Candidate `json:"candidate"`
		CloudApproved bool              `json:"cloud_approved"`
	}
	if !h.decode(w, r, &input) {
		return
	}
	v, err := h.profiles.Confirm(r.Context(), scope, r.PathValue("id"), input.Revision, input.Candidate, input.CloudApproved, requestID(r), h.service.Now())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.writeJSON(w, r, 201, map[string]any{"profile": v})
}
func (h *Handler) profileActivate(w http.ResponseWriter, r *http.Request, p identity.Principal, _ string) {
	scope, ok := h.profileScope(w, r, p)
	if !ok || !h.validID(w, r) {
		return
	}
	if !uuidPattern.MatchString(r.PathValue("versionID")) {
		h.fail(w, r, identity.ErrNotFound)
		return
	}
	if err := h.profiles.Activate(r.Context(), scope, r.PathValue("id"), r.PathValue("versionID"), requestID(r), h.service.Now()); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(204)
}
func (h *Handler) profileList(w http.ResponseWriter, r *http.Request, p identity.Principal, _ string) {
	scope, ok := h.profileScope(w, r, p)
	if !ok {
		return
	}
	items, err := h.profiles.Versions(r.Context(), scope, requestID(r), h.service.Now())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.writeJSON(w, r, 200, map[string]any{"items": items})
}
func (h *Handler) privateTask(w http.ResponseWriter, r *http.Request, p identity.Principal, _ string) {
	_, ok := h.profileScope(w, r, p)
	if !ok || !h.validID(w, r) {
		return
	}
	j, err := h.tasks.Get(r.Context(), p.User.ID, r.PathValue("id"), h.service.Now())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.writeJSON(w, r, 200, map[string]any{"task": j})
}
func (h *Handler) recommendationCreate(w http.ResponseWriter, r *http.Request, p identity.Principal, _ string) {
	scope, ok := h.profileScope(w, r, p)
	if !ok {
		return
	}
	var input struct {
		JobIDs   []string `json:"job_ids"`
		UseModel bool     `json:"use_model"`
		Retry    bool     `json:"retry"`
	}
	if !h.decode(w, r, &input) {
		return
	}
	for _, id := range input.JobIDs {
		if !uuidPattern.MatchString(id) {
			h.fail(w, r, identity.ErrInvalidInput)
			return
		}
	}
	items, err := h.matches.Generate(r.Context(), scope, input.JobIDs, input.UseModel, input.Retry, h.modelConfigured, h.modelVersion, requestID(r), h.service.Now())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.writeJSON(w, r, 202, map[string]any{"items": items})
}
func (h *Handler) recommendationList(w http.ResponseWriter, r *http.Request, p identity.Principal, _ string) {
	scope, ok := h.profileScope(w, r, p)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if id != "" && !h.validID(w, r) {
		return
	}
	limit, offset, ok := h.pagination(w, r)
	if !ok {
		return
	}
	items, err := h.matches.List(r.Context(), scope, id, limit, offset, requestID(r), h.service.Now(), h.modelVersion)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.writeJSON(w, r, 200, map[string]any{"items": items, "model_configured": h.modelConfigured})
}
