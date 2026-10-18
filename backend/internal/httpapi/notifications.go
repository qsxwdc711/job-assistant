package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"jobassistant/backend/internal/identity"
	"jobassistant/backend/internal/notification"
)

// WithNotifications registers owner-scoped subscription and administrator task routes.
func WithNotifications(store *notification.Store) Option {
	return func(h *Handler) error {
		if store == nil {
			return errors.New("notification store is required")
		}
		h.notifications = store
		return nil
	}
}

func (h *Handler) notificationRoutes() {
	for _, prefix := range []string{"/api/v1/me", "/api/v1/admin"} {
		h.mux.HandleFunc("GET "+prefix+"/subscriptions", h.protect(h.subscriptionGet, false))
		h.mux.HandleFunc("PUT "+prefix+"/subscriptions", h.protect(h.subscriptionSave, true))
		h.mux.HandleFunc("GET "+prefix+"/notification-channel", h.protect(h.channelGet, false))
		h.mux.HandleFunc("PUT "+prefix+"/notification-channel", h.protect(h.channelSave, true))
		h.mux.HandleFunc("DELETE "+prefix+"/notification-channel", h.protect(h.channelClear, true))
		h.mux.HandleFunc("POST "+prefix+"/notification-channel/test", h.protect(h.channelTest, true))
		h.mux.HandleFunc("GET "+prefix+"/notifications", h.protect(h.notificationsGet, false))
	}
	h.mux.HandleFunc("GET /api/v1/admin/automation", h.protect(h.automationStatus, false))
	h.mux.HandleFunc("POST /api/v1/admin/automation/collect", h.protect(h.collectionRequest, true))
	h.mux.HandleFunc("POST /api/v1/admin/automation/digest", h.protect(h.digestRequest, true))
	h.mux.HandleFunc("POST /api/v1/admin/notifications/{id}/resolve", h.protect(h.deliveryResolve, true))
}
func (h *Handler) notificationScope(w http.ResponseWriter, r *http.Request, p identity.Principal) (notification.Scope, bool) {
	scope := notification.Scope{ActorID: p.User.ID, Admin: strings.HasPrefix(r.URL.Path, "/api/v1/admin/")}
	if scope.Admin {
		scope.OwnerID = r.URL.Query().Get("owner_user_id")
		if !uuidPattern.MatchString(scope.OwnerID) {
			h.fail(w, r, identity.ErrInvalidInput)
			return scope, false
		}
	}
	return scope, true
}
func (h *Handler) subscriptionGet(w http.ResponseWriter, r *http.Request, p identity.Principal, _ string) {
	scope, ok := h.notificationScope(w, r, p)
	if !ok {
		return
	}
	value, err := h.notifications.Subscription(r.Context(), scope, h.service.Now())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.writeJSON(w, r, 200, map[string]any{"subscription": value})
}
func (h *Handler) subscriptionSave(w http.ResponseWriter, r *http.Request, p identity.Principal, id string) {
	scope, ok := h.notificationScope(w, r, p)
	if !ok {
		return
	}
	var input notification.Subscription
	if !h.decode(w, r, &input) {
		return
	}
	value, err := h.notifications.SaveSubscription(r.Context(), scope, input, id, h.service.Now())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.writeJSON(w, r, 200, map[string]any{"subscription": value})
}
func (h *Handler) channelGet(w http.ResponseWriter, r *http.Request, p identity.Principal, _ string) {
	scope, ok := h.notificationScope(w, r, p)
	if !ok {
		return
	}
	value, err := h.notifications.Channel(r.Context(), scope, h.service.Now())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.writeJSON(w, r, 200, map[string]any{"channel": value})
}
func (h *Handler) channelSave(w http.ResponseWriter, r *http.Request, p identity.Principal, id string) {
	scope, ok := h.notificationScope(w, r, p)
	if !ok {
		return
	}
	var input notification.ChannelInput
	if !h.decode(w, r, &input) {
		return
	}
	value, err := h.notifications.SaveChannel(r.Context(), scope, input, id, h.service.Now())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.writeJSON(w, r, 200, map[string]any{"channel": value})
}
func (h *Handler) channelClear(w http.ResponseWriter, r *http.Request, p identity.Principal, id string) {
	scope, ok := h.notificationScope(w, r, p)
	if !ok {
		return
	}
	var input struct {
		Revision int64 `json:"revision"`
	}
	if !h.decode(w, r, &input) {
		return
	}
	if err := h.notifications.ClearChannel(r.Context(), scope, input.Revision, id, h.service.Now()); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(204)
}
func (h *Handler) channelTest(w http.ResponseWriter, r *http.Request, p identity.Principal, id string) {
	scope, ok := h.notificationScope(w, r, p)
	if !ok {
		return
	}
	value, err := h.notifications.Test(r.Context(), scope, id, h.service.Now())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.writeJSON(w, r, 202, map[string]string{"batch_id": value})
}
func (h *Handler) notificationsGet(w http.ResponseWriter, r *http.Request, p identity.Principal, _ string) {
	scope, ok := h.notificationScope(w, r, p)
	if !ok {
		return
	}
	limit, offset, ok := h.pagination(w, r)
	if !ok {
		return
	}
	value, err := h.notifications.List(r.Context(), scope, limit, offset, h.service.Now())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.writeJSON(w, r, 200, map[string]any{"items": value})
}
func (h *Handler) automationStatus(w http.ResponseWriter, r *http.Request, p identity.Principal, _ string) {
	value, err := h.notifications.AutomationStatus(r.Context(), p.User.ID, h.service.Now())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.writeJSON(w, r, 200, map[string]any{"items": value})
}
func (h *Handler) collectionRequest(w http.ResponseWriter, r *http.Request, p identity.Principal, id string) {
	var input struct {
		Source string `json:"source_code"`
	}
	if !h.decode(w, r, &input) {
		return
	}
	value, err := h.notifications.RequestCollection(r.Context(), p.User.ID, input.Source, id, h.service.Now())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.writeJSON(w, r, 202, map[string]string{"task_id": value})
}
func (h *Handler) digestRequest(w http.ResponseWriter, r *http.Request, p identity.Principal, id string) {
	value, err := h.notifications.RequestDigest(r.Context(), p.User.ID, id, h.service.Now())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.writeJSON(w, r, 202, map[string]string{"task_id": value})
}
func (h *Handler) deliveryResolve(w http.ResponseWriter, r *http.Request, p identity.Principal, id string) {
	if !h.validID(w, r) {
		return
	}
	var input struct {
		Decision string `json:"decision"`
	}
	if !h.decode(w, r, &input) {
		return
	}
	if err := h.notifications.ResolveUnknown(r.Context(), p.User.ID, r.PathValue("id"), input.Decision, id, h.service.Now()); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(204)
}
