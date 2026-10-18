package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"jobassistant/backend/internal/application"
	"jobassistant/backend/internal/catalog"
	"jobassistant/backend/internal/httpapi"
	"jobassistant/backend/internal/identity"
	"jobassistant/backend/internal/notification"
	"jobassistant/backend/internal/task"
)

const hookA = "https://open.feishu.cn/open-apis/bot/v2/hook/11111111-1111-1111-1111-111111111111"
const hookB = "https://open.feishu.cn/open-apis/bot/v2/hook/22222222-2222-2222-2222-222222222222"

func notificationStore(t *testing.T, f *fixture, send bool) *notification.Store {
	t.Helper()
	store, err := notification.NewStore(f.pool, notification.Options{Key: bytes.Repeat([]byte{4}, 32), Origin: "http://app.test", ModelVersion: "isolated-test-model", SendEnabled: send})
	if err != nil {
		t.Fatal(err)
	}
	return store
}
func subscribe(t *testing.T, store *notification.Store, user identity.User, query, hook string, now time.Time) {
	t.Helper()
	ctx := context.Background()
	scope := notification.Scope{ActorID: user.ID}
	if _, err := store.SaveSubscription(ctx, scope, notification.Subscription{FiltersQuery: query, Mode: "filtered", Enabled: true}, "test", now); err != nil {
		t.Fatal(err)
	}
	secret := "isolated-only-signing-key"
	if _, err := store.SaveChannel(ctx, scope, notification.ChannelInput{Webhook: &hook, Secret: &secret, Enabled: true}, "test", now); err != nil {
		t.Fatal(err)
	}
}

type notificationTransport struct{ target string }

func (tr notificationTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	copy := request.Clone(request.Context())
	target, err := http.NewRequest("POST", tr.target, nil)
	if err != nil {
		return nil, err
	}
	endpoint := *request.URL
	endpoint.Scheme, endpoint.Host = target.URL.Scheme, target.URL.Host
	copy.URL = &endpoint
	return http.DefaultTransport.RoundTrip(copy)
}

func TestNotificationTwoUsersRoutingFiltersEncryptionAndHTTPIsolation(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	publishEvidence(t, f, evidenceJob())
	a := f.user(t, "notify-a", identity.RoleStandard, nil)
	b := f.user(t, "notify-b", identity.RoleStandard, nil)
	store := notificationStore(t, f, true)
	now := time.Now().UTC().Add(time.Second)
	subscribe(t, store, a, "direction=backend&city=深圳市", hookA, now)
	subscribe(t, store, b, "direction=data&city=合肥市", hookB, now)
	var mutex sync.Mutex
	received := map[string]string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Content struct {
				Text string `json:"text"`
			} `json:"content"`
			Sign string `json:"sign"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if payload.Sign == "" {
			t.Error("signature missing")
		}
		mutex.Lock()
		received[r.URL.Path] = payload.Content.Text
		mutex.Unlock()
		if _, err := io.WriteString(w, `{"code":0}`); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	sender := notification.NewSender(&http.Client{Transport: notificationTransport{target: server.URL}})
	for _, user := range []identity.User{a, b} {
		id, err := store.Generate(ctx, user.ID, "same-day", now, now)
		if err != nil || id == "" {
			t.Fatalf("digest: %s %v", id, err)
		}
		repeat, err := store.Generate(ctx, user.ID, "same-day", now, now)
		if err != nil || repeat != id {
			t.Fatal("duplicate daily batch")
		}
	}
	for range 2 {
		worked, err := store.DispatchOne(ctx, sender, now)
		if err != nil || !worked {
			t.Fatalf("delivery: %v", err)
		}
	}
	mutex.Lock()
	defer mutex.Unlock()
	if len(received) != 2 {
		t.Fatalf("deliveries=%d", len(received))
	}
	for path, text := range received {
		if strings.Contains(path, "1111") && (!strings.Contains(text, "深圳市") || strings.Contains(text, "合肥市")) {
			t.Fatal("other direction city reached A")
		}
		if strings.Contains(path, "2222") && (!strings.Contains(text, "合肥市") || strings.Contains(text, "深圳市")) {
			t.Fatal("other direction city reached B")
		}
		for _, private := range []string{"notify-a", "notify-b", "isolated-only-signing-key", "13800138000", "备注"} {
			if strings.Contains(text, private) {
				t.Fatalf("private data reached group: %s", private)
			}
		}
	}
	var raw []byte
	if err := f.pool.QueryRow(ctx, "SELECT encrypted_config FROM notification_channels WHERE owner_user_id=$1", a.ID).Scan(&raw); err != nil || bytes.Contains(raw, []byte(hookA)) || bytes.Contains(raw, []byte("isolated-only-signing-key")) {
		t.Fatal("plaintext stored in channel")
	}
	handler, err := httpapi.NewHandler(f.service, httpapi.Config{CSRFKey: bytes.Repeat([]byte{6}, 32), Origin: "http://app.test"}, f.pool.Ping, httpapi.WithNotifications(store))
	if err != nil {
		t.Fatal(err)
	}
	defer handler.Close()
	apiServer := httptest.NewServer(handler)
	defer apiServer.Close()
	browser := newBrowser(t, apiServer)
	browser.login(t, "notify-a")
	status, data, _ := browser.request(t, "GET", "/api/v1/me/notification-channel", nil, false)
	if status != 200 || bytes.Contains(data, []byte(hookA)) || bytes.Contains(data, []byte("isolated-only-signing-key")) {
		t.Fatalf("channel read leaks secret: %d", status)
	}
	if status, _, _ := browser.request(t, "GET", "/api/v1/admin/notifications?owner_user_id="+b.ID, nil, false); status != 403 {
		t.Fatalf("cross-user admin notification read: %d", status)
	}
	if status, _, _ := browser.request(t, "PUT", "/api/v1/me/subscriptions", notification.Subscription{Mode: "personalized", Enabled: true, Revision: 1}, true); status != 403 {
		t.Fatalf("ordinary personalized subscription: %d", status)
	}
	if status, _, _ := browser.request(t, "PUT", "/api/v1/me/subscriptions", notification.Subscription{Mode: "filtered", Enabled: true, Revision: 0}, true); status != 409 {
		t.Fatalf("stale revision: %d", status)
	}
	if status, _, _ := browser.request(t, "PUT", "/api/v1/me/subscriptions", notification.Subscription{Mode: "filtered", Enabled: true, Revision: 1}, false); status != 403 {
		t.Fatalf("subscription without CSRF: %d", status)
	}
}

func TestNotificationCutoffLateChangesAndNearestDeadline(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	store := notificationStore(t, f, false)
	user := f.user(t, "late-source", identity.RoleStandard, nil)
	_, start, cutoff := task.DayTimes(time.Now())
	subscribe(t, store, user, "", hookA, start)
	doc := evidenceJob()
	deadline := cutoff.Add(36 * time.Hour)
	doc.DeadlineAt = &deadline
	doc.DeadlineDate = deadline.In(task.Shanghai).Format("2006-01-02")
	catalogStore := catalog.NewStore(f.pool)
	run, err := catalogStore.BeginRun(ctx, doc.SourceCode, start)
	if err != nil {
		t.Fatal(err)
	}
	if err = catalogStore.Publish(ctx, run, []catalog.Document{doc}, 1, nil, cutoff.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Generate(ctx, user.ID, "daily-before-late", cutoff, cutoff.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	list, err := store.List(ctx, notification.Scope{ActorID: user.ID}, 20, 0, cutoff.Add(2*time.Minute))
	if err != nil || len(list) != 1 || len(list[0].Items) != 0 || list[0].Sources["tencent-campus"] != "not_completed" {
		t.Fatalf("late source entered cutoff: %#v %v", list, err)
	}
	next := cutoff.Add(24 * time.Hour)
	if _, err = store.Generate(ctx, user.ID, "next-day", next, next); err != nil {
		t.Fatal(err)
	}
	list, err = store.List(ctx, notification.Scope{ActorID: user.ID}, 20, 0, next)
	if err != nil || len(list[0].Items) != 1 {
		t.Fatalf("late source lost on next day: %v", err)
	}
	var reminders int
	if err = f.pool.QueryRow(ctx, "SELECT count(*) FROM notification_events WHERE owner_user_id=$1 AND kind='deadline'", user.ID).Scan(&reminders); err != nil || reminders != 1 {
		t.Fatalf("nearest deadline reminders=%d %v", reminders, err)
	}
	if _, err = store.Generate(ctx, user.ID, "duplicate-next-day", next.Add(time.Minute), next.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	var events int
	if err = f.pool.QueryRow(ctx, "SELECT count(*) FROM notification_events WHERE owner_user_id=$1", user.ID).Scan(&events); err != nil || events != 2 {
		t.Fatalf("duplicate events=%d %v", events, err)
	}
}

func TestNotificationRetriesUnknownRecoveryAndCurrentApplication(t *testing.T) {
	for _, mode := range []string{"reject", "crashed_send", "applied", "channel_off", "account_off"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()
			catalogStore := publishEvidence(t, f, evidenceJob())
			user := f.user(t, "delivery-user", identity.RoleStandard, nil)
			store := notificationStore(t, f, true)
			now := time.Now().UTC().Add(time.Second)
			subscribe(t, store, user, "", hookA, now)
			id, err := store.Generate(ctx, user.ID, "batch", now, now)
			if err != nil {
				t.Fatal(err)
			}
			var calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if _, err := io.WriteString(w, `{"code":19024}`); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			sender := notification.NewSender(&http.Client{Transport: notificationTransport{target: server.URL}})
			switch mode {
			case "crashed_send":
				_, err = f.pool.Exec(ctx, "UPDATE notification_deliveries SET state='sending',sending_at=$2 WHERE batch_id=$1", id, now.Add(-time.Minute))
			case "applied":
				page, e := catalogStore.Jobs(ctx, f.admin.ID, catalog.Filter{Limit: 20}, now)
				if e != nil {
					t.Fatal(e)
				}
				_, err = application.NewStore(f.pool).Create(ctx, application.Scope{ActorID: user.ID}, application.Input{JobID: page.Items[0].ID, Status: "submitted", Notes: "private test note"}, "test", now)
			case "channel_off":
				_, err = store.SaveChannel(ctx, notification.Scope{ActorID: user.ID}, notification.ChannelInput{Enabled: false, Revision: 1}, "test", now)
			case "account_off":
				_, err = f.service.SetDisabled(ctx, f.admin, user.ID, true, "test")
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = store.DispatchOne(ctx, sender, now); err != nil {
				t.Fatal(err)
			}
			if mode == "reject" {
				if _, err = store.DispatchOne(ctx, sender, now.Add(30*time.Second)); err != nil {
					t.Fatal(err)
				}
				if calls.Load() != 1 {
					t.Fatal("retry ignored backoff")
				}
				for _, delay := range []time.Duration{2 * time.Minute, 7 * time.Minute, 30 * time.Minute} {
					if _, err = store.DispatchOne(ctx, sender, now.Add(delay)); err != nil {
						t.Fatal(err)
					}
				}
				if calls.Load() != 3 {
					t.Fatalf("attempt bound: %d", calls.Load())
				}
			} else if calls.Load() != 0 {
				t.Fatal("cancelled or unknown batch reached HTTP")
			}
			list, err := store.List(ctx, notification.Scope{ActorID: f.admin.ID, OwnerID: user.ID, Admin: true}, 20, 0, now)
			if err != nil {
				t.Fatal(err)
			}
			want := "cancelled"
			if mode == "reject" {
				want = "failed"
			}
			if mode == "crashed_send" {
				want = "delivery_unknown"
			}
			if list[0].State != want {
				t.Fatalf("got %s want %s", list[0].State, want)
			}
		})
	}
}

func TestNotificationAdminQueueAndPermissionRecheck(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	publishEvidence(t, f, evidenceJob())
	store := notificationStore(t, f, false)
	user := f.user(t, "manual-digest", identity.RoleStandard, nil)
	now := time.Now().UTC().Add(time.Second)
	subscribe(t, store, user, "", hookA, now)
	if _, err := store.RequestDigest(ctx, user.ID, "unauthorized", now); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("ordinary user requested a global digest: %v", err)
	}
	request, err := store.RequestDigest(ctx, f.admin.ID, "manual", now)
	if err != nil {
		t.Fatal(err)
	}
	if worked, err := store.ProcessRequest(ctx, now); err != nil || !worked {
		t.Fatalf("manual digest processing: %v", err)
	}
	var state string
	if err = f.pool.QueryRow(ctx, "SELECT state FROM digest_requests WHERE id=$1", request).Scan(&state); err != nil || state != "succeeded" {
		t.Fatalf("manual digest state: %s %v", state, err)
	}
	if worked, err := store.ProcessRequest(ctx, now); err != nil || worked {
		t.Fatalf("completed request reclaimed: %v", err)
	}
	if worked, err := store.DispatchOne(ctx, nil, now); err != nil || !worked {
		t.Fatalf("simulation required an HTTP sender: %v", err)
	}
	batches, err := store.List(ctx, notification.Scope{ActorID: user.ID}, 20, 0, now)
	if err != nil || len(batches) != 1 || batches[0].State != "simulated" || len(batches[0].Items) != 1 {
		t.Fatalf("manual digest did not reach the station: %#v %v", batches, err)
	}
	second := f.user(t, "queued-admin", identity.RoleAdmin, nil)
	request, err = store.RequestDigest(ctx, second.ID, "before-revocation", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.SetDisabled(ctx, f.admin, second.ID, true, "revoke-queued-request"); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ProcessRequest(ctx, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err = f.pool.QueryRow(ctx, "SELECT state FROM digest_requests WHERE id=$1", request).Scan(&state); err != nil || state != "cancelled" {
		t.Fatalf("revoked admin request executed: %s %v", state, err)
	}
	if _, err = store.RequestCollection(ctx, user.ID, "tencent-campus", "unauthorized", now); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("ordinary user requested collection: %v", err)
	}
	if _, err = store.RequestCollection(ctx, f.admin.ID, "tencent-campus", "manual", now); err != nil {
		t.Fatal(err)
	}
	if status, err := store.AutomationStatus(ctx, f.admin.ID, now); err != nil || len(status) != 1 || status[0]["state"] != "queued" {
		t.Fatalf("collection was not queued: %#v %v", status, err)
	}
}

func TestNotificationUnknownRequiresExplicitAdminConclusion(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	publishEvidence(t, f, evidenceJob())
	store := notificationStore(t, f, true)
	user := f.user(t, "unknown-delivery", identity.RoleStandard, nil)
	now := time.Now().UTC().Add(time.Second)
	subscribe(t, store, user, "", hookA, now)
	batch, err := store.Generate(ctx, user.ID, "unknown", now, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(ctx, "UPDATE notification_deliveries SET state='sending',attempts=1,sending_at=$2 WHERE batch_id=$1", batch, now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err = store.DispatchOne(ctx, nil, now); err != nil {
		t.Fatal(err)
	}
	if err = store.ResolveUnknown(ctx, user.ID, batch, "not_received", "test", now); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("ordinary user resolved unknown delivery: %v", err)
	}
	if err = store.ResolveUnknown(ctx, f.admin.ID, batch, "not_received", "test", now); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if _, err := io.WriteString(w, `{"code":0}`); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	sender := notification.NewSender(&http.Client{Transport: notificationTransport{target: server.URL}})
	if worked, err := store.DispatchOne(ctx, sender, now); err != nil || !worked || calls.Load() != 1 {
		t.Fatalf("explicitly confirmed retry: worked=%t calls=%d err=%v", worked, calls.Load(), err)
	}
	batches, err := store.List(ctx, notification.Scope{ActorID: user.ID}, 20, 0, now)
	if err != nil || batches[0].State != "sent" || batches[0].Attempts != 2 {
		t.Fatalf("retry did not reuse the delivery: %#v %v", batches, err)
	}
	var audits int
	if err = f.pool.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE action='notification.delivery.resolve' AND target_id=$1", batch).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("resolution not audited: %d %v", audits, err)
	}
}
