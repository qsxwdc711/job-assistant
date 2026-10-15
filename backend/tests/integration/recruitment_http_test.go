package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"jobassistant/backend/internal/application"
	"jobassistant/backend/internal/catalog"
	"jobassistant/backend/internal/httpapi"
	"jobassistant/backend/internal/identity"
)

func TestRecruitmentHTTPIsolationAndVersionConflict(t *testing.T) {
	f := newFixture(t)
	store := publishEvidence(t, f, evidenceJob())
	ctx := context.Background()
	now := time.Now().UTC()
	f.user(t, "http-apply-a", identity.RoleStandard, nil)
	f.user(t, "http-apply-b", identity.RoleStandard, nil)
	handler, err := httpapi.NewHandler(f.service, httpapi.Config{CSRFKey: bytes.Repeat([]byte{3}, 32), Origin: "http://app.test"}, f.pool.Ping, httpapi.WithRecruitment(store, application.NewStore(f.pool)))
	if err != nil {
		t.Fatal(err)
	}
	defer handler.Close()
	server := httptest.NewServer(handler)
	defer server.Close()
	a, b, admin := newBrowser(t, server), newBrowser(t, server), newBrowser(t, server)
	a.login(t, "http-apply-a")
	b.login(t, "http-apply-b")
	admin.login(t, "admin")
	page, err := store.Jobs(ctx, f.admin.ID, catalog.Filter{Limit: 20}, now)
	if err != nil {
		t.Fatal(err)
	}
	jobID := page.Items[0].ID
	if status, _, _ := a.request(t, "POST", "/api/v1/me/applications", map[string]any{"job_id": jobID, "status": "submitted|written_test"}, true); status != 400 {
		t.Fatalf("combined status must be rejected before SQL: %d", status)
	}
	if status, _, _ := b.request(t, "GET", "/api/v1/admin/jobs", nil, false); status != 403 {
		t.Fatalf("ordinary admin list: %d", status)
	}
	if status, _, _ := a.request(t, "PUT", "/api/v1/me/favorites/"+jobID, nil, false); status != 403 {
		t.Fatalf("favorite without CSRF: %d", status)
	}
	if status, _, _ := a.request(t, "PUT", "/api/v1/me/favorites/"+jobID, nil, true); status != 204 {
		t.Fatalf("save favorite: %d", status)
	}
	status, data, _ := a.request(t, "POST", "/api/v1/me/applications", map[string]any{"job_id": jobID, "status": "submitted"}, true)
	if status != 201 {
		t.Fatalf("create application: %d %s", status, data)
	}
	var created struct {
		Application application.Record `json:"application"`
	}
	if err := json.Unmarshal(data, &created); err != nil {
		t.Fatal(err)
	}
	id := created.Application.ID
	if status, _, _ := b.request(t, "GET", "/api/v1/me/applications/"+id, nil, false); status != 404 {
		t.Fatalf("cross-owner read: %d", status)
	}
	if status, _, _ := b.request(t, "GET", "/api/v1/me/applications/"+id+"/events", nil, false); status != 404 {
		t.Fatalf("cross-owner events: %d", status)
	}
	input := map[string]any{"status": "interview", "version": 1, "notes": "电脑端备注"}
	if status, _, _ := a.request(t, "PATCH", "/api/v1/me/applications/"+id, input, true); status != 200 {
		t.Fatalf("edit: %d", status)
	}
	if status, data, _ := a.request(t, "PATCH", "/api/v1/me/applications/"+id, input, true); status != 409 || !strings.Contains(string(data), "version_conflict") {
		t.Fatalf("lost update: %d %s", status, data)
	}
	if status, _, _ := admin.request(t, "GET", "/api/v1/admin/applications/"+id, nil, false); status != 200 {
		t.Fatalf("administrator access: %d", status)
	}
	if status, _, _ := admin.request(t, "PATCH", "/api/v1/admin/jobs/"+jobID, map[string]any{"published": false, "note": "待复核"}, true); status != 204 {
		t.Fatalf("hide job: %d", status)
	}
	if status, _, _ := a.request(t, "GET", "/api/v1/jobs/"+jobID, nil, false); status != 404 {
		t.Fatalf("hidden public job: %d", status)
	}
	if status, _, _ := a.request(t, "GET", "/api/v1/me/applications/"+id, nil, false); status != 200 {
		t.Fatalf("private progress lost after hiding: %d", status)
	}
}

func TestFrontendFallbackDoesNotExposeAPIOrPrivatePaths(t *testing.T) {
	f := newFixture(t)
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "index.html"), []byte("<!doctype html><title>local test app</title>"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, ".env"), []byte("private-local-fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	handler, err := httpapi.NewHandler(f.service, httpapi.Config{CSRFKey: bytes.Repeat([]byte{4}, 32), Origin: "http://app.test"}, f.pool.Ping, httpapi.WithFrontend(directory))
	if err != nil {
		t.Fatal(err)
	}
	defer handler.Close()
	for _, test := range []struct {
		path   string
		status int
	}{{"/jobs", 200}, {"/api/v1/absent", 404}, {"/assets/missing.js", 404}, {"/.env", 404}} {
		request := httptest.NewRequest("GET", test.path, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != test.status || strings.Contains(response.Body.String(), "private-local-fixture") {
			t.Fatalf("static boundary %s: %d", test.path, response.Code)
		}
	}
}
