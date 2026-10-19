package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"jobassistant/backend/internal/catalog"
	"jobassistant/backend/internal/identity"
	"jobassistant/backend/internal/ingest"
)

func chaitinSourceServer(t *testing.T, mode string) *httptest.Server {
	t.Helper()
	body, err := os.ReadFile("../../internal/ingest/testdata/chaitin/jobs.json")
	if err != nil {
		t.Fatal(err)
	}
	var samples []map[string]any
	if err = json.Unmarshal(body, &samples); err != nil {
		t.Fatal(err)
	}
	items := []map[string]any{samples[0], samples[2]}
	if mode == "metadata" {
		for _, item := range items {
			item["updated_at"] = 1792400000
		}
	}
	template, err := os.ReadFile("../../internal/ingest/testdata/chaitin/detail.html")
	if err != nil {
		t.Fatal(err)
	}
	writeJSON := func(w http.ResponseWriter, value any) {
		if err := json.NewEncoder(w).Encode(value); err != nil {
			t.Error(err)
		}
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/plugins/career_site/sites/default" || strings.HasPrefix(r.URL.Path, "/plugins/career_site/sites/default/jobs/") {
			id := fmt.Sprint(items[0]["job_id"])
			if strings.HasPrefix(r.URL.Path, "/plugins/career_site/sites/default/jobs/") {
				id = strings.TrimPrefix(r.URL.Path, "/plugins/career_site/sites/default/jobs/")
			}
			page := strings.ReplaceAll(string(template), fmt.Sprint(samples[0]["job_id"]), id)
			if _, err := fmt.Fprint(w, page); err != nil {
				t.Error(err)
			}
			return
		}
		if r.URL.Path == "/plugins/career_site/api/default/jobs" {
			if r.Method != "GET" || r.URL.Query().Get("page") != "1" || r.URL.Query().Get("size") != "10" {
				t.Error("public pagination query changed")
			}
			page := items
			if mode == "empty" {
				page = []map[string]any{}
			}
			writeJSON(w, map[string]any{"code": 0, "data": map[string]any{"total_count": len(page), "has_next_page": false, "items": page}})
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/plugins/career_site/api/default/jobs/")
		for _, job := range items {
			if fmt.Sprint(job["job_id"]) == id {
				detail := job
				if mode == "failure" && id == fmt.Sprint(items[1]["job_id"]) {
					detail = map[string]any{"job_id": id, "title": "错误详情"}
				}
				writeJSON(w, map[string]any{"code": 0, "data": map[string]any{"job": detail}})
				return
			}
		}
		w.WriteHeader(404)
	}))
}

func TestChaitinPublicationVersionsFailuresAndPermissionIsolation(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
	defer cancel()
	store := publishEvidence(t, f, evidenceJob())
	standard := f.user(t, "chaitin-reader", identity.RoleStandard, nil)
	filter := catalog.Filter{Company: "腾讯", IncludeUnknown: true, Limit: 20}
	before, err := store.Jobs(ctx, standard.ID, filter, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	collect := func(mode string) ingest.Result {
		server := chaitinSourceServer(t, mode)
		defer server.Close()
		reader := ingest.NewReader()
		reader.ChaitinBase = server.URL
		return ingest.Collect(ctx, store, reader, "chaitin-careers")
	}
	for _, mode := range []string{"success", "success", "metadata"} {
		if result := collect(mode); result.Status != "succeeded" || result.Fetched != 2 || result.Published != 2 {
			t.Fatalf("complete Chaitin publication: %#v", result)
		}
	}
	var total, version int
	if err := f.pool.QueryRow(ctx, "SELECT count(*),max(version) FROM jobs WHERE source_code='chaitin-careers'").Scan(&total, &version); err != nil || total != 2 || version != 1 {
		t.Fatalf("repeat/metadata created duplicate versions: %d %d %v", total, version, err)
	}
	if result := collect("failure"); result.Status != "failed" || result.ErrorCode != "schema_changed" {
		t.Fatalf("late bad detail was partially published: %#v", result)
	}
	if result := collect("empty"); result.Status != "succeeded" || result.Published != 0 {
		t.Fatalf("valid empty list rejected: %#v", result)
	}
	if err := f.pool.QueryRow(ctx, "SELECT count(*),max(version) FROM jobs WHERE source_code='chaitin-careers' AND published").Scan(&total, &version); err != nil || total != 2 || version != 1 {
		t.Fatalf("failure/empty removed published content: %d %d %v", total, version, err)
	}
	after, err := store.Jobs(ctx, standard.ID, filter, time.Now())
	if err != nil || !reflect.DeepEqual(before.Items, after.Items) {
		t.Fatalf("new source changed existing jobs: %v", err)
	}
	choices, err := store.Filters(ctx, standard.ID, time.Now())
	if err != nil || choices.SourceNames["chaitin-careers"] != "长亭科技官方招聘" {
		t.Fatalf("public source name missing: %v", err)
	}
	if _, err = store.Sources(ctx, standard.ID, time.Now()); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("ordinary user gained source administration access: %v", err)
	}
}
