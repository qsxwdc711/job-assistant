// Package httpapi exposes the versioned identity API with cookie and CSRF checks.
package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"jobassistant/backend/internal/application"
	"jobassistant/backend/internal/catalog"
	"jobassistant/backend/internal/identity"
	"jobassistant/backend/internal/matching"
	"jobassistant/backend/internal/notification"
	"jobassistant/backend/internal/profile"
	"jobassistant/backend/internal/task"
)

// CookieName is the opaque, HttpOnly session cookie name.
const CookieName = "ja_session"

type requestIDKey struct{}

// Config defines cookie security and the deployment origin.
type Config struct {
	CSRFKey       []byte
	SecureCookies bool
	Origin        string
}

// Handler owns the identity routes and bounded login protections.
type Handler struct {
	service         *identity.Service
	config          Config
	ready           func(context.Context) error
	mux             *http.ServeMux
	limiter         loginLimiter
	hashSlots       chan struct{}
	catalog         *catalog.Store
	applications    *application.Store
	frontend        *frontend
	profiles        *profile.Store
	matches         *matching.Store
	tasks           *task.Store
	modelConfigured bool
	modelVersion    string
	notifications   *notification.Store
}

// NewHandler creates the API. Ready should check the actual database.
func NewHandler(service *identity.Service, cfg Config, ready func(context.Context) error, options ...Option) (*Handler, error) {
	if service == nil || len(cfg.CSRFKey) != 32 || cfg.Origin == "" || ready == nil {
		return nil, errors.New("invalid HTTP configuration")
	}
	cfg.CSRFKey = append([]byte(nil), cfg.CSRFKey...)
	h := &Handler{service: service, config: cfg, ready: ready, mux: http.NewServeMux(), limiter: loginLimiter{buckets: make(map[string]loginBucket)}, hashSlots: make(chan struct{}, 2)}
	for _, option := range options {
		if err := option(h); err != nil {
			return nil, errors.Join(err, h.Close())
		}
	}
	h.mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, r *http.Request) {
		h.writeJSON(w, r, http.StatusOK, map[string]string{"status": "ok"})
	})
	h.mux.HandleFunc("GET /health/ready", h.readiness)
	h.mux.HandleFunc("POST /api/v1/auth/login", h.login)
	h.mux.HandleFunc("GET /api/v1/auth/me", h.protect(h.me, false))
	h.mux.HandleFunc("POST /api/v1/auth/logout", h.protect(h.logout, true))
	h.mux.HandleFunc("GET /api/v1/auth/sessions", h.protect(h.sessions, false))
	h.mux.HandleFunc("DELETE /api/v1/auth/sessions/{id}", h.protect(h.revokeSession, true))
	h.mux.HandleFunc("GET /api/v1/admin/users", h.protect(h.users, false))
	h.mux.HandleFunc("POST /api/v1/admin/users", h.protect(h.createUser, true))
	h.mux.HandleFunc("PATCH /api/v1/admin/users/{id}/role", h.protect(h.changeRole, true))
	h.mux.HandleFunc("PATCH /api/v1/admin/users/{id}/disabled", h.protect(h.setDisabled, true))
	h.mux.HandleFunc("GET /api/v1/admin/audit-events", h.protect(h.audit, false))
	if h.catalog != nil {
		h.recruitmentRoutes()
	}
	if h.profiles != nil {
		h.resumeRoutes()
	}
	if h.notifications != nil {
		h.notificationRoutes()
	}
	h.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		apiPath := r.URL.Path == "/api" || r.URL.Path == "/health" || strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/health/")
		if h.frontend != nil && (r.Method == http.MethodGet || r.Method == http.MethodHead) && !apiPath {
			h.frontend.ServeHTTP(w, r)
			return
		}
		h.fail(w, r, identity.ErrNotFound)
	})
	return h, nil
}

// ServeHTTP supplies a fresh request identifier and rejects cross-origin writes.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return
	}
	id := hex.EncodeToString(raw)
	r = r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id))
	w.Header().Set("X-Request-ID", id)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	if r.Method != "GET" && r.Method != "HEAD" {
		if origin := r.Header.Get("Origin"); origin != "" && origin != h.config.Origin {
			h.fail(w, r, identity.ErrForbidden)
			return
		}
	}
	h.mux.ServeHTTP(w, r)
}

func requestID(r *http.Request) string {
	id, _ := r.Context().Value(requestIDKey{}).(string)
	return id
}

func (h *Handler) csrf(token string) string {
	mac := hmac.New(sha256.New, h.config.CSRFKey)
	if _, err := mac.Write([]byte("jobassistant-csrf:" + token)); err != nil {
		panic("hash write failed")
	}
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

type authenticatedHandler func(http.ResponseWriter, *http.Request, identity.Principal, string)

func (h *Handler) protect(next authenticatedHandler, write bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(CookieName)
		if err != nil {
			h.fail(w, r, identity.ErrUnauthenticated)
			return
		}
		p, err := h.service.Authenticate(r.Context(), cookie.Value)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		if write && !hmac.Equal([]byte(r.Header.Get("X-CSRF-Token")), []byte(h.csrf(cookie.Value))) {
			h.fail(w, r, identity.ErrForbidden)
			return
		}
		next(w, r, p, cookie.Value)
	}
}

func (h *Handler) readiness(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := h.ready(ctx); err != nil {
		h.writeJSON(w, r, http.StatusServiceUnavailable, map[string]any{"error": map[string]string{"code": "not_ready", "message": "database unavailable", "request_id": requestID(r)}})
		return
	}
	h.writeJSON(w, r, http.StatusOK, map[string]string{"status": "ready"})
}

func (h *Handler) decode(w http.ResponseWriter, r *http.Request, target any) bool {
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		h.writeError(w, r, http.StatusUnsupportedMediaType, "unsupported_media_type", "application/json required")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		var sizeErr *http.MaxBytesError
		if errors.As(err, &sizeErr) {
			h.writeError(w, r, http.StatusRequestEntityTooLarge, "body_too_large", "request body exceeds limit")
		} else {
			h.writeError(w, r, http.StatusBadRequest, "invalid_json", "invalid JSON request")
		}
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		h.writeError(w, r, http.StatusBadRequest, "invalid_json", "one JSON object required")
		return false
	}
	return true
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !h.decode(w, r, &input) {
		return
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	if !h.limiter.allow(ip, h.service.Now()) {
		w.Header().Set("Retry-After", "60")
		h.writeError(w, r, http.StatusTooManyRequests, "rate_limited", "retry later")
		return
	}
	select {
	case h.hashSlots <- struct{}{}:
		defer func() { <-h.hashSlots }()
	default:
		w.Header().Set("Retry-After", "1")
		h.writeError(w, r, http.StatusTooManyRequests, "busy", "retry later")
		return
	}
	result, err := h.service.Login(r.Context(), input.Username, input.Password)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: result.Token, Path: "/", HttpOnly: true, Secure: h.config.SecureCookies, SameSite: http.SameSiteLaxMode, Expires: result.ExpiresAt, MaxAge: 7 * 24 * 60 * 60})
	h.writeJSON(w, r, http.StatusOK, map[string]any{"user": result.Principal.User, "effective_role": result.Principal.User.EffectiveRole(h.service.Now()), "capabilities": identity.Capabilities(result.Principal.User, h.service.Now()), "csrf_token": h.csrf(result.Token), "expires_at": result.ExpiresAt})
}

func (h *Handler) me(w http.ResponseWriter, r *http.Request, p identity.Principal, token string) {
	h.writeJSON(w, r, http.StatusOK, map[string]any{"user": p.User, "effective_role": p.User.EffectiveRole(h.service.Now()), "capabilities": identity.Capabilities(p.User, h.service.Now()), "csrf_token": h.csrf(token)})
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request, p identity.Principal, _ string) {
	if err := h.service.RevokeSession(r.Context(), p.User, p.SessionID, requestID(r)); err != nil {
		h.fail(w, r, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: "", Path: "/", HttpOnly: true, Secure: h.config.SecureCookies, SameSite: http.SameSiteLaxMode, MaxAge: -1, Expires: time.Unix(1, 0)})
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) sessions(w http.ResponseWriter, r *http.Request, p identity.Principal, _ string) {
	items, err := h.service.Sessions(r.Context(), p)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.writeJSON(w, r, http.StatusOK, map[string]any{"items": items})
}

var uuidPattern = regexp.MustCompile(`^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$`)

func (h *Handler) validID(w http.ResponseWriter, r *http.Request) bool {
	if !uuidPattern.MatchString(r.PathValue("id")) {
		h.fail(w, r, identity.ErrNotFound)
		return false
	}
	return true
}

func (h *Handler) revokeSession(w http.ResponseWriter, r *http.Request, p identity.Principal, _ string) {
	if !h.validID(w, r) {
		return
	}
	if err := h.service.RevokeSession(r.Context(), p.User, r.PathValue("id"), requestID(r)); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) pagination(w http.ResponseWriter, r *http.Request) (int, int, bool) {
	limit, offset := 20, 0
	var err error
	if value := r.URL.Query().Get("limit"); value != "" {
		limit, err = strconv.Atoi(value)
		if err != nil {
			h.fail(w, r, identity.ErrInvalidInput)
			return 0, 0, false
		}
	}
	if value := r.URL.Query().Get("offset"); value != "" {
		offset, err = strconv.Atoi(value)
		if err != nil {
			h.fail(w, r, identity.ErrInvalidInput)
			return 0, 0, false
		}
	}
	if limit < 1 || limit > 100 || offset < 0 || offset > 1000000 {
		h.fail(w, r, identity.ErrInvalidInput)
		return 0, 0, false
	}
	return limit, offset, true
}

func (h *Handler) users(w http.ResponseWriter, r *http.Request, p identity.Principal, _ string) {
	limit, offset, ok := h.pagination(w, r)
	if !ok {
		return
	}
	items, err := h.service.Users(r.Context(), p.User, limit, offset)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.writeJSON(w, r, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) createUser(w http.ResponseWriter, r *http.Request, p identity.Principal, _ string) {
	if err := identity.Authorize(p.User, identity.ManageUsers, "", h.service.Now()); err != nil {
		h.fail(w, r, err)
		return
	}
	var input identity.CreateUserInput
	if !h.decode(w, r, &input) {
		return
	}
	u, err := h.service.CreateUser(r.Context(), p.User, input, requestID(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.writeJSON(w, r, http.StatusCreated, map[string]any{"user": u})
}

func (h *Handler) changeRole(w http.ResponseWriter, r *http.Request, p identity.Principal, _ string) {
	if err := identity.Authorize(p.User, identity.ManageUsers, "", h.service.Now()); err != nil {
		h.fail(w, r, err)
		return
	}
	if !h.validID(w, r) {
		return
	}
	var input struct {
		Role      identity.Role `json:"role"`
		ExpiresAt *time.Time    `json:"vip_expires_at"`
	}
	if !h.decode(w, r, &input) {
		return
	}
	u, err := h.service.ChangeRole(r.Context(), p.User, r.PathValue("id"), input.Role, input.ExpiresAt, requestID(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.writeJSON(w, r, http.StatusOK, map[string]any{"user": u})
}

func (h *Handler) setDisabled(w http.ResponseWriter, r *http.Request, p identity.Principal, _ string) {
	if err := identity.Authorize(p.User, identity.ManageUsers, "", h.service.Now()); err != nil {
		h.fail(w, r, err)
		return
	}
	if !h.validID(w, r) {
		return
	}
	var input struct {
		Disabled *bool `json:"disabled"`
	}
	if !h.decode(w, r, &input) {
		return
	}
	if input.Disabled == nil {
		h.fail(w, r, identity.ErrInvalidInput)
		return
	}
	u, err := h.service.SetDisabled(r.Context(), p.User, r.PathValue("id"), *input.Disabled, requestID(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.writeJSON(w, r, http.StatusOK, map[string]any{"user": u})
}

func (h *Handler) audit(w http.ResponseWriter, r *http.Request, p identity.Principal, _ string) {
	limit, offset, ok := h.pagination(w, r)
	if !ok {
		return
	}
	items, err := h.service.Audit(r.Context(), p.User, limit, offset)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.writeJSON(w, r, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, notification.ErrUnconfigured):
		h.writeError(w, r, 503, "notification_not_configured", "notification encryption key is not configured")
	case errors.Is(err, notification.ErrVersionConflict):
		h.writeError(w, r, 409, "version_conflict", "notification settings changed; refresh before saving")
	case errors.Is(err, profile.ErrInvalidFile):
		h.writeError(w, r, 400, "invalid_document", "PDF or DOCX required; document may be corrupt, encrypted, scanned or too large")
	case errors.Is(err, profile.ErrUnconfigured):
		h.writeError(w, r, 503, "model_not_configured", "model key is not configured; local review and manual confirmation remain available")
	case errors.Is(err, profile.ErrStale):
		h.writeError(w, r, 409, "profile_changed", "refresh the current profile or wait for its active task")
	case errors.Is(err, application.ErrVersionConflict):
		h.writeError(w, r, 409, "version_conflict", "record changed on another device; refresh before saving")
	case errors.Is(err, application.ErrAlreadyExists):
		h.writeError(w, r, 409, "application_exists", "an application for this job already exists")
	case errors.Is(err, identity.ErrUnauthenticated):
		h.writeError(w, r, 401, "unauthenticated", "authentication required")
	case errors.Is(err, identity.ErrForbidden):
		h.writeError(w, r, 403, "forbidden", "permission denied")
	case errors.Is(err, identity.ErrNotFound):
		h.writeError(w, r, 404, "not_found", "resource not found")
	case errors.Is(err, identity.ErrInvalidInput):
		h.writeError(w, r, 400, "invalid_input", err.Error())
	case errors.Is(err, identity.ErrConflict):
		h.writeError(w, r, 409, "user_exists", "username already exists")
	case errors.Is(err, identity.ErrLastAdministrator):
		h.writeError(w, r, 409, "last_administrator", err.Error())
	default:
		slog.Error("identity request failed", "request_id", requestID(r))
		h.writeError(w, r, 500, "internal_error", "request failed")
	}
}

func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	h.writeJSON(w, r, status, map[string]any{"error": map[string]string{"code": code, "message": message, "request_id": requestID(r)}})
}

func (h *Handler) writeJSON(w http.ResponseWriter, r *http.Request, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		slog.Warn("response write failed", "request_id", requestID(r))
	}
}
