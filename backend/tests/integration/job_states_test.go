package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"jobassistant/backend/internal/application"
	"jobassistant/backend/internal/catalog"
	"jobassistant/backend/internal/httpapi"
	"jobassistant/backend/internal/identity"
)

func TestPersonalJobStatesHTTPBoundsAndIsolation(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	second := evidenceJob()
	second.ExternalID = "job-states-second"
	catalogStore := publishEvidence(t, f, evidenceJob(), second)
	a := f.user(t, "states-a", identity.RoleStandard, nil)
	b := f.user(t, "states-b", identity.RoleVIP, nil)
	page, err := catalogStore.Jobs(ctx, a.ID, catalog.Filter{Limit: 20}, now)
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("fixture jobs: count=%d err=%v", len(page.Items), err)
	}
	firstID, secondID := page.Items[0].ID, page.Items[1].ID
	store := application.NewStore(f.pool)
	for _, entry := range []struct{ owner, favorite, recorded string }{{a.ID, firstID, secondID}, {b.ID, secondID, firstID}} {
		scope := application.Scope{ActorID: entry.owner}
		if err := store.SetFavorite(ctx, scope, entry.favorite, true, "test", now); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Create(ctx, scope, application.Input{JobID: entry.recorded, Status: "draft"}, "test", now); err != nil {
			t.Fatal(err)
		}
	}
	handler, err := httpapi.NewHandler(f.service, httpapi.Config{CSRFKey: bytes.Repeat([]byte{5}, 32), Origin: "http://app.test"}, f.pool.Ping, httpapi.WithRecruitment(catalogStore, store))
	if err != nil {
		t.Fatal(err)
	}
	defer handler.Close()
	server := httptest.NewServer(handler)
	defer server.Close()
	clientA, clientB, admin := newBrowser(t, server), newBrowser(t, server), newBrowser(t, server)
	clientA.login(t, "states-a")
	clientB.login(t, "states-b")
	admin.login(t, "admin")
	path := "/api/v1/me/job-states?job_id=" + firstID
	if status, _, _ := newBrowser(t, server).request(t, "GET", path, nil, false); status != 401 {
		t.Fatalf("anonymous states: got %d, want 401", status)
	}
	for _, query := range []string{"", "?job_id=", "?job_id=invalid", "?job_id=" + firstID + "&job_id=invalid", "?" + strings.Repeat("job_id="+firstID+"&", 101)} {
		if status, _, _ := clientA.request(t, "GET", "/api/v1/me/job-states"+query, nil, false); status != 400 {
			t.Fatalf("invalid states query %q: got %d, want 400", query, status)
		}
	}
	unknown := "00000000-0000-0000-0000-000000000000"
	query := url.Values{"job_id": {secondID, unknown, strings.ToUpper(firstID)}}
	query.Set("owner_user_id", b.ID)
	type state struct {
		JobID    string `json:"job_id"`
		Favorite bool   `json:"favorite"`
		Recorded bool   `json:"recorded"`
	}
	for _, entry := range []struct {
		name   string
		client *browser
		want   []state
	}{
		{"ordinary", clientA, []state{{secondID, false, true}, {unknown, false, false}, {firstID, true, false}}},
		{"vip", clientB, []state{{secondID, true, false}, {unknown, false, false}, {firstID, false, true}}},
		{"admin only self", admin, []state{{secondID, false, false}, {unknown, false, false}, {firstID, false, false}}},
	} {
		status, data, _ := entry.client.request(t, "GET", "/api/v1/me/job-states?"+query.Encode(), nil, false)
		if status != 200 {
			t.Fatalf("%s states: got %d, want 200", entry.name, status)
		}
		var result struct {
			Items []state `json:"items"`
		}
		if err := json.Unmarshal(data, &result); err != nil {
			t.Fatal(err)
		}
		if len(result.Items) != len(entry.want) {
			t.Fatalf("%s count: got %d, want %d", entry.name, len(result.Items), len(entry.want))
		}
		for i, got := range result.Items {
			if got != entry.want[i] {
				t.Fatalf("%s state %d: got %+v, want %+v", entry.name, i, got, entry.want[i])
			}
		}
	}
	status, data, _ := clientA.request(t, "GET", "/api/v1/me/job-states?"+strings.Repeat("job_id="+firstID+"&", 100), nil, false)
	var full struct {
		Items []state `json:"items"`
	}
	if err := json.Unmarshal(data, &full); err != nil {
		t.Fatal(err)
	}
	if status != 200 || len(full.Items) != 100 {
		t.Fatalf("maximum batch: status=%d count=%d", status, len(full.Items))
	}
	if err := catalogStore.SetPublished(ctx, f.admin.ID, firstID, false, "test hidden", "test", now); err != nil {
		t.Fatal(err)
	}
	status, data, _ = clientA.request(t, "GET", path, nil, false)
	if status != 200 || !strings.Contains(string(data), `"favorite":true`) {
		t.Fatalf("hidden job lost private state: %d %s", status, data)
	}
	if _, err := f.service.SetDisabled(ctx, f.admin, a.ID, true, "test"); err != nil {
		t.Fatal(err)
	}
	if status, _, _ := clientA.request(t, "GET", path, nil, false); status != 401 {
		t.Fatalf("disabled states: got %d, want 401", status)
	}
}

func TestPersonalJobStatesRechecksDisabledActorAndBatchSize(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	u := f.user(t, "states-disabled", identity.RoleStandard, nil)
	store := application.NewStore(f.pool)
	for _, ids := range [][]string{nil, make([]string, 101)} {
		if _, err := store.JobStates(ctx, u.ID, ids, now); !errors.Is(err, identity.ErrInvalidInput) {
			t.Fatalf("invalid batch: got %v, want invalid input", err)
		}
	}
	if _, err := f.service.SetDisabled(ctx, f.admin, u.ID, true, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.JobStates(ctx, u.ID, []string{"00000000-0000-0000-0000-000000000000"}, now); !errors.Is(err, identity.ErrUnauthenticated) {
		t.Fatalf("stale disabled actor: got %v, want unauthenticated", err)
	}
}
